package integration

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

func TestProgramCompletionMatrix(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Matrix Facility")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("GED Prep", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	class, err := env.CreateTestClass(program, facility, programs.Active, nil)
	require.NoError(t, err)

	// 3 enrollees, 1 completed -> enrolled=3 meets the n>=3 threshold
	for i := 0; i < 2; i++ {
		u, err := env.CreateTestUser("matrixres"+string(rune('a'+i)), models.Student, facility.ID, "")
		require.NoError(t, err)
		_, err = env.CreateTestEnrollment(class.ID, u.ID, programs.Enrolled)
		require.NoError(t, err)
	}
	completedUser, err := env.CreateTestUser("matrixrescompleted", models.Student, facility.ID, "")
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(class.ID, completedUser.ID, programs.EnrollmentCompleted)
	require.NoError(t, err)

	// A second program in the SAME facility, with only 2 enrollees (below the
	// n>=3 threshold) and 1 completion, both to exercise the "insufficient"
	// branch distinctly AND to give the facility average a second, different
	// cell so the delta calculation is a genuine average, not a trivial
	// single-cell equivalence.
	smallProgram, err := env.CreateTestProgram("Financial Literacy", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.LifeSkills}}, nil, true, nil)
	require.NoError(t, err)
	smallClass, err := env.CreateTestClass(smallProgram, facility, programs.Active, nil)
	require.NoError(t, err)

	smallCompletedUser, err := env.CreateTestUser("matrixressmall", models.Student, facility.ID, "")
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(smallClass.ID, smallCompletedUser.ID, programs.EnrollmentCompleted)
	require.NoError(t, err)
	smallEnrolledUser, err := env.CreateTestUser("matrixressmallb", models.Student, facility.ID, "")
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(smallClass.ID, smallEnrolledUser.ID, programs.Enrolled)
	require.NoError(t, err)

	cells := NewRequest[[]programs.ProgramCompletionMatrixCell](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/completion-matrix?facility="+strconv.Itoa(int(facility.ID)), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	require.Len(t, cells, 2)

	var gedCell, smallCell programs.ProgramCompletionMatrixCell
	for _, c := range cells {
		switch c.ProgramName {
		case "GED Prep":
			gedCell = c
		case "Financial Literacy":
			smallCell = c
		}
	}

	require.Equal(t, "Matrix Facility", gedCell.FacilityName)
	require.Equal(t, int64(3), gedCell.Enrolled)
	require.Equal(t, int64(1), gedCell.Completed)
	require.False(t, gedCell.Insufficient)
	require.InDelta(t, 33.33, gedCell.CompletionRate, 0.01)

	require.Equal(t, "Matrix Facility", smallCell.FacilityName)
	require.Equal(t, int64(2), smallCell.Enrolled)
	require.Equal(t, int64(1), smallCell.Completed)
	require.True(t, smallCell.Insufficient)
	require.Equal(t, 0.0, smallCell.CompletionRate)
	require.Equal(t, 0.0, smallCell.DeltaFromFacilityAverage)

	// Facility average across both cells: (1 + 1) completed / (3 + 2) enrolled = 40%.
	// GED Prep's own rate is 33.33%, so its delta should be 33.33 - 40 = -6.67,
	// not 0 - this is the assertion that actually proves the delta is computed
	// relative to a genuine multi-cell facility average.
	require.InDelta(t, -6.67, gedCell.DeltaFromFacilityAverage, 0.01)
}
