package providerplatforms

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// fakeCanvas stands in for a Canvas instance's REST and GraphQL APIs.
type fakeCanvas struct {
	*httptest.Server
	users       []map[string]any
	courses     []map[string]any
	assignments map[string]int // GraphQL assignment count per course id
	submissions []map[string]any
	enrollments []map[string]any

	mu       sync.Mutex
	requests []string
}

func newFakeCanvas(t *testing.T) *fakeCanvas {
	t.Helper()
	fc := &fakeCanvas{assignments: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/accounts/{account}/users", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(fc.users)
	})
	mux.HandleFunc("GET /api/v1/accounts/{account}/courses", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(fc.courses)
	})
	mux.HandleFunc("POST /api/graphql", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		n := 0
		for id, count := range fc.assignments {
			if body.Query == `query { course(id: "`+id+`") { assignmentsConnection { nodes { _id } } } }` {
				n = count
			}
		}
		nodes := make([]map[string]string, n)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"course": map[string]any{"assignmentsConnection": map[string]any{"nodes": nodes}}},
		})
	})
	mux.HandleFunc("GET /api/v1/courses/{id}/students/submissions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(fc.submissions)
	})
	mux.HandleFunc("GET /api/v1/courses/{id}/enrollments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(fc.enrollments)
	})
	fc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fc.mu.Lock()
		fc.requests = append(fc.requests, r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization"))
		fc.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(fc.Close)
	return fc
}

func (fc *fakeCanvas) Requests() []string {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return append([]string(nil), fc.requests...)
}

func createCanvasProvider(t *testing.T, db *gorm.DB, fc *fakeCanvas) *ProviderPlatform {
	t.Helper()
	return createProvider(t, db, ProviderPlatform{
		Type:      CanvasCloud,
		Name:      "Canvas",
		AccountID: "1",
		AccessKey: " token-123\n",
		BaseUrl:   fc.URL,
	})
}

func canvasUser(id float64, name, sortable, short string, login any) map[string]any {
	return map[string]any{"id": id, "name": name, "sortable_name": sortable, "short_name": short, "login_id": login}
}

func TestCanvasUsers(t *testing.T) {
	db := newTestDB(t)
	fc := newFakeCanvas(t)
	fc.users = []map[string]any{
		canvasUser(11, "Ada Lovelace", "Lovelace, Ada", "Ada", "ada@example.com"),
		canvasUser(12, "Grace Hopper", "Hopper", "Grace", "grace@example.com"),
		canvasUser(13, "Plato", "Plato", "Plato", "plato@example.com"),
		canvasUser(14, "No Login", "Login, No", "No", nil),
		canvasUser(15, "Mapped User", "User, Mapped", "Mapped", "mapped@example.com"),
	}
	p := createCanvasProvider(t, db, fc)
	createResident(t, db, p.ID, 1, "student", "15")
	srv := newCanvasService(p, nil)

	all, err := srv.GetAllUsers()
	require.NoError(t, err)
	assert.Equal(t, []ImportUser{
		{ExternalUserID: "11", ExternalUsername: "ada@example.com", NameFirst: "Ada", NameLast: "Lovelace", Email: "ada@example.com", Username: "LovelaceAda"},
		{ExternalUserID: "12", ExternalUsername: "grace@example.com", NameFirst: "Grace", NameLast: "Hopper", Email: "grace@example.com", Username: "HopperGrace"},
		{ExternalUserID: "13", ExternalUsername: "plato@example.com", NameFirst: "Plato", NameLast: "Plato", Email: "plato@example.com", Username: "PlatoPlato"},
		{ExternalUserID: "15", ExternalUsername: "mapped@example.com", NameFirst: "Mapped", NameLast: "User", Email: "mapped@example.com", Username: "UserMapped"},
	}, all)

	unmapped, err := srv.GetUsers(&store{db: db})
	require.NoError(t, err)
	assert.Equal(t, all[:3], unmapped)

	assert.Equal(t, "GET /api/v1/accounts/1/users?per_page=1000&enrollment_type=student auth=Bearer token-123", fc.Requests()[0])
}

func TestCanvasUsersError(t *testing.T) {
	db := newTestDB(t)
	fc := newFakeCanvas(t)
	p := createCanvasProvider(t, db, fc)
	fc.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	})
	_, err := newCanvasService(p, nil).GetUsers(&store{db: db})
	assert.EqualError(t, err, "canvas API returned status 401")
}

func TestCanvasImportCourses(t *testing.T) {
	db := newTestDB(t)
	fc := newFakeCanvas(t)
	fc.courses = []map[string]any{
		{"id": float64(101), "name": "Algebra", "course_code": "ALG-1", "is_public": true, "image_download_url": "https://img/alg.png", "start_at": "2026-01-05T00:00:00Z", "end_at": "2026-05-01T00:00:00Z"},
		{"id": float64(102), "name": "Biology", "course_code": "", "start_at": nil},
		{"id": float64(103), "name": "Already here", "course_code": "OLD"},
	}
	fc.assignments = map[string]int{"101": 4, "102": 0}
	p := createCanvasProvider(t, db, fc)
	require.NoError(t, db.Create(&Course{ProviderPlatformID: p.ID, ExternalID: "103", Name: "Kept"}).Error)

	require.NoError(t, newCanvasService(p, nil).ImportCourses(&store{db: db}))

	var courses []Course
	require.NoError(t, db.Order("external_id").Find(&courses).Error)
	require.Len(t, courses, 3)
	alg, bio := courses[0], courses[1]
	assert.Equal(t, "Algebra", alg.Name)
	assert.Equal(t, stringPtr("ALG-1"), alg.AltName)
	assert.Equal(t, stringPtr("ALG-1"), alg.Description)
	assert.Equal(t, stringPtr(fc.URL+"/courses/101"), alg.ExternalURL)
	assert.Equal(t, CourseType("open_enrollment"), alg.Type)
	assert.Equal(t, stringPtr("grade, college_credit"), alg.OutcomeTypes)
	assert.Equal(t, stringPtr("https://img/alg.png"), alg.ThumbnailURL)
	assert.Equal(t, uint(4), alg.TotalProgressMilestones)
	assert.Equal(t, "2026-01-05", alg.StartDt.Format(time.DateOnly))
	assert.Equal(t, "2026-05-01", alg.EndDt.Format(time.DateOnly))

	assert.Equal(t, "Biology", bio.Name)
	assert.Nil(t, bio.AltName)
	assert.Nil(t, bio.Description)
	assert.Nil(t, bio.ThumbnailURL)
	assert.Equal(t, CourseType("fixed_enrollment"), bio.Type)
	assert.Nil(t, bio.StartDt)
	assert.Equal(t, "Kept", courses[2].Name)
}

func TestCanvasImportMilestones(t *testing.T) {
	db := newTestDB(t)
	fc := newFakeCanvas(t)
	lastRun := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	fc.submissions = []map[string]any{
		// Graded and submitted after the last run: a submission and a grade.
		{"id": float64(1), "user_id": float64(21), "workflow_state": "graded", "grade": "A", "anonymous_id": "anon1", "submitted_at": "2026-03-02T00:00:00Z"},
		// Submitted before the last run but graded after: a grade too.
		{"id": float64(2), "user_id": float64(21), "workflow_state": "graded", "grade": "B", "anonymous_id": "anon2", "submitted_at": "2026-02-01T00:00:00Z", "graded_at": "2026-03-05T00:00:00Z"},
		// Not graded and no grade: only the submission, incomplete.
		{"id": float64(3), "user_id": float64(22), "workflow_state": "submitted", "submitted_at": "2026-03-02T00:00:00Z"},
		// Graded before the last run: no grade milestone.
		{"id": float64(4), "user_id": float64(22), "workflow_state": "complete", "grade": "C", "anonymous_id": "anon4", "submitted_at": "2026-01-01T00:00:00Z", "graded_at": "2026-02-01T00:00:00Z"},
		// No mapping for this user: skipped.
		{"id": float64(5), "user_id": float64(99), "workflow_state": "graded", "grade": "A", "anonymous_id": "anon5", "submitted_at": "2026-03-02T00:00:00Z"},
	}
	p := createCanvasProvider(t, db, fc)
	course := Course{ProviderPlatformID: p.ID, ExternalID: "101"}
	require.NoError(t, db.Create(&course).Error)
	users := []map[string]any{
		{"user_id": uint(1), "external_user_id": "21"},
		{"user_id": int64(2), "external_user_id": "22"},
		{"user_id": "bad", "external_user_id": "23"},
	}
	pair := map[string]any{"course_id": int64(course.ID), "external_course_id": "101"}

	require.NoError(t, newCanvasService(p, nil).ImportMilestones(pair, users, &store{db: db}, lastRun))

	type row struct {
		UserID      uint
		ExternalID  string
		Type        string
		IsCompleted bool
	}
	var got []row
	require.NoError(t, db.Model(&Milestone{}).Order("id").Find(&got).Error)
	assert.Equal(t, []row{
		{1, "1", "assignment_submission", true},
		{1, "anon1", "grade_received", true},
		{1, "2", "assignment_submission", true},
		{1, "anon2", "grade_received", true},
		{2, "3", "assignment_submission", false},
		{2, "4", "assignment_submission", true},
	}, got)
	assert.Contains(t, fc.Requests(), "GET /api/v1/courses/101/students/submissions?per_page=100&submitted_since=2026-03-01T00%3A00%3A00Z&user_ids%5B%5D=21&user_ids%5B%5D=22 auth=Bearer token-123")
}

func TestCanvasImportMilestonesBadCourseID(t *testing.T) {
	srv := &CanvasService{}
	err := srv.ImportMilestones(map[string]any{"course_id": "1", "external_course_id": "101"}, nil, nil, time.Time{})
	assert.EqualError(t, err, "unexpected type string for course_id in course mapping")
}

func TestCheckTimespanOfSubmission(t *testing.T) {
	lastRun := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		submission map[string]any
		want       bool
	}{
		{"not submitted", map[string]any{"submitted_at": nil}, false},
		{"bad submitted date", map[string]any{"submitted_at": "yesterday"}, false},
		{"submitted after", map[string]any{"submitted_at": "2026-03-01T00:00:01Z"}, true},
		{"submitted at the last run", map[string]any{"submitted_at": "2026-03-01T00:00:00Z"}, false},
		{"before, never graded", map[string]any{"submitted_at": "2026-02-01T00:00:00Z", "graded_at": nil}, false},
		{"before, bad graded date", map[string]any{"submitted_at": "2026-02-01T00:00:00Z", "graded_at": "soon"}, false},
		{"before, graded after", map[string]any{"submitted_at": "2026-02-01T00:00:00Z", "graded_at": "2026-03-02T00:00:00Z"}, true},
		{"before, graded before", map[string]any{"submitted_at": "2026-02-01T00:00:00Z", "graded_at": "2026-02-02T00:00:00Z"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, checkTimespanOfSubmission(lastRun, tt.submission))
		})
	}
}

// The activity import adds missing enrollments, then records each mapped
// student's total time through a Postgres procedure.
func TestCanvasImportActivity(t *testing.T) {
	db := newTestDB(t)
	fc := newFakeCanvas(t)
	fc.enrollments = []map[string]any{
		{"user_id": float64(21), "total_activity_time": float64(600)},
		{"user_id": float64(22), "total_activity_time": float64(60)},
		{"user_id": float64(99), "total_activity_time": float64(5)},
	}
	p := createCanvasProvider(t, db, fc)
	course := Course{ProviderPlatformID: p.ID, ExternalID: "101"}
	require.NoError(t, db.Create(&course).Error)
	createResident(t, db, p.ID, 1, "student", "21")
	createResident(t, db, p.ID, 2, "student", "22")
	// Another provider's student with the same external id isn't this one.
	other := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "other"})
	createResident(t, db, other.ID, 3, "student", "99")
	require.NoError(t, db.Create(&UserEnrollment{UserID: 2, CourseID: course.ID}).Error)
	calls := recordCalls(t, db)

	pair := map[string]any{"course_id": int64(course.ID), "external_course_id": "101"}
	require.NoError(t, newCanvasService(p, nil).ImportActivityForCourse(pair, &store{db: db}))

	var enrolled []uint
	require.NoError(t, db.Model(&UserEnrollment{}).Order("user_id").Pluck("user_id", &enrolled).Error)
	assert.Equal(t, []uint{1, 2}, enrolled)
	assert.Equal(t, []procedureCall{
		{"insert_daily_activity_canvas", []any{uint(1), course.ID, ActivityType("course_interaction"), uint(600), "101"}},
		{"insert_daily_activity_canvas", []any{uint(2), course.ID, ActivityType("course_interaction"), uint(60), "101"}},
	}, *calls)
	assert.Contains(t, fc.Requests(), "GET /api/v1/courses/101/enrollments?state[]=active&state[]=invited&type[]=StudentEnrollment auth=Bearer token-123")
}
