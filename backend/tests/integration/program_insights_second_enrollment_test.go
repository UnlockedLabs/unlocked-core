package integration

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

func TestSecondProgramEnrollment(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Second Enrollment Facility")
	require.NoError(t, err)

	progA, err := env.CreateTestProgram("Welding", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Vocational}}, nil, true, nil)
	require.NoError(t, err)
	progB, err := env.CreateTestProgram("Financial Literacy", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.LifeSkills}}, nil, true, nil)
	require.NoError(t, err)
	classA, err := env.CreateTestClass(progA, facility, programs.Active, nil)
	require.NoError(t, err)
	classB, err := env.CreateTestClass(progB, facility, programs.Active, nil)
	require.NoError(t, err)

	user, err := env.CreateTestUser("repeatres", models.Student, facility.ID, "")
	require.NoError(t, err)
	otherUser, err := env.CreateTestUser("onceres", models.Student, facility.ID, "")
	require.NoError(t, err)
	earlyUser, err := env.CreateTestUser("earlyres", models.Student, facility.ID, "")
	require.NoError(t, err)
	sameProgramUser, err := env.CreateTestUser("sameprogres", models.Student, facility.ID, "")
	require.NoError(t, err)

	completedAt := time.Now().Add(-48 * time.Hour)
	// user completes Vocational, then enrolls in LifeSkills AFTER completion -> counts
	_, err = env.CreateTestEnrollmentWithDates(classA.ID, user.ID, programs.EnrollmentCompleted,
		completedAt.Add(-time.Hour), &completedAt)
	require.NoError(t, err)
	_, err = env.CreateTestEnrollmentWithDates(classB.ID, user.ID, programs.Enrolled,
		completedAt.Add(time.Hour), nil)
	require.NoError(t, err)

	// otherUser completes Vocational but never enrolls again -> does not count
	otherCompletedAt := time.Now().Add(-24 * time.Hour)
	_, err = env.CreateTestEnrollmentWithDates(classA.ID, otherUser.ID, programs.EnrollmentCompleted,
		otherCompletedAt.Add(-time.Hour), &otherCompletedAt)
	require.NoError(t, err)

	// earlyUser enrolls in LifeSkills BEFORE completing Vocational -> the LifeSkills
	// enrollment predates the completion, so it must NOT count as a "second enrollment"
	// triggered by that completion. This is the chronological-ordering fix under test:
	// a buggy implementation that ignores order would incorrectly count this as a hit.
	earlyCompletedAt := time.Now().Add(-12 * time.Hour)
	_, err = env.CreateTestEnrollmentWithDates(classB.ID, earlyUser.ID, programs.Enrolled,
		earlyCompletedAt.Add(-time.Hour), nil)
	require.NoError(t, err)
	_, err = env.CreateTestEnrollmentWithDates(classA.ID, earlyUser.ID, programs.EnrollmentCompleted,
		earlyCompletedAt.Add(-30*time.Minute), &earlyCompletedAt)
	require.NoError(t, err)

	// sameProgramUser completes Vocational, then re-enrolls in Vocational again
	// (same program, not a different one) -> must NOT count as a "second
	// program" enrollment, even though it starts after the first completion.
	sameProgramCompletedAt := time.Now().Add(-6 * time.Hour)
	_, err = env.CreateTestEnrollmentWithDates(classA.ID, sameProgramUser.ID, programs.EnrollmentCompleted,
		sameProgramCompletedAt.Add(-time.Hour), &sameProgramCompletedAt)
	require.NoError(t, err)
	_, err = env.CreateTestEnrollmentWithDates(classA.ID, sameProgramUser.ID, programs.Enrolled,
		sameProgramCompletedAt.Add(time.Hour), nil)
	require.NoError(t, err)

	// SystemAdmin can switch facilities, so getQueryContext defaults to
	// statewide (facility_id 0) unless a facility is named explicitly -
	// pass it via the "facility" param that resolveFacilityFilter reads.
	rows := NewRequest[[]programs.SecondProgramEnrollmentRow](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/second-enrollment?facility="+strconv.Itoa(int(facility.ID)), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	require.Len(t, rows, 1)
	row := rows[0]
	require.Equal(t, "Second Enrollment Facility", row.FacilityName)
	require.Equal(t, "Vocational", row.ProgramType)
	require.Equal(t, int64(4), row.CompletedFirst)
	require.Equal(t, int64(1), row.EnrolledSecond)
	require.InDelta(t, 25.0, row.Rate, 0.01)
}
