package integration

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

func TestProgramCompletionFlag(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Completion Flag Facility")
	require.NoError(t, err)

	admin, err := env.CreateTestUser("flagadmin", models.SystemAdmin, facility.ID, "")
	require.NoError(t, err)

	claims := &handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: facility.ID}

	body := map[string]any{
		"name":                   "Flag Program",
		"description":            "program with its own completion",
		"funding_type":           programs.FederalGrants,
		"is_active":              true,
		"has_program_completion": true,
		"program_types":          []map[string]any{{"program_type": "Educational"}},
		"credit_types":           []map[string]any{{"credit_type": "Completion"}},
		"facilities":             []uint{facility.ID},
	}

	created := NewRequest[programs.Program](env.Client, t, http.MethodPost, "/api/programs", body).
		WithTestClaims(claims).
		Do().
		ExpectStatus(http.StatusCreated).
		GetData()

	require.True(t, created.HasProgramCompletion, "flag must survive create")

	var stored programs.Program
	require.NoError(t, env.DB.First(&stored, created.ID).Error)
	require.True(t, stored.HasProgramCompletion, "flag must be persisted, not just echoed back")

	t.Run("defaults to false when the field is omitted", func(t *testing.T) {
		plain := map[string]any{
			"name":          "Plain Program",
			"description":   "no program-level completion",
			"funding_type":  programs.FederalGrants,
			"is_active":     true,
			"program_types": []map[string]any{{"program_type": "Educational"}},
			"credit_types":  []map[string]any{{"credit_type": "Completion"}},
			"facilities":    []uint{facility.ID},
		}
		p := NewRequest[programs.Program](env.Client, t, http.MethodPost, "/api/programs", plain).
			WithTestClaims(claims).
			Do().
			ExpectStatus(http.StatusCreated).
			GetData()

		require.False(t, p.HasProgramCompletion,
			"omitting the field must mean this is pre has completion behaviour, never true")
	})

	t.Run("can be turned off again through update", func(t *testing.T) {
		update := map[string]any{
			"name":                   "Flag Program",
			"description":            "program with its own completion",
			"funding_type":           programs.FederalGrants,
			"is_active":              true,
			"has_program_completion": false,
			"program_types":          []map[string]any{{"program_type": "Educational"}},
			"credit_types":           []map[string]any{{"credit_type": "Completion"}},
			"facilities":             []uint{facility.ID},
		}
		NewRequest[programs.Program](env.Client, t, http.MethodPatch,
			fmt.Sprintf("/api/programs/%d", created.ID), update).
			WithTestClaims(claims).
			Do().
			ExpectStatus(http.StatusOK)

		var after programs.Program
		require.NoError(t, env.DB.First(&after, created.ID).Error)
		require.False(t, after.HasProgramCompletion,
			"clearing the flag must persist -- if this fails, HasProgramCompletion is missing "+
				"from the Select list in UpdateProgram and every change to it is silently dropped")
	})

	// The program-detail Edit Program dialog does not send this field at all. Because the
	// column IS in UpdateProgram's Select list, a plain bool would decode to false and clear
	// the flag on every such edit -- silently, with a 200 response. Hence the *bool.
	t.Run("an update that omits the field leaves it alone", func(t *testing.T) {
		require.NoError(t, env.DB.Model(&programs.Program{}).
			Where("id = ?", created.ID).
			Update("has_program_completion", true).Error)

		withoutField := map[string]any{
			"name":          "Flag Program",
			"description":   "edited without touching the flag",
			"funding_type":  programs.FederalGrants,
			"is_active":     true,
			"program_types": []map[string]any{{"program_type": "Educational"}},
			"credit_types":  []map[string]any{{"credit_type": "Completion"}},
			"facilities":    []uint{facility.ID},
		}
		NewRequest[programs.Program](env.Client, t, http.MethodPatch,
			fmt.Sprintf("/api/programs/%d", created.ID), withoutField).
			WithTestClaims(claims).
			Do().
			ExpectStatus(http.StatusOK)

		var after programs.Program
		require.NoError(t, env.DB.First(&after, created.ID).Error)
		require.True(t, after.HasProgramCompletion,
			"omitting has_program_completion must PRESERVE it, not clear it -- if this fails, "+
				"ProgramForm.HasProgramCompletion is a plain bool again and every edit from a "+
				"client that does not send the field wipes the flag")
	})

	t.Run("can be turned on again through update", func(t *testing.T) {
		update := map[string]any{
			"name":                   "Flag Program",
			"description":            "program with its own completion",
			"funding_type":           programs.FederalGrants,
			"is_active":              true,
			"has_program_completion": true,
			"program_types":          []map[string]any{{"program_type": "Educational"}},
			"credit_types":           []map[string]any{{"credit_type": "Completion"}},
			"facilities":             []uint{facility.ID},
		}
		NewRequest[programs.Program](env.Client, t, http.MethodPatch,
			fmt.Sprintf("/api/programs/%d", created.ID), update).
			WithTestClaims(claims).
			Do().
			ExpectStatus(http.StatusOK)

		var after programs.Program
		require.NoError(t, env.DB.First(&after, created.ID).Error)
		require.True(t, after.HasProgramCompletion, "setting the flag must persist")
	})
}
