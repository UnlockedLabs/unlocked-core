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

func TestProgramEngagementOverview(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Engagement Facility")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("GED Prep", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	class, err := env.CreateTestClass(program, facility, programs.Active, nil)
	require.NoError(t, err)

	// 3 residents: one active, one completed-only (not active), one never enrolled
	// (usernames must be alphanumunicode: no hyphens/underscores)
	activeUser, err := env.CreateTestUser("activeres", models.Student, facility.ID, "")
	require.NoError(t, err)
	completedUser, err := env.CreateTestUser("completedres", models.Student, facility.ID, "")
	require.NoError(t, err)
	_, err = env.CreateTestUser("neverres", models.Student, facility.ID, "")
	require.NoError(t, err)

	now := time.Now()
	_, err = env.CreateTestEnrollmentWithDates(class.ID, activeUser.ID, programs.Enrolled, now, nil)
	require.NoError(t, err)
	// Completed well before the endpoint's default 30-day lookback window, so it
	// falls into "previously engaged" rather than "active in this range".
	completedEnrolledAt := now.AddDate(0, 0, -40)
	completedEndedAt := now.AddDate(0, 0, -35)
	_, err = env.CreateTestEnrollmentWithDates(class.ID, completedUser.ID, programs.EnrollmentCompleted, completedEnrolledAt, &completedEndedAt)
	require.NoError(t, err)

	overview := NewRequest[programs.ProgramEngagementOverview](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/engagement-overview?facility="+strconv.Itoa(int(facility.ID)), nil).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	require.Equal(t, int64(3), overview.TotalResidents)
	require.Equal(t, int64(1), overview.ActiveResidents)
	require.Equal(t, int64(1), overview.PreviouslyEngagedResidents)
	// never-engaged = residents with ZERO enrollment rows ever -> only "never-res"
	require.Equal(t, int64(1), overview.NeverEngagedResidents)
	require.Len(t, overview.TopPrograms, 0) // below the >=5-enrollee threshold, so excluded
}

// enrollNResidents creates n Student residents in facility and enrolls each of them
// in classID with the given status, all with enrolled_at set to now (so they fall
// within the endpoint's default 30-day lookback window with no explicit date range).
func enrollNResidents(t *testing.T, env *TestEnv, facilityID uint, classID uint, usernamePrefix string, n int, status programs.ProgramEnrollmentStatus) {
	t.Helper()
	now := time.Now()
	for i := 0; i < n; i++ {
		user, err := env.CreateTestUser(usernamePrefix+strconv.Itoa(i), models.Student, facilityID, "")
		require.NoError(t, err)
		var endedAt *time.Time
		if status != programs.Enrolled {
			ended := now.Add(time.Hour)
			endedAt = &ended
		}
		_, err = env.CreateTestEnrollmentWithDates(classID, user.ID, status, now, endedAt)
		require.NoError(t, err)
	}
}

func TestProgramEngagementOverview_TopProgramsRanking(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Ranking Facility")
	require.NoError(t, err)

	programA, err := env.CreateTestProgram("Program A", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	classA, err := env.CreateTestClass(programA, facility, programs.Active, nil)
	require.NoError(t, err)

	programB, err := env.CreateTestProgram("Program B", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	classB, err := env.CreateTestClass(programB, facility, programs.Active, nil)
	require.NoError(t, err)

	programC, err := env.CreateTestProgram("Program C", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	classC, err := env.CreateTestClass(programC, facility, programs.Active, nil)
	require.NoError(t, err)

	// Program A: 5 enrollees, 4 completed, 1 still enrolled -> 80% completion, clears threshold.
	enrollNResidents(t, env, facility.ID, classA.ID, "proga", 4, programs.EnrollmentCompleted)
	enrollNResidents(t, env, facility.ID, classA.ID, "progaactive", 1, programs.Enrolled)

	// Program B: 5 enrollees, 1 completed, 4 still enrolled -> 20% completion, clears threshold.
	enrollNResidents(t, env, facility.ID, classB.ID, "progb", 1, programs.EnrollmentCompleted)
	enrollNResidents(t, env, facility.ID, classB.ID, "progbactive", 4, programs.Enrolled)

	// Program C: only 3 enrollees -> below the >=5 threshold, must be excluded entirely.
	enrollNResidents(t, env, facility.ID, classC.ID, "progc", 3, programs.EnrollmentCompleted)

	overview := NewRequest[programs.ProgramEngagementOverview](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/engagement-overview?facility="+strconv.Itoa(int(facility.ID)), nil).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	require.Equal(t, []programs.ProgramCompletionRank{
		{ProgramName: "Program A", Enrolled: 5, Completed: 4, CompletionRate: 80},
		{ProgramName: "Program B", Enrolled: 5, Completed: 1, CompletionRate: 20},
	}, overview.TopPrograms)
}

func TestProgramEngagementOverview_FacilityScopingIsolatesData(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facilityOne, err := env.CreateTestFacility("Scope Facility One")
	require.NoError(t, err)
	facilityTwo, err := env.CreateTestFacility("Scope Facility Two")
	require.NoError(t, err)

	programOne, err := env.CreateTestProgram("Facility One Program", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	classOne, err := env.CreateTestClass(programOne, facilityOne, programs.Active, nil)
	require.NoError(t, err)

	programTwo, err := env.CreateTestProgram("Facility Two Program", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	classTwo, err := env.CreateTestClass(programTwo, facilityTwo, programs.Active, nil)
	require.NoError(t, err)

	// Facility one: 2 residents, 1 active enrollment, 1 never-engaged.
	activeOne, err := env.CreateTestUser("factiveone", models.Student, facilityOne.ID, "")
	require.NoError(t, err)
	_, err = env.CreateTestUser("fneverone", models.Student, facilityOne.ID, "")
	require.NoError(t, err)
	_, err = env.CreateTestEnrollmentWithDates(classOne.ID, activeOne.ID, programs.Enrolled, time.Now(), nil)
	require.NoError(t, err)

	// Facility two: 5 residents, all completed -- enough to clear the top-programs
	// threshold, so if the facility filter leaks (e.g. wrong join column), this
	// program would incorrectly surface in facility one's response.
	enrollNResidents(t, env, facilityTwo.ID, classTwo.ID, "fleaktwo", 5, programs.EnrollmentCompleted)

	overview := NewRequest[programs.ProgramEngagementOverview](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/engagement-overview?facility="+strconv.Itoa(int(facilityOne.ID)), nil).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, FacilityID: facilityOne.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	require.Equal(t, int64(2), overview.TotalResidents)
	require.Equal(t, int64(1), overview.ActiveResidents)
	require.Equal(t, int64(0), overview.PreviouslyEngagedResidents)
	require.Equal(t, int64(1), overview.NeverEngagedResidents)
	require.Empty(t, overview.TopPrograms, "facility two's program must not leak into facility one's results")
}

func TestProgramEngagementOverview_AllFacilitiesAggregates(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facilityOne, err := env.CreateTestFacility("All Facilities One")
	require.NoError(t, err)
	facilityTwo, err := env.CreateTestFacility("All Facilities Two")
	require.NoError(t, err)

	programOne, err := env.CreateTestProgram("Program One", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	classOne, err := env.CreateTestClass(programOne, facilityOne, programs.Active, nil)
	require.NoError(t, err)

	programTwo, err := env.CreateTestProgram("Program Two", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	classTwo, err := env.CreateTestClass(programTwo, facilityTwo, programs.Active, nil)
	require.NoError(t, err)

	// Facility one: 6 residents -- 5 enrolled in Program One (3 completed, 2 still
	// active), plus 1 who never enrolls.
	enrollNResidents(t, env, facilityOne.ID, classOne.ID, "aone", 3, programs.EnrollmentCompleted)
	enrollNResidents(t, env, facilityOne.ID, classOne.ID, "aoneactive", 2, programs.Enrolled)
	_, err = env.CreateTestUser("aonenever", models.Student, facilityOne.ID, "")
	require.NoError(t, err)

	// Facility two: 5 residents, all completed in Program Two.
	enrollNResidents(t, env, facilityTwo.ID, classTwo.ID, "btwo", 5, programs.EnrollmentCompleted)

	overview := NewRequest[programs.ProgramEngagementOverview](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/engagement-overview?facility=all", nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, FacilityID: facilityOne.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	require.Equal(t, int64(11), overview.TotalResidents)
	// All 10 enrollments (completed + still-enrolled) were created "now", so all
	// fall within the endpoint's default 30-day lookback window and count as
	// active-in-range, regardless of current status.
	require.Equal(t, int64(10), overview.ActiveResidents)
	require.Equal(t, int64(0), overview.PreviouslyEngagedResidents)
	require.Equal(t, int64(1), overview.NeverEngagedResidents)
	require.Equal(t, []programs.ProgramCompletionRank{
		{ProgramName: "Program Two", Enrolled: 5, Completed: 5, CompletionRate: 100},
		{ProgramName: "Program One", Enrolled: 5, Completed: 3, CompletionRate: 60},
	}, overview.TopPrograms)
}
