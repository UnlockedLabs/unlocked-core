package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/stretchr/testify/require"
)

// These tests pin the core routes that read Open Content's tables (EN-198):
// the facility list's can_delete, the facility delete check and delete, and
// the resident profile's activity engagement. They must pass unchanged once
// core asks Open Content for those numbers and the tables move into the
// open_content schema.
//
// Regenerate the golden files after an intended change with:
//
//	go test ./tests/integration -run 'TestOpenContentCoreReads' -update

type openContentCoreWorld struct {
	north, reading, favorites, visibility, empty *models.Facility
	resident, deptAdmin                          *models.User
}

// seedOpenContentCore adds facilities that each hold one kind of Open Content
// data, so each golden shows which kinds block deleting a facility:
//   - North has the residents, and every kind of Open Content data;
//   - Reading Room has only reading activity;
//   - Favorites Hall has only a facility-wide favorite;
//   - Visibility Annex has only a content visibility setting;
//   - Empty Wing has nothing.
func seedOpenContentCore(t *testing.T, env *TestEnv) openContentCoreWorld {
	t.Helper()
	var w openContentCoreWorld
	for _, f := range []struct {
		dst  **models.Facility
		name string
	}{
		{&w.north, "North"}, {&w.reading, "Reading Room"}, {&w.favorites, "Favorites Hall"},
		{&w.visibility, "Visibility Annex"}, {&w.empty, "Empty Wing"},
	} {
		facility, err := env.CreateTestFacility(f.name)
		require.NoError(t, err)
		*f.dst = facility
	}
	var err error
	w.resident, err = env.CreateTestUser("jdoe", models.Student, w.north.ID, "1001")
	require.NoError(t, err)
	w.deptAdmin, err = env.CreateTestUser("deptadmin", models.DepartmentAdmin, w.north.ID, "")
	require.NoError(t, err)

	day := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO libraries (id, open_content_provider_id, external_id, title, language, description, url, thumbnail_url, created_at, updated_at)
			VALUES (1, 1, 'lib-algebra', 'Algebra', 'eng', 'Algebra for everyone', '/content/algebra', NULL, ?, ?)`, []any{day, day}},
		{`INSERT INTO open_content_urls (id, content_url) VALUES (1, '/api/proxy/libraries/1/content/algebra/A/Lesson_1')`, nil},
		{`INSERT INTO open_content_activities (open_content_provider_id, facility_id, user_id, content_id, open_content_url_id, request_ts, stop_ts) VALUES
			(1, ?, ?, 1, 1, ?, ?), (1, ?, ?, 1, 1, ?, ?)`,
			[]any{w.north.ID, w.resident.ID, day, day.Add(30 * time.Minute), w.reading.ID, w.resident.ID, day.Add(time.Hour), day.Add(90 * time.Minute)}},
		{`INSERT INTO open_content_favorites (user_id, content_id, open_content_provider_id, facility_id, created_at) VALUES
			(?, 1, 1, NULL, ?), (?, 1, 1, ?, ?), (?, 1, 1, ?, ?)`,
			[]any{w.resident.ID, day, w.deptAdmin.ID, w.north.ID, day, w.deptAdmin.ID, w.favorites.ID, day}},
		{`INSERT INTO facility_visibility_statuses (facility_id, open_content_provider_id, content_id, visibility_status) VALUES
			(?, 1, 1, true), (?, 1, 1, true)`, []any{w.north.ID, w.visibility.ID}},
	} {
		require.NoError(t, env.DB.Exec(stmt.sql, stmt.args...).Error, stmt.sql)
	}
	return w
}

func TestOpenContentCoreReads(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	w := seedOpenContentCore(t, env)
	deptAdmin := &handlers.Claims{UserID: w.deptAdmin.ID, Username: w.deptAdmin.Username, Role: w.deptAdmin.Role, FacilityID: w.north.ID, FacilityName: w.north.Name}
	facility := func(format string, f *models.Facility) string { return fmt.Sprintf(format, f.ID) }

	// The cases run in order against one database, so the list after the
	// deletes shows them.
	tests := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{"facilities", http.MethodGet, "/api/facilities", http.StatusOK},
		{"delete_check_north", http.MethodGet, facility("/api/facilities/%d/delete-check", w.north), http.StatusOK},
		{"delete_check_reading", http.MethodGet, facility("/api/facilities/%d/delete-check", w.reading), http.StatusOK},
		{"delete_check_favorites", http.MethodGet, facility("/api/facilities/%d/delete-check", w.favorites), http.StatusOK},
		{"delete_check_visibility", http.MethodGet, facility("/api/facilities/%d/delete-check", w.visibility), http.StatusOK},
		{"delete_check_empty", http.MethodGet, facility("/api/facilities/%d/delete-check", w.empty), http.StatusOK},
		{"delete_reading", http.MethodDelete, facility("/api/facilities/%d", w.reading), http.StatusConflict},
		{"delete_favorites", http.MethodDelete, facility("/api/facilities/%d", w.favorites), http.StatusConflict},
		{"delete_visibility", http.MethodDelete, facility("/api/facilities/%d", w.visibility), http.StatusNoContent},
		{"delete_empty", http.MethodDelete, facility("/api/facilities/%d", w.empty), http.StatusNoContent},
		{"facilities_after_deletes", http.MethodGet, "/api/facilities", http.StatusOK},
		// The profile's session numbers use Postgres-only SQL, so on SQLite it
		// answers 500 before it reaches Open Content's activity numbers.
		// EN-198 checks the whole profile on Postgres.
		{"resident_profile", http.MethodGet, fmt.Sprintf("/api/users/%d/profile", w.resident.ID), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := NewRequest[any](env.Client, t, tt.method, tt.path, nil).
				WithTestClaims(deptAdmin).AsRaw().Do().
				ExpectStatus(tt.status)
			expectGolden(t, "open_content_core_reads", tt.name, resp.rawBody)
		})
	}
}
