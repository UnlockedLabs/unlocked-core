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

func TestProgramLoadDistribution(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Load Facility")
	require.NoError(t, err)

	progA, err := env.CreateTestProgram("Program A", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	progB, err := env.CreateTestProgram("Program B", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Vocational}}, nil, true, nil)
	require.NoError(t, err)
	classA, err := env.CreateTestClass(progA, facility, programs.Active, nil)
	require.NoError(t, err)
	classB, err := env.CreateTestClass(progB, facility, programs.Active, nil)
	require.NoError(t, err)

	zeroUser, err := env.CreateTestUser("loadzero", models.Student, facility.ID, "")
	require.NoError(t, err)
	_ = zeroUser
	oneUser, err := env.CreateTestUser("loadone", models.Student, facility.ID, "")
	require.NoError(t, err)
	twoUser, err := env.CreateTestUser("loadtwo", models.Student, facility.ID, "")
	require.NoError(t, err)

	_, err = env.CreateTestEnrollment(classA.ID, oneUser.ID, programs.Enrolled)
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(classA.ID, twoUser.ID, programs.Enrolled)
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(classB.ID, twoUser.ID, programs.Enrolled)
	require.NoError(t, err)

	dist := NewRequest[programs.ProgramLoadDistribution](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/load-distribution?facility="+strconv.Itoa(int(facility.ID)), nil).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	byBucket := map[string]int64{}
	for _, b := range dist.Statewide {
		byBucket[b.Bucket] = b.Count
	}
	require.Equal(t, int64(1), byBucket["0"])
	require.Equal(t, int64(1), byBucket["1"])
	require.Equal(t, int64(1), byBucket["2"])

	require.Len(t, dist.ByFacility, 1)
	row := dist.ByFacility[0]
	require.Equal(t, "Load Facility", row.FacilityName)
	require.Equal(t, int64(1), row.Zero)
	require.Equal(t, int64(1), row.One)
	require.Equal(t, int64(1), row.Two)
	require.Equal(t, int64(3), row.Total)
}

// TestProgramLoadDistributionAllFacilities exercises the facilityID == nil
// (statewide) path, which is this endpoint's highest-risk branch: the
// resident population and by-facility attribution are scoped by each
// resident's HOME facility (u.facility_id), while the active-enrollment
// COUNT per resident is joined through the enrollment's cohort facility
// (pc.facility_id). This test deliberately enrolls a resident whose home
// facility differs from the cohort facility of the class they're enrolled
// in, to prove the by-facility breakdown attributes them to their home
// facility (not the cohort's facility) even when no facility filter is
// applied to the counts query.
func TestProgramLoadDistributionAllFacilities(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facilityOne, err := env.CreateTestFacility("Facility One")
	require.NoError(t, err)
	facilityTwo, err := env.CreateTestFacility("Facility Two")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Cross Facility Program", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	class, err := env.CreateTestClass(program, facilityOne, programs.Active, nil)
	require.NoError(t, err)

	homeUser, err := env.CreateTestUser("loadhomeone", models.Student, facilityOne.ID, "")
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(class.ID, homeUser.ID, programs.Enrolled)
	require.NoError(t, err)

	crossUser, err := env.CreateTestUser("loadcrosstwo", models.Student, facilityTwo.ID, "")
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(class.ID, crossUser.ID, programs.Enrolled)
	require.NoError(t, err)

	idleUser, err := env.CreateTestUser("loadidletwo", models.Student, facilityTwo.ID, "")
	require.NoError(t, err)
	_ = idleUser

	dist := NewRequest[programs.ProgramLoadDistribution](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/load-distribution?facility=all", nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, FacilityID: facilityOne.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	byBucket := map[string]int64{}
	for _, b := range dist.Statewide {
		byBucket[b.Bucket] = b.Count
	}
	require.Equal(t, int64(1), byBucket["0"])
	require.Equal(t, int64(2), byBucket["1"])

	require.Len(t, dist.ByFacility, 2)
	byName := map[string]programs.ProgramLoadFacilityRow{}
	for _, row := range dist.ByFacility {
		byName[row.FacilityName] = row
	}

	one := byName["Facility One"]
	require.Equal(t, int64(0), one.Zero)
	require.Equal(t, int64(1), one.One)
	require.Equal(t, int64(1), one.Total)

	two := byName["Facility Two"]
	require.Equal(t, int64(1), two.Zero)
	require.Equal(t, int64(1), two.One)
	require.Equal(t, int64(2), two.Total)
}
