package integration

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/stretchr/testify/require"
)

// resident_programs governs only the resident-facing Programs page. It defaults
// to on wherever program tracking is on, cascades off with its parent, and never
// changes what an admin can see.
func TestResidentProgramsAccess_EffectiveState(t *testing.T) {
	env = SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Resident Programs Facility")
	require.NoError(t, err)

	statewide := []models.FeatureAccess{models.ProgramAccess, models.ResidentProgramsAccess}
	setStatewideDefaults(t, statewide...)
	args := &models.QueryContext{Ctx: env.Context}

	t.Run("defaults to on once program tracking is on", func(t *testing.T) {
		effective, err := env.DB.GetFacilityFeatureAccess(facility.ID)
		require.NoError(t, err)
		require.Contains(t, effective, models.ResidentProgramsAccess,
			"an admin must take an explicit action to hide the Programs page")
	})

	t.Run("turning it off leaves program tracking itself on", func(t *testing.T) {
		require.NoError(t, env.DB.UpsertFacilityFeatureFlag(args, facility.ID, models.ResidentProgramsAccess, false))

		effective, err := env.DB.GetFacilityFeatureAccess(facility.ID)
		require.NoError(t, err)
		require.NotContains(t, effective, models.ResidentProgramsAccess)
		require.Contains(t, effective, models.ProgramAccess)
	})

	t.Run("turning it back on restores resident visibility", func(t *testing.T) {
		require.NoError(t, env.DB.UpsertFacilityFeatureFlag(args, facility.ID, models.ResidentProgramsAccess, true))

		effective, err := env.DB.GetFacilityFeatureAccess(facility.ID)
		require.NoError(t, err)
		require.Contains(t, effective, models.ResidentProgramsAccess)
	})

	// If program tracking is later turned off entirely, the Programs page must
	// behave as if the flag is off regardless of its last set value.
	t.Run("disabling program tracking cascades even with the flag left on", func(t *testing.T) {
		require.NoError(t, env.DB.UpsertFacilityFeatureFlag(args, facility.ID, models.ProgramAccess, false))

		effective, err := env.DB.GetFacilityFeatureAccess(facility.ID)
		require.NoError(t, err)
		require.NotContains(t, effective, models.ProgramAccess)
		require.NotContains(t, effective, models.ResidentProgramsAccess,
			"resident_programs is still stored as enabled, but its parent is off")

		err = env.DB.UpsertFacilityFeatureFlag(args, facility.ID, models.ResidentProgramsAccess, true)
		require.Error(t, err, "the flag isn't toggleable while program tracking is off")
	})
}

// Direct navigation to the resident Programs page must not render, which means
// the endpoints behind it have to refuse the resident — not just the nav item
// disappearing client-side.
func TestResidentProgramsAccess_RouteGating(t *testing.T) {
	env = SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Resident Programs Route Facility")
	require.NoError(t, err)
	resident, err := env.CreateTestUser("residentprograms", models.Student, facility.ID, "")
	require.NoError(t, err)

	withPage := []models.FeatureAccess{models.ProgramAccess, models.ResidentProgramsAccess}
	withoutPage := []models.FeatureAccess{models.ProgramAccess}

	residentClaims := func(features []models.FeatureAccess) *handlers.Claims {
		return &handlers.Claims{
			UserID:        resident.ID,
			Role:          models.Student,
			FacilityID:    facility.ID,
			FeatureAccess: features,
		}
	}
	programsURL := fmt.Sprintf("/api/users/%d/programs", resident.ID)
	calendarURL := "/api/student-calendar"

	// handleGetUserPrograms runs raw Postgres-only SQL, so it can't return 200
	// against the in-memory SQLite test DB. Asserting "not refused" is what this
	// test is actually about: the gate let the request through to the handler.
	expectNotRefused := func(t *testing.T, claims *handlers.Claims, url string) {
		t.Helper()
		resp := NewRequest[any](env.Client, t, http.MethodGet, url, nil).
			WithTestClaims(claims).
			Do()
		require.NotEqual(t, http.StatusUnauthorized, resp.resp.StatusCode,
			"the resident_programs gate must not block this caller")
	}

	t.Run("resident with the page enabled reaches their programs", func(t *testing.T) {
		expectNotRefused(t, residentClaims(withPage), programsURL)
	})

	t.Run("resident with the page enabled reaches the class calendar", func(t *testing.T) {
		NewRequest[any](env.Client, t, http.MethodGet, calendarURL, nil).
			WithTestClaims(residentClaims(withPage)).
			Do().
			ExpectStatus(http.StatusOK)
	})

	t.Run("resident with the page hidden is refused their programs", func(t *testing.T) {
		NewRequest[any](env.Client, t, http.MethodGet, programsURL, nil).
			WithTestClaims(residentClaims(withoutPage)).
			Do().
			ExpectStatus(http.StatusUnauthorized)
	})

	t.Run("resident with the page hidden is refused the class calendar", func(t *testing.T) {
		NewRequest[any](env.Client, t, http.MethodGet, calendarURL, nil).
			WithTestClaims(residentClaims(withoutPage)).
			Do().
			ExpectStatus(http.StatusUnauthorized)
	})

	// Hiding the page from residents must not touch any admin-side view.
	t.Run("admin without the flag still sees the resident's programs", func(t *testing.T) {
		expectNotRefused(t, &handlers.Claims{
			Role:          models.FacilityAdmin,
			FacilityID:    facility.ID,
			FeatureAccess: withoutPage,
		}, programsURL)
	})
}
