package integration

import (
	"testing"

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
