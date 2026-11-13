package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	programsapi "github.com/UnlockedLabs/unlocked-contracts/programs"
	"github.com/stretchr/testify/require"
)

// These tests pin the rest of core's routes that ask Programs: the dashboard
// and department metrics, the users list's class filters, deactivating a
// resident, and the account history's attendance rows. The
// stand-in Programs answers with what the original seed's rows gave, so the
// golden files are the ones the routes answered before Programs moved out.
//
// Regenerate the golden files after an intended change with:
//
//	go test ./tests/integration -run 'TestProgramsCoreRoutes' -update

type programsRoutesWorld struct {
	north, south *models.Facility
	// jdoe, leaver and bulkLeaver are in class 1; newbie could join it.
	// mmajor completed it.
	resident, mmajor, newbie, leaver, bulkLeaver, southResident *models.User
	facAdmin, southAdmin, deptAdmin, instructor                 *models.User
}

func (w programsRoutesWorld) claims(u *models.User) *handlers.Claims {
	return &handlers.Claims{UserID: u.ID, Username: u.Username, Role: u.Role, FacilityID: u.FacilityID, TimeZone: "America/Chicago"}
}

// seedProgramsRoutes adds North and South, their users, and Programs'
// answers about them.
func seedProgramsRoutes(t *testing.T, env *TestEnv) programsRoutesWorld {
	t.Helper()
	var w programsRoutesWorld
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
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE users SET name_first = 'John', name_last = 'Doe' WHERE id = ?`, []any{w.resident.ID}},
		{`UPDATE users SET name_first = 'Mary', name_last = 'Major' WHERE id = ?`, []any{w.mmajor.ID}},
		{`UPDATE users SET name_first = 'Ivy', name_last = 'Instructor' WHERE id = ?`, []any{w.instructor.ID}},
		// The account history row Programs writes when it records attendance.
		{`INSERT INTO user_account_history (user_id, admin_id, action, attendance_status, class_name, session_date, created_at)
			VALUES (?, ?, 'attendance_recorded', 'partial', 'Reading 101', ?, ?)`,
			[]any{w.mmajor.ID, w.facAdmin.ID, time.Date(2026, 1, 26, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 26, 15, 0, 0, 0, time.UTC)}},
	} {
		require.NoError(t, env.DB.Exec(stmt.sql, stmt.args...).Error, stmt.sql)
	}

	north, south := w.north.ID, w.south.ID
	env.Programs.set(func(f *fakePrograms) {
		f.enrolled = map[uint][]uint{1: {w.resident.ID, w.leaver.ID, w.bulkLeaver.ID}}
		f.eligible = map[uint][]uint{1: {w.newbie.ID}}
		f.engagement = programsapi.ProgramEngagementOverview{
			ActiveResidents: 5, PreviouslyEngagedResidents: 1, TotalResidents: 6,
			TopPrograms: []programsapi.ProgramCompletionRank{{ProgramName: "Adult Literacy", Enrolled: 5, Completed: 1, CompletionRate: 20}},
		}
		f.secondEnrollment = []programsapi.SecondProgramEnrollmentRow{{FacilityName: "North", ProgramType: "Educational", CompletedFirst: 1}}
		f.completionMatrix = []programsapi.ProgramCompletionMatrixCell{
			{ProgramName: "Adult Literacy", FacilityID: south, FacilityName: "South", Enrolled: 1, Insufficient: true},
			{ProgramName: "Welding", FacilityID: north, FacilityName: "North", Enrolled: 2, Insufficient: true},
			{ProgramName: "Adult Literacy", FacilityID: north, FacilityName: "North", Enrolled: 4, Completed: 1, CompletionRate: 25, DeltaFromFacilityAverage: 8.333333333333336},
		}
		f.loadDistribution = programsapi.ProgramLoadDistribution{
			Statewide: []programsapi.ProgramLoadBucket{{Bucket: "0", Count: 2}, {Bucket: "1", Count: 3}, {Bucket: "2", Count: 1}, {Bucket: "3"}, {Bucket: "4+"}},
			ByFacility: []programsapi.ProgramLoadFacilityRow{
				{FacilityID: north, FacilityName: "North", Zero: 2, One: 2, Two: 1, Total: 5},
				{FacilityID: south, FacilityName: "South", One: 1, Total: 1},
			},
		}
		f.enrollmentByType = []programsapi.ProgramTypeEnrollment{
			{ProgramType: "Educational", Enrolled: 5, Completed: 1, Rate: 20},
			{ProgramType: "Vocational", Enrolled: 2},
		}
		f.classMetrics = programsapi.ClassDashboardMetrics{ActiveClasses: 1, ScheduledClasses: 2, TotalEnrollments: 3, TotalSeats: 10}
		f.facilityHealth = []programsapi.FacilityHealthSummary{
			{FacilityID: 1, FacilityName: "Default"},
			{FacilityID: south, FacilityName: "South", Programs: 2, ActiveClasses: 1, Enrollment: 1},
			{FacilityID: north, FacilityName: "North", Programs: 2, ActiveClasses: 1, Enrollment: 3},
		}
	})
	return w
}

func TestProgramsCoreRoutes(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	w := seedProgramsRoutes(t, env)
	client := noRedirects(env)

	facAdmin, deptAdmin := w.claims(w.facAdmin), w.claims(w.deptAdmin)
	user := func(format string, u *models.User) string { return fmt.Sprintf(format, u.ID) }

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
		{"users_only_unenrolled", facAdmin, http.MethodGet, "/api/users?include=only_unenrolled&cohort_id=1", nil, http.StatusOK, false},
		{"users_only_enrolled", facAdmin, http.MethodGet, "/api/users?include=only_enrolled&cohort_id=1", nil, http.StatusOK, false},
		{"users_include_bad_cohort", facAdmin, http.MethodGet, "/api/users?include=only_enrolled&cohort_id=abc", nil, http.StatusBadRequest, false},
		{"metrics_engagement", deptAdmin, http.MethodGet, "/api/department-metrics/programs/engagement-overview?facility=all&all_time=true", nil, http.StatusOK, true},
		{"metrics_second_enrollment", deptAdmin, http.MethodGet, "/api/department-metrics/programs/second-enrollment?facility=all", nil, http.StatusOK, true},
		{"metrics_completion_matrix", deptAdmin, http.MethodGet, "/api/department-metrics/programs/completion-matrix?facility=all", nil, http.StatusOK, true},
		{"metrics_load_distribution", deptAdmin, http.MethodGet, "/api/department-metrics/programs/load-distribution?facility=all", nil, http.StatusOK, true},
		{"metrics_enrollment_by_type", deptAdmin, http.MethodGet, "/api/department-metrics/programs/enrollment-by-type?facility=all", nil, http.StatusOK, false},
		{"dashboard_class_metrics", facAdmin, http.MethodGet, "/api/dashboard/class-metrics", nil, http.StatusOK, false},
		{"dashboard_facility_health", deptAdmin, http.MethodGet, "/api/dashboard/facility-health?facility=all", nil, http.StatusOK, true},
		{"user_deactivate", facAdmin, http.MethodPost, user("/api/users/%d/deactivate", w.leaver), nil, http.StatusOK, false},
		{"users_bulk_deactivate", facAdmin, http.MethodPost, "/api/users/bulk/deactivate", map[string]any{"user_ids": []uint{w.bulkLeaver.ID}}, http.StatusOK, false},
		{"account_history_after_deactivate", facAdmin, http.MethodGet, user("/api/users/%d/account-history", w.leaver), nil, http.StatusOK, false},
		{"account_history_attendance", facAdmin, http.MethodGet, user("/api/users/%d/account-history?categories=attendance", w.mmajor), nil, http.StatusOK, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := NewRequest[any](client, t, tt.method, tt.path, tt.body).
				WithTestClaims(tt.claims).AsRaw().Do().
				ExpectStatus(tt.status)
			body := resp.rawBody
			if tt.sorted {
				body = sortArrays(t, body)
			}
			expectGolden(t, "programs_core_routes", tt.name, body)
		})
	}

	t.Run("deactivating withdraws the enrollments in Programs", func(t *testing.T) {
		_, withdrawn := env.Programs.changes()
		require.Equal(t, []uint{w.leaver.ID, w.bulkLeaver.ID}, withdrawn)
	})
}

// TestProgramsCoreRouteAccess pins who may call core's Programs dashboard and
// metrics routes: an admin with program_management.
func TestProgramsCoreRouteAccess(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	w := seedProgramsRoutes(t, env)
	client := noRedirects(env)

	resident, facAdmin := w.claims(w.resident), w.claims(w.facAdmin)
	for _, path := range []string{
		"/api/department-metrics/programs/engagement-overview",
		"/api/department-metrics/programs/second-enrollment",
		"/api/department-metrics/programs/completion-matrix",
		"/api/department-metrics/programs/load-distribution",
		"/api/department-metrics/programs/enrollment-by-type",
		"/api/dashboard/class-metrics",
		"/api/dashboard/facility-health",
	} {
		t.Run(path, func(t *testing.T) {
			NewRequest[any](client, t, http.MethodGet, path, nil).
				WithTestClaims(resident).Do().
				ExpectStatus(http.StatusUnauthorized).ExpectMessage("Unauthorized - not admin")
			NewRequest[any](client, t, http.MethodGet, path, nil).
				WithTestClaims(withFeatures(facAdmin, models.ProgramAccess)).Do().
				ExpectStatus(http.StatusUnauthorized).ExpectMessage("Feature not enabled")
			resp := NewRequest[any](client, t, http.MethodGet, path, nil).
				WithTestClaims(facAdmin).AsRaw().Do()
			require.NotEqual(t, http.StatusUnauthorized, resp.resp.StatusCode, resp.rawBody)
		})
	}
}

// TestProgramsChangesFirst pins that a transfer or a deactivation asks
// Programs first, and changes nothing in core when Programs fails.
func TestProgramsChangesFirst(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	w := seedProgramsRoutes(t, env)
	env.Programs.set(func(f *fakePrograms) { f.failChanges = true })
	facAdmin, deptAdmin := w.claims(w.facAdmin), w.claims(w.deptAdmin)

	t.Run("transfer", func(t *testing.T) {
		NewRequest[any](env.Client, t, http.MethodPatch, "/api/users/resident-transfer", map[string]any{
			"user_id": w.resident.ID, "curr_facility_id": w.north.ID, "trans_facility_id": w.south.ID,
		}).WithTestClaims(deptAdmin).Do().
			ExpectStatus(http.StatusInternalServerError).ExpectMessage("error updating program_class_enrollments")
		resident, err := env.DB.GetUserByID(w.resident.ID)
		require.NoError(t, err)
		require.Equal(t, w.north.ID, resident.FacilityID)
	})
	t.Run("deactivate", func(t *testing.T) {
		NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/users/%d/deactivate", w.leaver.ID), nil).
			WithTestClaims(facAdmin).Do().
			ExpectStatus(http.StatusInternalServerError).ExpectMessage("error updating program_class_enrollments")
		leaver, err := env.DB.GetUserByID(w.leaver.ID)
		require.NoError(t, err)
		require.Nil(t, leaver.DeactivatedAt)
	})
	t.Run("bulk deactivate", func(t *testing.T) {
		resp := NewRequest[struct {
			SuccessCount int `json:"success_count"`
			FailedCount  int `json:"failed_count"`
		}](env.Client, t, http.MethodPost, "/api/users/bulk/deactivate", map[string]any{"user_ids": []uint{w.bulkLeaver.ID}}).
			WithTestClaims(facAdmin).Do().
			ExpectStatus(http.StatusOK)
		require.Equal(t, 0, resp.GetData().SuccessCount)
		require.Equal(t, 1, resp.GetData().FailedCount)
		bulkLeaver, err := env.DB.GetUserByID(w.bulkLeaver.ID)
		require.NoError(t, err)
		require.Nil(t, bulkLeaver.DeactivatedAt)
	})
	ended, withdrawn := env.Programs.changes()
	require.Empty(t, ended)
	require.Empty(t, withdrawn)
}

// A transfer to a facility that doesn't exist fails before Programs ends any
// enrollments, so the resident's classes stay as they were. Programs fails its
// changes here, so a transfer that asked it would answer 500, not 400.
func TestTransferToUnknownFacility(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	w := seedProgramsRoutes(t, env)
	env.Programs.set(func(f *fakePrograms) { f.failChanges = true })

	NewRequest[any](env.Client, t, http.MethodPatch, "/api/users/resident-transfer", map[string]any{
		"user_id": w.resident.ID, "curr_facility_id": w.north.ID, "trans_facility_id": 999,
	}).WithTestClaims(w.claims(w.deptAdmin)).Do().
		ExpectStatus(http.StatusBadRequest).ExpectMessage("facilities not found")
	resident, err := env.DB.GetUserByID(w.resident.ID)
	require.NoError(t, err)
	require.Equal(t, w.north.ID, resident.FacilityID)
	ended, _ := env.Programs.changes()
	require.Empty(t, ended)
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
