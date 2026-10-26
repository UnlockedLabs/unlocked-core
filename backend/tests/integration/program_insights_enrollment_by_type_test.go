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

// SystemAdmin can switch facilities, so getQueryContext defaults to
// statewide (facility_id 0) unless a facility is named explicitly - pass it
// via the "facility" param that resolveFacilityFilter reads. Same gotcha as
// the second-enrollment and completion-matrix tests.
func TestEnrollmentByProgramType(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Type Facility")
	require.NoError(t, err)

	progA, err := env.CreateTestProgram("GED Prep", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	progB, err := env.CreateTestProgram("Welding", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Vocational}}, nil, true, nil)
	require.NoError(t, err)
	classA, err := env.CreateTestClass(progA, facility, programs.Active, nil)
	require.NoError(t, err)
	classB, err := env.CreateTestClass(progB, facility, programs.Active, nil)
	require.NoError(t, err)

	u1, err := env.CreateTestUser("typeres1", models.Student, facility.ID, "")
	require.NoError(t, err)
	u2, err := env.CreateTestUser("typeres2", models.Student, facility.ID, "")
	require.NoError(t, err)

	_, err = env.CreateTestEnrollment(classA.ID, u1.ID, programs.EnrollmentCompleted)
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(classA.ID, u2.ID, programs.Enrolled)
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(classB.ID, u1.ID, programs.Enrolled)
	require.NoError(t, err)

	rows := NewRequest[[]programs.ProgramTypeEnrollment](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/enrollment-by-type?facility="+strconv.Itoa(int(facility.ID)), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	byType := map[string]programs.ProgramTypeEnrollment{}
	for _, row := range rows {
		byType[row.ProgramType] = row
	}
	require.Equal(t, int64(2), byType["Educational"].Enrolled)
	require.Equal(t, int64(1), byType["Educational"].Completed)
	require.Equal(t, int64(1), byType["Vocational"].Enrolled)
	require.Equal(t, int64(0), byType["Vocational"].Completed)
}

// TestEnrollmentByProgramTypeAllFacilities exercises the facilityID == nil
// (statewide) path, which is this endpoint's primary/default use case per
// its name - not an edge case.
func TestEnrollmentByProgramTypeAllFacilities(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facilityOne, err := env.CreateTestFacility("Type Facility One")
	require.NoError(t, err)
	facilityTwo, err := env.CreateTestFacility("Type Facility Two")
	require.NoError(t, err)

	prog, err := env.CreateTestProgram("GED Prep", programs.StateGrants,
		[]programs.ProgramType{{ProgramType: programs.Educational}}, nil, true, nil)
	require.NoError(t, err)
	classOne, err := env.CreateTestClass(prog, facilityOne, programs.Active, nil)
	require.NoError(t, err)
	classTwo, err := env.CreateTestClass(prog, facilityTwo, programs.Active, nil)
	require.NoError(t, err)

	u1, err := env.CreateTestUser("alltyperes1", models.Student, facilityOne.ID, "")
	require.NoError(t, err)
	u2, err := env.CreateTestUser("alltyperes2", models.Student, facilityTwo.ID, "")
	require.NoError(t, err)

	_, err = env.CreateTestEnrollment(classOne.ID, u1.ID, programs.EnrollmentCompleted)
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(classTwo.ID, u2.ID, programs.Enrolled)
	require.NoError(t, err)

	rows := NewRequest[[]programs.ProgramTypeEnrollment](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/enrollment-by-type?facility=all", nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, FacilityID: facilityOne.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	require.Len(t, rows, 1)
	require.Equal(t, "Educational", rows[0].ProgramType)
	require.Equal(t, int64(2), rows[0].Enrolled)
	require.Equal(t, int64(1), rows[0].Completed)
	require.InDelta(t, 50.0, rows[0].Rate, 0.01)
}

// TestEnrollmentByProgramTypeCapping exercises the top-4 + "Other" capping
// logic: with 6 distinct program types enrolled, only the 4 highest-
// enrollment individual types are returned as-is, and the remaining 2 are
// rolled up into a single "Other" bucket whose enrolled/completed counts are
// the sum of the rolled-up types and whose rate is recomputed from those sums.
func TestEnrollmentByProgramTypeCapping(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Capping Facility")
	require.NoError(t, err)

	allTypes := []programs.ProgType{
		programs.Educational, programs.Vocational, programs.MentalHealth,
		programs.Religious, programs.ReEntry, programs.Therapeutic,
	}

	type classInfo struct {
		class     *programs.ProgramClassCohort
		enrollees int
		completes int
	}
	// Give each type a distinct enrollment count so ranking is unambiguous:
	// Educational=6, Vocational=5, MentalHealth=4, Religious=3, ReEntry=2, Therapeutic=1.
	// ReEntry and Therapeutic (the two rolled into "Other") are given
	// deliberately different individual rates - ReEntry 2/2=100%,
	// Therapeutic 1/0=0% - so a buggy average-of-rates implementation
	// ((100+0)/2=50%) would diverge from the correct sum-then-recompute
	// result (2/3=66.67%) and the assertion below would catch it.
	counts := []int{6, 5, 4, 3, 2, 1}
	completes := []int{3, 2, 1, 1, 2, 0}

	classes := make([]classInfo, 0, len(allTypes))
	for i, pt := range allTypes {
		prog, err := env.CreateTestProgram(string(pt)+" Program", programs.StateGrants,
			[]programs.ProgramType{{ProgramType: pt}}, nil, true, nil)
		require.NoError(t, err)
		class, err := env.CreateTestClass(prog, facility, programs.Active, nil)
		require.NoError(t, err)
		classes = append(classes, classInfo{class: class, enrollees: counts[i], completes: completes[i]})
	}

	userN := 0
	for _, ci := range classes {
		for i := 0; i < ci.enrollees; i++ {
			userN++
			user, err := env.CreateTestUser("capres"+strconv.Itoa(userN), models.Student, facility.ID, "")
			require.NoError(t, err)
			status := programs.Enrolled
			if i < ci.completes {
				status = programs.EnrollmentCompleted
			}
			_, err = env.CreateTestEnrollment(ci.class.ID, user.ID, status)
			require.NoError(t, err)
		}
	}

	rows := NewRequest[[]programs.ProgramTypeEnrollment](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/enrollment-by-type?facility="+strconv.Itoa(int(facility.ID)), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	require.Len(t, rows, 5)

	byType := map[string]programs.ProgramTypeEnrollment{}
	for _, row := range rows {
		byType[row.ProgramType] = row
	}

	// Top 4 by enrollment: Educational(6), Vocational(5), Mental Health/Behavioral(4), Religious/Faith-Based(3).
	require.Equal(t, int64(6), byType["Educational"].Enrolled)
	require.Equal(t, int64(5), byType["Vocational"].Enrolled)
	require.Equal(t, int64(4), byType["Mental Health/Behavioral"].Enrolled)
	require.Equal(t, int64(3), byType["Religious/Faith-Based"].Enrolled)

	// Rolled up: ReEntry(2 enrolled, 2 completed) + Therapeutic(1 enrolled, 0 completed).
	// Sum-then-recompute gives 2/3*100=66.67%; a buggy average-of-rates
	// would give (100+0)/2=50%, so this assertion distinguishes the two.
	other, ok := byType["Other"]
	require.True(t, ok, "expected an Other bucket for the rolled-up remainder")
	require.Equal(t, int64(3), other.Enrolled)
	require.Equal(t, int64(2), other.Completed)
	require.InDelta(t, 66.67, other.Rate, 0.01)
}

// TestEnrollmentByProgramTypeNoCappingAtBoundary exercises the len(all) <=
// maxIndividualProgramTypes boundary with exactly 4 program types: no
// capping should occur and no "Other" row should appear.
func TestEnrollmentByProgramTypeNoCappingAtBoundary(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Boundary Facility")
	require.NoError(t, err)

	fourTypes := []programs.ProgType{
		programs.Educational, programs.Vocational, programs.MentalHealth, programs.Religious,
	}
	for i, pt := range fourTypes {
		prog, err := env.CreateTestProgram(string(pt)+" Program", programs.StateGrants,
			[]programs.ProgramType{{ProgramType: pt}}, nil, true, nil)
		require.NoError(t, err)
		class, err := env.CreateTestClass(prog, facility, programs.Active, nil)
		require.NoError(t, err)
		user, err := env.CreateTestUser("boundres"+strconv.Itoa(i), models.Student, facility.ID, "")
		require.NoError(t, err)
		_, err = env.CreateTestEnrollment(class.ID, user.ID, programs.Enrolled)
		require.NoError(t, err)
	}

	rows := NewRequest[[]programs.ProgramTypeEnrollment](env.Client, t, http.MethodGet,
		"/api/department-metrics/programs/enrollment-by-type?facility="+strconv.Itoa(int(facility.ID)), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	require.Len(t, rows, 4)
	for _, row := range rows {
		require.NotEqual(t, "Other", row.ProgramType)
	}
}
