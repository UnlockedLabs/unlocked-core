package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/stretchr/testify/require"
)

// These tests pin what every Programs route answers (EN-194): the status and
// the JSON, and who may call it. They also cover the core routes that read or
// change Programs data. Like the Open Content and Provider Platforms tests,
// they seed with SQL and read raw JSON, and must pass unchanged after the move.
//
// Some routes run SQL that only Postgres understands, so on SQLite they answer
// 500. Those cases still pin the route's access and error shape; make dev
// covers the real answer.
//
// Regenerate the golden files after an intended change with:
//
//	go test ./tests/integration -run 'TestPrograms' -update

// The live Canvas and Essential Education providers, and the IDs the Programs
// routes give their programs and classes.
const (
	canvasProviderID  = 10
	essentialEdID     = 11
	canvasProgramID   = models.CanvasProgramIDOffset + canvasProviderID
	canvasCourseID    = 101
	canvasClassID     = models.CanvasClassIDOffset + canvasProviderID*1_000_000 + canvasCourseID
	canvasStudentID   = "9001"
	essentialEdUserID = "8001"
)

// newFakeLMS stands in for Canvas (under /canvas) and Essential Education
// (under /ee). It serves TLS because the live provider code refuses plain
// HTTP. Background cache refreshes may still call it after a test ends, so it
// answers anything it doesn't know with 404 instead of failing the test.
func newFakeLMS(t *testing.T) *httptest.Server {
	t.Helper()
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	course := map[string]any{
		"id": canvasCourseID, "name": "Intro to Writing", "course_code": "ENG101",
		"start_at": "2026-01-05T00:00:00Z", "end_at": "2099-12-31T00:00:00Z", "time_zone": "America/Chicago",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /canvas/api/v1/accounts/1/courses", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{course})
	})
	mux.HandleFunc("GET /canvas/api/v1/courses/101", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, course)
	})
	mux.HandleFunc("GET /canvas/api/v1/courses/101/enrollments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{map[string]any{
			"user": map[string]any{"id": 9001}, "created_at": "2026-01-06T00:00:00Z", "enrollment_state": "active",
		}})
	})
	mux.HandleFunc("GET /canvas/api/v1/courses/101/analytics/student_summaries", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{map[string]any{
			"id": 9001, "page_views": 10, "participations": 0,
			"tardiness_breakdown": map[string]any{"total": 4, "missing": 2, "on_time": 2, "late": 0},
		}})
	})
	mux.HandleFunc("GET /canvas/api/v1/calendar_events", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{map[string]any{
			"id": 7001, "title": "Week 1", "workflow_state": "active",
			"start_at": "2026-02-02T15:00:00Z", "end_at": "2026-02-02T16:00:00Z",
		}})
	})
	mux.HandleFunc("GET /canvas/api/v1/users/{id}/courses", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{course})
	})
	mux.HandleFunc("GET /ee/classes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"at_end": true, "records": []any{map[string]any{
			"id": 201, "name": "GED Math", "open_date": "2026-01-05", "close_date": "2099-12-31", "active": 1,
			"students": []any{map[string]any{"id": 8001}}, "schedule_mon": "9:00am-10:00am",
		}}})
	})
	mux.HandleFunc("GET /ee/users", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"at_end": true, "records": []any{map[string]any{
			"id": 8001, "first_name": "John", "last_name": "Doe", "username": "ee.jdoe",
		}}})
	})
	mux.HandleFunc("/", http.NotFound)
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type programsWorld struct {
	north, south *models.Facility
	// jdoe and mmajor are in the North morning cohort; mmajor completed it.
	// newbie isn't enrolled anywhere. leaver and bulkLeaver get deactivated.
	resident, mmajor, newbie, leaver, bulkLeaver, southResident *models.User
	facAdmin, southAdmin, deptAdmin, instructor                 *models.User
}

func (w programsWorld) claims(u *models.User) *handlers.Claims {
	return &handlers.Claims{UserID: u.ID, Username: u.Username, Role: u.Role, FacilityID: u.FacilityID, TimeZone: "America/Chicago"}
}

// seedPrograms adds four programs, their classes and cohorts, rooms, events,
// enrollments, attendance and a completion, plus a Canvas and an Essential
// Education provider that point at the fake LMS.
//
//   - Program 1 "Adult Literacy" is offered at North and South. Its North
//     cohort 1 is Active with weekly Monday sessions in early 2026; South has
//     cohort 3.
//   - Program 2 "Welding" is offered at North only. Cohort 2 is Scheduled for
//     2099.
//   - Program 3 is inactive and archived. Program 4 is offered at South only.
func seedPrograms(t *testing.T, env *TestEnv, lms *httptest.Server) programsWorld {
	t.Helper()
	t.Setenv("APP_KEY", "test-app-key")
	env.Server.Client = lms.Client()

	var w programsWorld
	var err error
	w.north, err = env.CreateTestFacility("North")
	require.NoError(t, err)
	w.south, err = env.CreateTestFacility("South")
	require.NoError(t, err)
	for _, u := range []struct {
		user     **models.User
		username string
		role     models.UserRole
		facility uint
		docID    string
	}{
		{&w.resident, "jdoe", models.Student, w.north.ID, "1001"},
		{&w.mmajor, "mmajor", models.Student, w.north.ID, "1002"},
		{&w.newbie, "newbie", models.Student, w.north.ID, "1003"},
		{&w.leaver, "leaver", models.Student, w.north.ID, "1004"},
		{&w.bulkLeaver, "bulkleaver", models.Student, w.north.ID, "1005"},
		{&w.southResident, "southres", models.Student, w.south.ID, "2001"},
		{&w.facAdmin, "facadmin", models.FacilityAdmin, w.north.ID, ""},
		{&w.southAdmin, "southadmin", models.FacilityAdmin, w.south.ID, ""},
		{&w.deptAdmin, "deptadmin", models.DepartmentAdmin, w.north.ID, ""},
		{&w.instructor, "instructornorth", models.FacilityAdmin, w.north.ID, ""},
	} {
		*u.user, err = env.CreateTestUser(u.username, u.role, u.facility, u.docID)
		require.NoError(t, err)
	}

	day := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	date := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		require.NoError(t, err)
		return d
	}
	const providerKey = "MDEyMzQ1Njc4OWFiY2RlZsFxCXqsaoGNjd91872zRlhh9w==" // "admin:kolibri-pass" under APP_KEY test-app-key
	north, south := w.north.ID, w.south.ID
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE users SET name_first = 'John', name_last = 'Doe' WHERE id = ?`, []any{w.resident.ID}},
		{`UPDATE users SET name_first = 'Mary', name_last = 'Major' WHERE id = ?`, []any{w.mmajor.ID}},
		{`UPDATE users SET name_first = 'Ivy', name_last = 'Instructor' WHERE id = ?`, []any{w.instructor.ID}},
		{`INSERT INTO programs (id, name, description, funding_type, is_active, archived_at, has_program_completion, create_user_id, created_at, updated_at) VALUES
			(1, 'Adult Literacy', 'Reading and writing', 'Federal_Grants', true, NULL, true, ?, ?, ?),
			(2, 'Welding', 'Shop safety and welding', 'State_Grants', true, NULL, false, ?, ?, ?),
			(3, 'Old Program', 'Retired', 'Other', false, ?, false, NULL, ?, ?),
			(4, 'South Only', 'Offered at South', 'Other', true, NULL, false, NULL, ?, ?)`,
			[]any{w.deptAdmin.ID, day, day, w.deptAdmin.ID, day, day, day, day, day, day, day}},
		{`INSERT INTO program_types (program_type, program_id) VALUES ('Educational', 1), ('Vocational', 2), ('Life_Skills', 4)`, nil},
		{`INSERT INTO program_credit_types (credit_type, program_id) VALUES ('Completion', 1), ('Earned-time', 2), ('Participation', 4)`, nil},
		{`INSERT INTO facilities_programs (id, program_id, facility_id, created_at, updated_at) VALUES
			(1, 1, ?, ?, ?), (2, 1, ?, ?, ?), (3, 2, ?, ?, ?), (4, 3, ?, ?, ?), (5, 4, ?, ?, ?)`,
			[]any{north, day, day, south, day, day, north, day, day, north, day, day, south, day, day}},
		{`INSERT INTO rooms (id, facility_id, name, created_at, updated_at) VALUES
			(1, ?, 'Room A', ?, ?), (2, ?, 'Room B', ?, ?), (3, ?, 'South Hall', ?, ?)`,
			[]any{north, day, day, north, day, day, south, day, day}},
		{`INSERT INTO program_classes (id, program_id, facility_id, name, description, credit_hours, created_at, updated_at) VALUES
			(1, 1, ?, 'Reading 101', 'Morning reading', 2, ?, ?),
			(2, 2, ?, 'Welding I', NULL, 3, ?, ?),
			(3, 1, ?, 'Reading South', NULL, 2, ?, ?)`,
			[]any{north, day, day, north, day, day, south, day, day}},
		{`INSERT INTO program_class_credit_types (class_id, credit_type) VALUES (1, 'Completion')`, nil},
		{`INSERT INTO program_class_cohorts (id, class_id, program_id, facility_id, capacity, description, start_dt, end_dt, status, credit_hours, create_user_id, created_at, updated_at) VALUES
			(1, 1, 1, ?, 10, 'Morning cohort', ?, ?, 'Active', 2, ?, ?, ?),
			(2, 2, 2, ?, 5, 'Evening cohort', ?, ?, 'Scheduled', 3, ?, ?, ?),
			(3, 3, 1, ?, 8, 'South cohort', ?, ?, 'Active', 2, NULL, ?, ?)`,
			[]any{
				north, date("2026-01-05"), date("2099-12-31"), w.facAdmin.ID, day, day,
				north, date("2099-01-04"), date("2099-06-30"), w.facAdmin.ID, day, day,
				south, date("2026-01-06"), date("2099-12-31"), day, day,
			}},
		{`INSERT INTO program_class_enrollments (id, cohort_id, class_id, user_id, enrollment_status, enrolled_at, enrollment_ended_at, created_at, updated_at) VALUES
			(1, 1, 1, ?, 'Enrolled', ?, NULL, ?, ?),
			(2, 1, 1, ?, 'Completed', ?, ?, ?, ?),
			(3, 1, 1, ?, 'Enrolled', ?, NULL, ?, ?),
			(4, 1, 1, ?, 'Enrolled', ?, NULL, ?, ?),
			(5, 3, 3, ?, 'Enrolled', ?, NULL, ?, ?)`,
			[]any{
				w.resident.ID, date("2026-01-05"), day, day,
				w.mmajor.ID, date("2026-01-05"), date("2026-03-01"), day, day,
				w.leaver.ID, date("2026-01-05"), day, day,
				w.bulkLeaver.ID, date("2026-01-05"), day, day,
				w.southResident.ID, date("2026-01-06"), day, day,
			}},
		{`INSERT INTO class_completions (user_id, cohort_id, class_id, facility_name, credit_type, admin_email, program_owner, program_name, program_id, class_name, cohort_start_dt, enrolled_on_dt, created_at, updated_at)
			VALUES (?, 1, 1, 'North', 'Completion', 'facadmin@example.com', '', 'Adult Literacy', 1, 'Reading 101', ?, ?, ?, ?)`,
			[]any{w.mmajor.ID, date("2026-01-05"), date("2026-01-05"), day, day}},
		{`INSERT INTO program_class_events (id, cohort_id, duration, recurrence_rule, room_id, instructor_id, is_cancelled, created_at, updated_at) VALUES
			(1, 1, '1h', ?, 1, ?, false, ?, ?),
			(2, 2, '1h', ?, 2, ?, false, ?, ?),
			(3, 3, '1h', ?, 3, ?, false, ?, ?)`,
			[]any{
				"DTSTART;TZID=America/Chicago:20260105T090000\nRRULE:FREQ=WEEKLY;BYDAY=MO;COUNT=8", w.instructor.ID, day, day,
				"DTSTART;TZID=America/Chicago:20990105T090000\nRRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4", w.instructor.ID, day, day,
				"DTSTART;TZID=America/Chicago:20260106T090000\nRRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=8", w.southAdmin.ID, day, day,
			}},
		{`INSERT INTO program_class_event_overrides (id, event_id, duration, override_rrule, is_cancelled, reason, created_at, updated_at)
			VALUES (1, 1, '1h', ?, true, 'Holiday', ?, ?)`,
			[]any{"DTSTART;TZID=America/Chicago:20260112T090000\nRRULE:FREQ=DAILY;COUNT=1", day, day}},
		{`INSERT INTO program_class_event_attendance (event_id, user_id, date, attendance_status, note, create_user_id, created_at, updated_at) VALUES
			(1, ?, '2026-01-05', 'present', NULL, ?, ?, ?),
			(1, ?, '2026-01-05', 'present', NULL, ?, ?, ?),
			(1, ?, '2026-01-19', 'absent_unexcused', 'Sick', ?, ?, ?)`,
			[]any{w.resident.ID, w.facAdmin.ID, day, day, w.mmajor.ID, w.facAdmin.ID, day, day, w.resident.ID, w.facAdmin.ID, day, day}},
		{`INSERT INTO provider_platforms (id, type, name, account_id, access_key, base_url, state, external_auth_provider_id, created_at, updated_at) VALUES
			(?, 'canvas_cloud', 'Canvas College', '1', ?, ?, 'enabled', '', ?, ?),
			(?, 'essential_ed', 'Essential Ed', 'district', ?, ?, 'enabled', '', ?, ?)`,
			[]any{canvasProviderID, providerKey, lms.URL + "/canvas", day, day, essentialEdID, providerKey, lms.URL + "/ee", day, day}},
		{`INSERT INTO provider_user_mappings (user_id, provider_platform_id, external_user_id, external_username, authentication_provider_status, created_at, updated_at) VALUES
			(?, ?, ?, 'jdoe', 'none', ?, ?), (?, ?, ?, 'ee.jdoe', 'none', ?, ?)`,
			[]any{w.resident.ID, canvasProviderID, canvasStudentID, day, day, w.resident.ID, essentialEdID, essentialEdUserID, day, day}},
	} {
		require.NoError(t, env.DB.Exec(stmt.sql, stmt.args...).Error, stmt.sql)
	}
	return w
}

func TestProgramsRoutes(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	lms := newFakeLMS(t)
	w := seedPrograms(t, env, lms)
	client := noRedirects(env)

	resident, facAdmin, deptAdmin := w.claims(w.resident), w.claims(w.facAdmin), w.claims(w.deptAdmin)
	user := func(format string, u *models.User) string { return fmt.Sprintf(format, u.ID) }
	january := "start_dt=2026-01-01&end_dt=2026-01-31"

	// The cases run in order against one database, so later reads show the
	// earlier writes. sorted marks routes whose row order isn't fixed: their
	// arrays are compared in sorted order.
	tests := []struct {
		name   string
		claims *handlers.Claims
		method string
		path   string
		body   any
		status int
		sorted bool
	}{
		// Programs
		{"programs", resident, http.MethodGet, "/api/programs", nil, http.StatusOK, false},
		{"programs_search", facAdmin, http.MethodGet, "/api/programs?search=weld", nil, http.StatusOK, false},
		{"programs_paged", facAdmin, http.MethodGet, "/api/programs?page=2&per_page=2&order_by=name", nil, http.StatusOK, false},
		{"program_filters", resident, http.MethodGet, "/api/programs/filters", nil, http.StatusOK, false},
		{"program_by_id", facAdmin, http.MethodGet, "/api/programs/1", nil, http.StatusOK, false},
		{"program_by_id_bad_id", facAdmin, http.MethodGet, "/api/programs/abc", nil, http.StatusBadRequest, false},
		{"program_by_id_missing", facAdmin, http.MethodGet, "/api/programs/999", nil, http.StatusBadRequest, false},
		{"program_canvas", facAdmin, http.MethodGet, fmt.Sprintf("/api/programs/%d", canvasProgramID), nil, http.StatusOK, false},
		{"programs_detailed_list", facAdmin, http.MethodGet, "/api/programs/detailed-list", nil, http.StatusInternalServerError, false},
		{"programs_stats_facility", facAdmin, http.MethodGet, "/api/programs/stats?days=all", nil, http.StatusOK, false},
		{"programs_stats_all", deptAdmin, http.MethodGet, "/api/programs/stats?days=all", nil, http.StatusOK, false},
		{"program_archive_check", facAdmin, http.MethodGet, "/api/programs/1/archive-check", nil, http.StatusOK, false},
		{"program_delete_check", facAdmin, http.MethodGet, "/api/programs/1/delete-check", nil, http.StatusOK, false},
		{"programs_csv", facAdmin, http.MethodGet, "/api/programs/csv", nil, http.StatusInternalServerError, false},
		{"program_create", facAdmin, http.MethodPost, "/api/programs",
			map[string]any{
				"name": "Life Skills", "description": "Everyday skills", "funding_type": "Other", "is_active": true,
				"facilities": []uint{w.north.ID}, "program_types": []map[string]any{{"program_type": "Life_Skills"}},
				"credit_types": []map[string]any{{"credit_type": "Participation"}},
			}, http.StatusCreated, false},
		{"program_create_bad_body", facAdmin, http.MethodPost, "/api/programs", "not an object", http.StatusBadRequest, false},
		{"program_create_missing_name", facAdmin, http.MethodPost, "/api/programs",
			map[string]any{"description": "d", "funding_type": "Other", "facilities": []uint{w.north.ID}}, http.StatusBadRequest, false},
		{"program_create_duplicate", facAdmin, http.MethodPost, "/api/programs",
			map[string]any{"name": "Welding", "description": "d", "funding_type": "Other", "facilities": []uint{w.north.ID}}, http.StatusInternalServerError, false},
		{"program_update", facAdmin, http.MethodPatch, "/api/programs/5",
			map[string]any{
				"name": "Life Skills", "description": "Everyday skills for re-entry", "funding_type": "Other", "is_active": true,
				"facilities": []uint{w.north.ID, w.south.ID}, "program_types": []map[string]any{{"program_type": "Re-Entry"}},
				"credit_types": []map[string]any{{"credit_type": "Participation"}},
			}, http.StatusOK, false},
		{"program_update_missing", facAdmin, http.MethodPatch, "/api/programs/999", map[string]any{"name": "x", "facilities": []uint{w.north.ID}}, http.StatusBadRequest, false},
		{"program_status_archive_blocked", facAdmin, http.MethodPatch, "/api/programs/1/status", map[string]any{"archived_at": "2026-02-01T00:00:00Z"}, http.StatusOK, false},
		{"program_status_inactive", facAdmin, http.MethodPatch, "/api/programs/5/status", map[string]any{"is_active": false}, http.StatusOK, false},
		{"program_status_missing", facAdmin, http.MethodPatch, "/api/programs/999/status", map[string]any{"is_active": false}, http.StatusBadRequest, false},
		{"program_history", facAdmin, http.MethodGet, "/api/programs/5/history", nil, http.StatusOK, false},
		{"program_history_status", facAdmin, http.MethodGet, "/api/programs/5/history?categories=status", nil, http.StatusOK, false},
		{"program_delete_blocked", facAdmin, http.MethodDelete, "/api/programs/1", nil, http.StatusConflict, false},
		{"program_delete", facAdmin, http.MethodDelete, "/api/programs/5", nil, http.StatusNoContent, false},
		{"programs_after_changes", facAdmin, http.MethodGet, "/api/programs?per_page=50", nil, http.StatusOK, false},

		// Classes (the tier above cohorts)
		{"classes", facAdmin, http.MethodGet, "/api/classes", nil, http.StatusOK, false},
		{"classes_for_program", facAdmin, http.MethodGet, "/api/classes?program_id=1", nil, http.StatusOK, false},
		{"classes_bad_program", facAdmin, http.MethodGet, "/api/classes?program_id=abc", nil, http.StatusBadRequest, false},
		{"class_by_id", resident, http.MethodGet, "/api/classes/1", nil, http.StatusOK, false},
		{"class_by_id_missing", deptAdmin, http.MethodGet, "/api/classes/999", nil, http.StatusBadRequest, false},
		{"class_create", facAdmin, http.MethodPost, "/api/classes",
			map[string]any{"program_id": 2, "name": "Welding II", "description": "Advanced", "credit_hours": 3, "credit_types": []string{"Earned-time"}},
			http.StatusCreated, false},
		{"class_create_no_program", facAdmin, http.MethodPost, "/api/classes", map[string]any{"name": "Nothing"}, http.StatusBadRequest, false},
		{"class_update", facAdmin, http.MethodPatch, "/api/classes/4", map[string]any{"description": "Advanced welding", "credit_types": []string{}}, http.StatusOK, false},
		{"class_update_cohort_field", facAdmin, http.MethodPatch, "/api/classes/4", map[string]any{"status": "Active"}, http.StatusBadRequest, false},

		// Cohorts
		{"program_classes", facAdmin, http.MethodGet, "/api/program-classes", nil, http.StatusOK, false},
		{"program_classes_all_facilities", deptAdmin, http.MethodGet, "/api/program-classes?facility=all&search=reading", nil, http.StatusOK, false},
		{"program_cohorts", facAdmin, http.MethodGet, "/api/programs/1/classes", nil, http.StatusInternalServerError, false},
		{"program_cohorts_none", facAdmin, http.MethodGet, "/api/programs/4/classes", nil, http.StatusOK, false},
		{"program_cohorts_canvas", facAdmin, http.MethodGet, fmt.Sprintf("/api/programs/%d/classes", canvasProgramID), nil, http.StatusOK, false},
		{"cohort_by_id", resident, http.MethodGet, "/api/program-classes/1", nil, http.StatusOK, false},
		{"cohort_by_id_missing", deptAdmin, http.MethodGet, "/api/program-classes/999", nil, http.StatusBadRequest, false},
		{"cohort_canvas", facAdmin, http.MethodGet, fmt.Sprintf("/api/program-classes/%d", canvasClassID), nil, http.StatusOK, false},
		{"cohort_create", facAdmin, http.MethodPost, "/api/programs/2/classes",
			map[string]any{
				"program_class_id": 2, "description": "Weekend cohort", "status": "Scheduled", "capacity": 4,
				"start_dt": "2099-02-01T00:00:00Z", "end_dt": "2099-03-01T00:00:00Z", "credit_hours": 3, "instructor_id": w.instructor.ID,
				"events": []map[string]any{{"recurrence_rule": "DTSTART;TZID=America/Chicago:20990206T090000\nRRULE:FREQ=WEEKLY;BYDAY=SA;COUNT=3", "duration": "1h", "room_id": 2}},
			}, http.StatusCreated, false},
		{"cohort_create_no_instructor", facAdmin, http.MethodPost, "/api/programs/2/classes",
			map[string]any{"description": "d", "status": "Scheduled", "capacity": 4, "start_dt": "2099-02-01T00:00:00Z"}, http.StatusBadRequest, false},
		{"cohort_create_room_conflict", facAdmin, http.MethodPost, "/api/programs/2/classes",
			map[string]any{
				"description": "Clash", "status": "Scheduled", "capacity": 4, "start_dt": "2099-02-01T00:00:00Z", "instructor_id": w.instructor.ID,
				"events": []map[string]any{{"recurrence_rule": "DTSTART;TZID=America/Chicago:20990206T090000\nRRULE:FREQ=WEEKLY;BYDAY=SA;COUNT=1", "duration": "1h", "room_id": 2}},
			}, http.StatusConflict, false},
		{"cohort_update", facAdmin, http.MethodPatch, "/api/programs/2/classes/4", map[string]any{"description": "Weekend cohort, small", "capacity": 3}, http.StatusOK, false},
		{"cohort_update_terminal", facAdmin, http.MethodPatch, "/api/programs/1/classes/1", map[string]any{"capacity": 1}, http.StatusBadRequest, false},
		{"cohorts_update", facAdmin, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", 4), map[string]any{"capacity": 6}, http.StatusOK, false},
		{"cohort_history", facAdmin, http.MethodGet, "/api/program-classes/4/history", nil, http.StatusOK, false},
		{"cohort_history_schedule", facAdmin, http.MethodGet, "/api/program-classes/1/history?categories=schedule", nil, http.StatusOK, false},
		{"cohort_attendance_flags", facAdmin, http.MethodGet, "/api/program-classes/1/attendance-flags", nil, http.StatusOK, false},
		{"cohort_attendance_rate", facAdmin, http.MethodGet, "/api/program-classes/1/attendance-rate", nil, http.StatusInternalServerError, false},
		{"cohort_attendance_rate_canvas", facAdmin, http.MethodGet, fmt.Sprintf("/api/program-classes/%d/attendance-rate", canvasClassID), nil, http.StatusOK, false},
		{"cohort_missing_attendance", facAdmin, http.MethodGet, "/api/program-classes/1/missing-attendance", nil, http.StatusInternalServerError, false},
		{"missing_attendance", facAdmin, http.MethodGet, "/api/program-classes/missing-attendance", nil, http.StatusOK, false},
		{"cohort_outcomes", facAdmin, http.MethodGet, "/api/programs/1/classes/outcomes", nil, http.StatusInternalServerError, false},
		{"instructor_classes", facAdmin, http.MethodGet, user("/api/instructors/%d/classes?start_date=2026-01-01&end_date=2026-01-31", w.instructor), nil, http.StatusOK, false},
		{"instructor_classes_no_dates", facAdmin, http.MethodGet, user("/api/instructors/%d/classes", w.instructor), nil, http.StatusBadRequest, false},
		{"bulk_cancel_nothing", facAdmin, http.MethodPost, "/api/program-classes/bulk-cancel",
			map[string]any{"instructorId": w.instructor.ID, "startDate": "2030-01-01", "endDate": "2030-01-31", "reason": "Instructor on leave"}, http.StatusOK, false},
		{"bulk_cancel_short_reason", facAdmin, http.MethodPost, "/api/program-classes/bulk-cancel",
			map[string]any{"instructorId": w.instructor.ID, "startDate": "2030-01-01", "endDate": "2030-01-31", "reason": "short"}, http.StatusBadRequest, false},
		{"cohort_delete_check", facAdmin, http.MethodGet, "/api/program-classes/4/delete-check", nil, http.StatusOK, false},
		{"cohort_delete_check_blocked", facAdmin, http.MethodGet, "/api/program-classes/1/delete-check", nil, http.StatusOK, false},
		{"cohort_delete_blocked", facAdmin, http.MethodDelete, "/api/program-classes/1", nil, http.StatusConflict, false},
		{"cohort_delete_canvas", facAdmin, http.MethodDelete, fmt.Sprintf("/api/program-classes/%d", canvasClassID), nil, http.StatusBadRequest, false},

		// Events and calendars
		{"cohort_events", facAdmin, http.MethodGet, "/api/program-classes/1/events?month=1&year=2026", nil, http.StatusOK, false},
		{"cohort_events_canvas", facAdmin, http.MethodGet, fmt.Sprintf("/api/program-classes/%d/events", canvasClassID), nil, http.StatusOK, false},
		{"student_calendar", resident, http.MethodGet, "/api/student-calendar?" + january, nil, http.StatusOK, false},
		{"student_calendar_bad_range", resident, http.MethodGet, "/api/student-calendar?start_dt=2026-02-01&end_dt=2026-01-01", nil, http.StatusBadRequest, false},
		{"admin_calendar", facAdmin, http.MethodGet, "/api/admin-calendar?" + january, nil, http.StatusOK, false},
		{"admin_calendar_cohort", facAdmin, http.MethodGet, "/api/admin-calendar?cohort_id=1&" + january, nil, http.StatusOK, false},
		{"admin_calendar_bad_cohort", facAdmin, http.MethodGet, "/api/admin-calendar?cohort_id=abc&" + january, nil, http.StatusBadRequest, false},
		{"todays_schedule", facAdmin, http.MethodGet, "/api/program-classes/todays-schedule", nil, http.StatusOK, false},
		{"canvas_schedule", facAdmin, http.MethodGet, fmt.Sprintf("/api/program-classes/%d/canvas-schedule?start_date=2026-02-01&end_date=2026-02-28", canvasClassID), nil, http.StatusOK, false},
		{"canvas_schedule_not_canvas", facAdmin, http.MethodGet, "/api/program-classes/1/canvas-schedule", nil, http.StatusBadRequest, false},
		{"event_create", facAdmin, http.MethodPost, "/api/program-classes/2/events",
			map[string]any{"duration": "1h", "room_id": 1, "instructor_id": w.instructor.ID,
				"recurrence_rule": "DTSTART;TZID=America/Chicago:20990107T120000\nRRULE:FREQ=WEEKLY;BYDAY=TH;UNTIL=20990228T000000Z"}, http.StatusCreated, false},
		{"event_create_no_rule", facAdmin, http.MethodPost, "/api/program-classes/2/events", map[string]any{"room_id": 1}, http.StatusInternalServerError, false},
		{"event_series_reschedule", facAdmin, http.MethodPut, "/api/program-classes/2/events",
			map[string]any{
				"event_series": map[string]any{"id": 2, "duration": "1h", "room_id": 2, "instructor_id": w.instructor.ID,
					"recurrence_rule": "DTSTART;TZID=America/Chicago:20990302T090000\nRRULE:FREQ=WEEKLY;BYDAY=TU;UNTIL=20990330T000000Z"},
				"closed_event_series": map[string]any{"id": 2, "duration": "1h", "room_id": 2, "instructor_id": w.instructor.ID,
					"recurrence_rule": "DTSTART;TZID=America/Chicago:20990105T090000\nRRULE:FREQ=WEEKLY;BYDAY=TU;UNTIL=20990127T000000Z"},
			}, http.StatusCreated, false},
		{"event_overrides", facAdmin, http.MethodPut, "/api/program-classes/1/events/1",
			[]map[string]any{{"override_rrule": "DTSTART;TZID=America/Chicago:20260202T090000\nRRULE:FREQ=DAILY;COUNT=1", "duration": "1h", "is_cancelled": true, "reason": "Staff training"}},
			http.StatusOK, false},
		{"event_override_patch", facAdmin, http.MethodPatch, "/api/program-classes/1/events/1",
			map[string]any{"date": "2026-02-09", "is_cancelled": true, "reason": "Lockdown"}, http.StatusOK, false},
		{"event_override_patch_no_date", facAdmin, http.MethodPatch, "/api/program-classes/1/events/1", map[string]any{"is_cancelled": true}, http.StatusBadRequest, false},
		{"event_override_uncancel", facAdmin, http.MethodPost, "/api/program-classes/1/events/3/uncancel", nil, http.StatusOK, false},
		{"event_override_delete", facAdmin, http.MethodDelete, "/api/program-classes/1/events/2", nil, http.StatusNoContent, false},
		{"event_uncancel_series", facAdmin, http.MethodPost, "/api/program-classes/1/events/1/uncancel-series", map[string]any{"restore_date": "2026-02-16"}, http.StatusOK, false},
		{"event_uncancel_series_no_date", facAdmin, http.MethodPost, "/api/program-classes/1/events/1/uncancel-series", map[string]any{}, http.StatusBadRequest, false},
		{"cohort_events_after_changes", facAdmin, http.MethodGet, "/api/program-classes/1/events?month=2&year=2026", nil, http.StatusOK, false},

		// Rooms and instructors
		{"rooms", facAdmin, http.MethodGet, "/api/rooms", nil, http.StatusOK, false},
		{"room_create", facAdmin, http.MethodPost, "/api/rooms", map[string]any{"name": "Room C"}, http.StatusCreated, false},
		{"room_create_no_name", facAdmin, http.MethodPost, "/api/rooms", map[string]any{"name": ""}, http.StatusBadRequest, false},
		{"facility_instructors", facAdmin, http.MethodGet, fmt.Sprintf("/api/facilities/%d/instructors", w.north.ID), nil, http.StatusOK, false},

		// Enrollments
		{"enrollments", facAdmin, http.MethodGet, "/api/program-classes/1/enrollments", nil, http.StatusOK, false},
		{"enrollments_by_status", facAdmin, http.MethodGet, "/api/program-classes/1/enrollments?status=Enrolled", nil, http.StatusInternalServerError, false},
		{"enrollment_conflicts", facAdmin, http.MethodPost, "/api/program-classes/2/enrollment-conflicts", map[string]any{"user_ids": []uint{w.resident.ID}}, http.StatusOK, false},
		{"enrollment_conflicts_none", facAdmin, http.MethodPost, "/api/program-classes/2/enrollment-conflicts", map[string]any{"user_ids": []uint{}}, http.StatusOK, false},
		{"users_only_unenrolled", facAdmin, http.MethodGet, "/api/users?include=only_unenrolled&cohort_id=1", nil, http.StatusOK, false},
		{"users_only_enrolled", facAdmin, http.MethodGet, "/api/users?include=only_enrolled&cohort_id=1", nil, http.StatusOK, false},
		{"users_include_bad_cohort", facAdmin, http.MethodGet, "/api/users?include=only_enrolled&cohort_id=abc", nil, http.StatusBadRequest, false},
		{"enroll", facAdmin, http.MethodPost, "/api/program-classes/2/enrollments", map[string]any{"user_ids": []uint{w.resident.ID, w.newbie.ID}, "confirm": true}, http.StatusCreated, false},
		{"enroll_missing_cohort", facAdmin, http.MethodPost, "/api/program-classes/999/enrollments", map[string]any{"user_ids": []uint{w.newbie.ID}, "confirm": true}, http.StatusUnauthorized, false},
		{"enrollments_update", facAdmin, http.MethodPatch, "/api/program-classes/2/enrollments",
			map[string]any{"enrollment_status": "Cancelled", "user_ids": []uint{w.newbie.ID}, "change_reason": "Schedule clash"}, http.StatusOK, false},
		{"enrollments_update_invalid", facAdmin, http.MethodPatch, "/api/program-classes/2/enrollments",
			map[string]any{"enrollment_status": "Completed", "user_ids": []uint{w.resident.ID}}, http.StatusBadRequest, false},
		{"enrollment_date", facAdmin, http.MethodPatch, "/api/program-classes/1/enrollments/1/date", map[string]any{"enrolled_date": "2026-01-06T00:00:00Z"}, http.StatusOK, false},
		{"enrollment_date_bad", facAdmin, http.MethodPatch, "/api/program-classes/1/enrollments/1/date", map[string]any{"enrolled_date": "Jan 6"}, http.StatusBadRequest, false},
		{"enrollments_after_changes", facAdmin, http.MethodGet, "/api/program-classes/2/enrollments?status=all", nil, http.StatusOK, false},
		{"enrollment_attendance", facAdmin, http.MethodGet, "/api/programs/1/classes/1/enrollments/1/attendance", nil, http.StatusOK, false},
		{"program_completions", facAdmin, http.MethodGet, user("/api/users/%d/program-completions", w.mmajor), nil, http.StatusOK, false},
		{"program_completions_self", w.claims(w.mmajor), http.MethodGet, user("/api/users/%d/program-completions?class_id=1", w.mmajor), nil, http.StatusOK, false},

		// Attendance
		{"event_attendance", facAdmin, http.MethodGet, "/api/program-classes/1/events/1/attendance?date=2026-01-05", nil, http.StatusInternalServerError, false},
		{"event_attendance_cancelled", facAdmin, http.MethodGet, "/api/program-classes/1/events/1/attendance?date=2026-01-12", nil, http.StatusConflict, false},
		{"event_attendance_rate", facAdmin, http.MethodGet, "/api/program-classes/1/events/1/attendance-rate", nil, http.StatusInternalServerError, false},
		{"historical_enrollment", facAdmin, http.MethodGet, "/api/program-classes/1/historical-enrollment-batch?dates=2026-01-05,2026-01-19", nil, http.StatusOK, false},
		{"historical_enrollment_no_dates", facAdmin, http.MethodGet, "/api/program-classes/1/historical-enrollment-batch", nil, http.StatusBadRequest, false},
		{"attendance_add", facAdmin, http.MethodPost, "/api/program-classes/1/events/1/attendance",
			[]map[string]any{
				{"user_id": w.resident.ID, "date": "2026-01-26", "attendance_status": "present"},
				{"user_id": w.mmajor.ID, "date": "2026-01-26", "attendance_status": "partial", "check_in_at": "09:00", "check_out_at": "09:30"},
			}, http.StatusOK, false},
		{"attendance_add_future", facAdmin, http.MethodPost, "/api/program-classes/1/events/1/attendance",
			[]map[string]any{{"user_id": w.resident.ID, "date": "2099-01-05", "attendance_status": "present"}}, http.StatusUnprocessableEntity, false},
		{"attendance_add_cancelled", facAdmin, http.MethodPost, "/api/program-classes/1/events/1/attendance",
			[]map[string]any{{"user_id": w.resident.ID, "date": "2026-01-12", "attendance_status": "present"}}, http.StatusConflict, false},
		{"attendance_delete", facAdmin, http.MethodDelete, user("/api/program-classes/1/events/1/attendance/%d?date=2026-01-26", w.resident), nil, http.StatusNoContent, false},
		{"attendance_delete_not_enrolled", facAdmin, http.MethodDelete, user("/api/program-classes/1/events/1/attendance/%d?date=2026-01-26", w.newbie), nil, http.StatusBadRequest, false},
		{"enrollment_attendance_after_changes", facAdmin, http.MethodGet, "/api/programs/1/classes/1/enrollments/2/attendance", nil, http.StatusOK, false},

		// Reports
		{"report_program_outcomes", facAdmin, http.MethodPost, "/api/reports/generate",
			map[string]any{"type": "program_outcomes", "format": "csv", "start_date": "2026-01-01T00:00:00Z", "end_date": "2026-06-30T00:00:00Z", "class_status": "All"}, http.StatusOK, false},
		{"report_class_roster", facAdmin, http.MethodPost, "/api/reports/generate",
			map[string]any{"type": "class_roster", "format": "csv", "start_date": "2026-01-01T00:00:00Z", "end_date": "2026-06-30T00:00:00Z", "cohort_id": 1}, http.StatusOK, false},
		{"report_resident_profile", facAdmin, http.MethodPost, "/api/reports/generate",
			map[string]any{"type": "resident_profile", "format": "csv", "start_date": "2026-01-01T00:00:00Z", "end_date": "2026-06-30T00:00:00Z", "user_id": w.mmajor.ID}, http.StatusOK, false},
		{"report_bad_type", facAdmin, http.MethodPost, "/api/reports/generate",
			map[string]any{"type": "nope", "format": "csv", "start_date": "2026-01-01T00:00:00Z", "end_date": "2026-06-30T00:00:00Z"}, http.StatusBadRequest, false},
		{"report_comparison_facility_admin", facAdmin, http.MethodPost, "/api/reports/generate",
			map[string]any{"type": "facility_comparison", "format": "csv", "start_date": "2026-01-01T00:00:00Z", "end_date": "2026-06-30T00:00:00Z"}, http.StatusForbidden, false},
		{"report_roster_no_cohort", facAdmin, http.MethodPost, "/api/reports/generate",
			map[string]any{"type": "class_roster", "format": "csv", "start_date": "2026-01-01T00:00:00Z", "end_date": "2026-06-30T00:00:00Z"}, http.StatusBadRequest, false},

		// Core routes that read Programs data
		{"user_programs", resident, http.MethodGet, user("/api/users/%d/programs", w.resident), nil, http.StatusInternalServerError, false},
		{"user_attendance_trend", resident, http.MethodGet, user("/api/users/%d/attendance-trend", w.resident), nil, http.StatusInternalServerError, false},
		{"user_attendance_export", facAdmin, http.MethodGet, user("/api/users/%d/attendance-export?all=true", w.resident), nil, http.StatusOK, false},
		{"facilities", deptAdmin, http.MethodGet, "/api/facilities", nil, http.StatusOK, false},
		{"facility_delete_check", deptAdmin, http.MethodGet, fmt.Sprintf("/api/facilities/%d/delete-check", w.north.ID), nil, http.StatusOK, false},
		{"metrics_engagement", deptAdmin, http.MethodGet, "/api/department-metrics/programs/engagement-overview?facility=all&all_time=true", nil, http.StatusOK, true},
		{"metrics_second_enrollment", deptAdmin, http.MethodGet, "/api/department-metrics/programs/second-enrollment?facility=all", nil, http.StatusOK, true},
		{"metrics_completion_matrix", deptAdmin, http.MethodGet, "/api/department-metrics/programs/completion-matrix?facility=all", nil, http.StatusOK, true},
		{"metrics_load_distribution", deptAdmin, http.MethodGet, "/api/department-metrics/programs/load-distribution?facility=all", nil, http.StatusOK, true},
		{"metrics_enrollment_by_type", deptAdmin, http.MethodGet, "/api/department-metrics/programs/enrollment-by-type?facility=all", nil, http.StatusOK, false},
		{"dashboard_class_metrics", facAdmin, http.MethodGet, "/api/dashboard/class-metrics", nil, http.StatusOK, false},
		{"dashboard_facility_health", deptAdmin, http.MethodGet, "/api/dashboard/facility-health?facility=all", nil, http.StatusOK, true},
		{"resident_verify", facAdmin, http.MethodGet, fmt.Sprintf("/api/users/resident-verify?facility_id=%d&user_id=%d&doc_id=1001", w.south.ID, w.resident.ID), nil, http.StatusOK, false},
		{"user_deactivate", facAdmin, http.MethodPost, user("/api/users/%d/deactivate", w.leaver), nil, http.StatusOK, false},
		{"users_bulk_deactivate", facAdmin, http.MethodPost, "/api/users/bulk/deactivate", map[string]any{"user_ids": []uint{w.bulkLeaver.ID}}, http.StatusOK, false},
		{"enrollments_after_deactivate", facAdmin, http.MethodGet, "/api/program-classes/1/enrollments?status=all", nil, http.StatusOK, false},
		{"account_history_after_deactivate", facAdmin, http.MethodGet, user("/api/users/%d/account-history", w.leaver), nil, http.StatusOK, false},
		{"account_history_attendance", facAdmin, http.MethodGet, user("/api/users/%d/account-history?categories=attendance", w.mmajor), nil, http.StatusOK, false},

		// Deletes last: this route removes the enrollment whose id is the
		// cohort id in the path, so it runs after everything that reads
		// enrollments.
		{"enrollments_delete", facAdmin, http.MethodDelete, "/api/programs/2/classes/2/enrollments", nil, http.StatusNoContent, false},
		{"cohort_delete", facAdmin, http.MethodDelete, "/api/program-classes/4", nil, http.StatusNoContent, false},
		{"program_classes_after_deletes", facAdmin, http.MethodGet, "/api/program-classes", nil, http.StatusOK, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := NewRequest[any](client, t, tt.method, tt.path, tt.body).
				WithTestClaims(tt.claims).AsRaw().Do().
				ExpectStatus(tt.status)
			body := strings.ReplaceAll(resp.rawBody, lms.URL, "https://lms.test")
			if tt.sorted {
				body = sortArrays(t, body)
			}
			expectGolden(t, "programs", tt.name, body)
		})
	}
}

// TestProgramsRouteAccess pins each route's access check and its
// program_management flag, the part of a route most easily lost in a move.
// Each route has a path to a North resource ("own" for the North facility
// admin and residents) and, where the check depends on the resource, the same
// route for a South one ("other").
func TestProgramsRouteAccess(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	lms := newFakeLMS(t)
	w := seedPrograms(t, env, lms)
	client := noRedirects(env)

	const (
		// anyUser is any logged-in user.
		anyUser = iota
		// admin is any admin.
		admin
		// atFacility is any user whose facility holds the resource, and
		// department and system admins.
		atFacility
		// adminAtFacility is admins of the resource's facility, and
		// department and system admins.
		adminAtFacility
		// adminAtClaimsFacility is admins whose own facility holds the
		// resource. Department admins are held to their facility too.
		adminAtClaimsFacility
		// deptAdminForAll is any admin, but only department and system
		// admins may add all=true.
		deptAdminForAll
		// selfOrAdmin is the user in the path, admins of that user's
		// facility, and department and system admins.
		selfOrAdmin
		// residentPrograms is any user, but residents also need the
		// resident_programs feature.
		residentPrograms
		// selfOrAdminResidentPrograms is selfOrAdmin, and residents also need
		// the resident_programs feature.
		selfOrAdminResidentPrograms
	)
	north, south := w.north.ID, w.south.ID
	user := func(format string, u *models.User) string { return fmt.Sprintf(format, u.ID) }

	routes := []struct {
		method     string
		own, other string
		access     int
		// gated is whether the route needs program_management.
		gated bool
	}{
		// Programs
		{http.MethodGet, "/api/programs", "", anyUser, true},
		{http.MethodGet, "/api/programs/filters", "", anyUser, true},
		{http.MethodGet, "/api/programs/1", "", anyUser, true},
		{http.MethodGet, "/api/programs/detailed-list", "", admin, true},
		{http.MethodGet, "/api/programs/stats", "", admin, true},
		{http.MethodGet, "/api/programs/1/history", "", admin, true},
		{http.MethodGet, "/api/programs/1/archive-check", "", admin, true},
		{http.MethodGet, "/api/programs/1/delete-check", "", admin, true},
		{http.MethodPost, "/api/programs", "", admin, true},
		{http.MethodDelete, "/api/programs/1", "", admin, true},
		{http.MethodPatch, "/api/programs/1/status", "", admin, true},
		{http.MethodPatch, "/api/programs/1", "", admin, true},
		{http.MethodGet, "/api/programs/csv", "/api/programs/csv?all=true", deptAdminForAll, true},

		// Classes
		{http.MethodGet, "/api/classes", "", anyUser, true},
		{http.MethodGet, "/api/classes/1", "/api/classes/3", atFacility, true},
		{http.MethodPost, "/api/classes", "", admin, true},
		{http.MethodPatch, "/api/classes/1", "/api/classes/3", adminAtFacility, true},

		// Cohorts
		{http.MethodGet, "/api/programs/1/classes", "", anyUser, true},
		{http.MethodGet, "/api/program-classes", "", anyUser, true},
		{http.MethodPost, "/api/programs/2/classes", fmt.Sprintf("/api/programs/4/classes?facility_id=%d", south), adminAtFacility, true},
		{http.MethodGet, user("/api/instructors/%d/classes", w.instructor), user("/api/instructors/%d/classes", w.southAdmin), adminAtFacility, true},
		{http.MethodPost, "/api/program-classes/bulk-cancel", "", admin, true},
		{http.MethodGet, "/api/program-classes/1", "/api/program-classes/3", atFacility, true},
		{http.MethodGet, "/api/programs/2/classes/outcomes", fmt.Sprintf("/api/programs/4/classes/outcomes?facility_id=%d", south), adminAtFacility, true},
		{http.MethodGet, "/api/program-classes/1/attendance-flags", "/api/program-classes/3/attendance-flags", adminAtFacility, true},
		{http.MethodGet, "/api/program-classes/missing-attendance", "", admin, true},
		{http.MethodGet, "/api/program-classes/1/missing-attendance", "/api/program-classes/3/missing-attendance", adminAtFacility, true},
		{http.MethodGet, "/api/program-classes/1/attendance-rate", "/api/program-classes/3/attendance-rate", adminAtFacility, true},
		{http.MethodGet, "/api/program-classes/1/history", "/api/program-classes/3/history", adminAtFacility, true},
		{http.MethodPatch, "/api/program-classes?id=1", "/api/program-classes?id=3", adminAtClaimsFacility, true},
		{http.MethodPatch, "/api/programs/1/classes/1", "/api/programs/1/classes/3", adminAtFacility, true},
		{http.MethodDelete, "/api/program-classes/1", "/api/program-classes/3", adminAtFacility, true},
		{http.MethodGet, "/api/program-classes/1/delete-check", "/api/program-classes/3/delete-check", adminAtFacility, true},

		// Events and calendars
		{http.MethodGet, "/api/student-calendar", "", residentPrograms, true},
		{http.MethodGet, "/api/program-classes/1/events", "", anyUser, true},
		{http.MethodGet, "/api/program-classes/1/canvas-schedule", "", admin, true},
		{http.MethodGet, "/api/admin-calendar", "", admin, true},
		{http.MethodGet, "/api/program-classes/todays-schedule", "", admin, true},
		{http.MethodPut, "/api/program-classes/1/events/1", "/api/program-classes/3/events/3", adminAtFacility, true},
		{http.MethodPatch, "/api/program-classes/1/events/1", "/api/program-classes/3/events/3", adminAtFacility, true},
		{http.MethodDelete, "/api/program-classes/1/events/1", "/api/program-classes/3/events/1", adminAtFacility, true},
		{http.MethodPost, "/api/program-classes/1/events/1/uncancel", "/api/program-classes/3/events/1/uncancel", adminAtFacility, true},
		{http.MethodPost, "/api/program-classes/1/events/1/uncancel-series", "/api/program-classes/3/events/3/uncancel-series", adminAtFacility, true},
		{http.MethodPost, "/api/program-classes/1/events", "/api/program-classes/3/events", adminAtFacility, true},
		{http.MethodPut, "/api/program-classes/1/events", "/api/program-classes/3/events", adminAtFacility, true},

		// Enrollments
		{http.MethodGet, "/api/program-classes/1/enrollments", "", admin, true},
		{http.MethodPost, "/api/program-classes/1/enrollment-conflicts", "", admin, true},
		{http.MethodPost, "/api/program-classes/1/enrollments", "/api/program-classes/3/enrollments", adminAtFacility, true},
		{http.MethodPatch, "/api/program-classes/1/enrollments", "/api/program-classes/3/enrollments", adminAtFacility, true},
		{http.MethodPatch, "/api/program-classes/1/enrollments/1/date", "/api/program-classes/3/enrollments/5/date", adminAtFacility, true},
		{http.MethodDelete, "/api/programs/1/classes/1/enrollments", "/api/programs/1/classes/3/enrollments", adminAtFacility, true},
		{http.MethodGet, "/api/programs/1/classes/1/enrollments/1/attendance", "/api/programs/1/classes/3/enrollments/5/attendance", adminAtFacility, true},
		{http.MethodGet, user("/api/users/%d/program-completions", w.resident), user("/api/users/%d/program-completions", w.southResident), selfOrAdmin, true},

		// Attendance
		{http.MethodGet, "/api/program-classes/1/events/1/attendance", "/api/program-classes/3/events/3/attendance", adminAtFacility, true},
		{http.MethodGet, "/api/program-classes/1/events/1/attendance-rate", "/api/program-classes/3/events/3/attendance-rate", adminAtFacility, true},
		{http.MethodGet, "/api/program-classes/1/historical-enrollment-batch", "/api/program-classes/3/historical-enrollment-batch", adminAtFacility, true},
		{http.MethodPost, "/api/program-classes/1/events/1/attendance", "/api/program-classes/3/events/3/attendance", adminAtFacility, true},
		{http.MethodDelete, user("/api/program-classes/1/events/1/attendance/%d", w.resident), user("/api/program-classes/3/events/3/attendance/%d", w.southResident), adminAtFacility, true},

		// Reports, rooms and instructors
		{http.MethodPost, "/api/reports/generate", "", admin, true},
		{http.MethodGet, "/api/rooms", "", admin, true},
		{http.MethodPost, "/api/rooms", "", admin, true},
		{http.MethodGet, fmt.Sprintf("/api/facilities/%d/instructors", north), fmt.Sprintf("/api/facilities/%d/instructors", south), adminAtFacility, true},

		// Resident routes in user_handler.go, and the dashboard's Programs
		// metrics. None needs program_management.
		{http.MethodGet, user("/api/users/%d/programs", w.resident), user("/api/users/%d/programs", w.southResident), selfOrAdminResidentPrograms, false},
		{http.MethodGet, user("/api/users/%d/attendance-trend", w.resident), user("/api/users/%d/attendance-trend", w.southResident), selfOrAdmin, false},
		{http.MethodGet, user("/api/users/%d/attendance-export", w.resident), user("/api/users/%d/attendance-export", w.southResident), adminAtFacility, false},
		{http.MethodGet, "/api/department-metrics/programs/engagement-overview", "", admin, false},
		{http.MethodGet, "/api/department-metrics/programs/second-enrollment", "", admin, false},
		{http.MethodGet, "/api/department-metrics/programs/completion-matrix", "", admin, false},
		{http.MethodGet, "/api/department-metrics/programs/load-distribution", "", admin, false},
		{http.MethodGet, "/api/department-metrics/programs/enrollment-by-type", "", admin, false},
		{http.MethodGet, "/api/dashboard/class-metrics", "", admin, false},
		{http.MethodGet, "/api/dashboard/facility-health", "", admin, false},
	}

	// passes asserts the caller gets past every access check. The body isn't
	// valid JSON, so a route that reads one stops there without side effects.
	passes := func(t *testing.T, method, path string, c *handlers.Claims) {
		t.Helper()
		resp := NewRequest[any](client, t, method, path, "not an object").
			WithTestClaims(c).AsRaw().Do()
		require.NotEqual(t, http.StatusUnauthorized, resp.resp.StatusCode, resp.rawBody)
	}
	notAllowed := func(t *testing.T, method, path string, c *handlers.Claims) {
		t.Helper()
		NewRequest[any](client, t, method, path, map[string]any{}).
			WithTestClaims(c).AsRaw().Do().
			ExpectStatus(http.StatusUnauthorized).
			ExpectRaw("User is not allowed to view this resource\n")
	}
	notAdmin := func(t *testing.T, method, path string, c *handlers.Claims) {
		t.Helper()
		NewRequest[any](client, t, method, path, map[string]any{}).
			WithTestClaims(c).Do().
			ExpectStatus(http.StatusUnauthorized).ExpectMessage("Unauthorized - not admin")
	}

	resident, southResident := w.claims(w.resident), w.claims(w.southResident)
	facAdmin, deptAdmin := w.claims(w.facAdmin), w.claims(w.deptAdmin)
	noPrograms := func(c *handlers.Claims) *handlers.Claims { return withFeatures(c, models.ProgramAccess) }
	noResidentPrograms := func(c *handlers.Claims) *handlers.Claims { return withFeatures(c, models.ResidentProgramsAccess) }
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.own, func(t *testing.T) {
			// The North facility admin passes every access check on own, so a
			// 401 here is the feature check.
			if rt.gated {
				NewRequest[any](client, t, rt.method, rt.own, map[string]any{}).
					WithTestClaims(noPrograms(facAdmin)).Do().
					ExpectStatus(http.StatusUnauthorized).ExpectMessage("Feature not enabled")
			} else {
				passes(t, rt.method, rt.own, noPrograms(facAdmin))
			}

			switch rt.access {
			case anyUser:
				passes(t, rt.method, rt.own, resident)
			case admin:
				notAdmin(t, rt.method, rt.own, resident)
				passes(t, rt.method, rt.own, facAdmin)
			case atFacility:
				passes(t, rt.method, rt.own, resident)
				notAllowed(t, rt.method, rt.other, resident)
				notAllowed(t, rt.method, rt.other, facAdmin)
				passes(t, rt.method, rt.other, deptAdmin)
			case adminAtFacility:
				notAdmin(t, rt.method, rt.own, resident)
				passes(t, rt.method, rt.own, facAdmin)
				notAllowed(t, rt.method, rt.other, facAdmin)
				passes(t, rt.method, rt.other, deptAdmin)
			case adminAtClaimsFacility:
				notAdmin(t, rt.method, rt.own, resident)
				passes(t, rt.method, rt.own, facAdmin)
				notAllowed(t, rt.method, rt.other, facAdmin)
				notAllowed(t, rt.method, rt.other, deptAdmin)
			case deptAdminForAll:
				notAllowed(t, rt.method, rt.own, resident)
				passes(t, rt.method, rt.own, facAdmin)
				notAllowed(t, rt.method, rt.other, facAdmin)
				passes(t, rt.method, rt.other, deptAdmin)
			case selfOrAdmin, selfOrAdminResidentPrograms:
				passes(t, rt.method, rt.own, resident)
				notAllowed(t, rt.method, rt.other, resident)
				notAllowed(t, rt.method, rt.other, facAdmin)
				passes(t, rt.method, rt.own, facAdmin)
				passes(t, rt.method, rt.other, deptAdmin)
				passes(t, rt.method, rt.other, southResident)
				if rt.access == selfOrAdminResidentPrograms {
					notAllowed(t, rt.method, rt.own, noResidentPrograms(resident))
					passes(t, rt.method, rt.own, noResidentPrograms(facAdmin))
				}
			case residentPrograms:
				passes(t, rt.method, rt.own, resident)
				notAllowed(t, rt.method, rt.own, noResidentPrograms(resident))
				passes(t, rt.method, rt.own, noResidentPrograms(facAdmin))
			}
		})
	}
}

// sortArrays orders every JSON array by its elements' JSON, so a route whose
// row order isn't fixed compares the same on every run.
func sortArrays(t *testing.T, body string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return body
	}
	var walk func(any) any
	walk = func(v any) any {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				v[k] = walk(x)
			}
		case []any:
			keys := make([]string, len(v))
			for i, x := range v {
				v[i] = walk(x)
				b, err := json.Marshal(v[i])
				require.NoError(t, err)
				keys[i] = string(b)
			}
			sort.Sort(byKey{keys, v})
		}
		return v
	}
	out, err := json.Marshal(walk(v))
	require.NoError(t, err)
	return string(out)
}

type byKey struct {
	keys []string
	vals []any
}

func (b byKey) Len() int           { return len(b.keys) }
func (b byKey) Less(i, j int) bool { return b.keys[i] < b.keys[j] }
func (b byKey) Swap(i, j int) {
	b.keys[i], b.keys[j] = b.keys[j], b.keys[i]
	b.vals[i], b.vals[j] = b.vals[j], b.vals[i]
}
