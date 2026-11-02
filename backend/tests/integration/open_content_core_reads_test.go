package integration

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	ocapi "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/stretchr/testify/require"
)

// These tests pin the core routes that use Open Content's numbers (EN-198):
// the facility list's can_delete, the facility delete check and delete, and
// the resident profile's activity engagement. Since EN-205 core asks Open
// Content's service, here a stand-in that answers with the numbers the
// original seed's rows gave, and the golden files are unchanged.
//
// Regenerate the golden files after an intended change with:
//
//	go test ./tests/integration -run 'TestOpenContentCoreReads' -update

type openContentCoreWorld struct {
	north, reading, favorites, visibility, empty *models.Facility
	resident, deptAdmin                          *models.User
}

// seedOpenContentCore adds facilities that each have one kind of Open Content
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

	// The numbers Open Content counts for each facility: its reading
	// activity, facility-wide favorites and visibility settings.
	env.OpenContent.set(func(f *fakeOpenContent) {
		f.facilityContent = map[uint]ocapi.FacilityContent{
			w.north.ID:      {Activities: 1, Favorites: 1, VisibilityStatuses: 1},
			w.reading.ID:    {Activities: 1},
			w.favorites.ID:  {Favorites: 1},
			w.visibility.ID: {VisibilityStatuses: 1},
		}
	})
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
