package integration

import (
	"context"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

// program_class_enrollments carries BOTH cohort_id and class_id, and the class tier's
// "enrolled" rollup keys on class_id (classRollupsFor: WHERE e.class_id = pc.id).
//
// CreateProgramClassEnrollments sets only CohortID, relying on ProgramClassEnrollment's
// BeforeCreate hook to denormalize the parent. If that hook ever stops firing on the
// BATCH insert path -- db.Create(&slice) rather than a single struct -- class_id lands as
// 0, the INSERT still succeeds, and every class rollup silently reports 0 enrolled. No
// error, no failing type check: exactly the fail-green shape this ticket keeps producing.
//
// So this asserts the denormalization on the batch path specifically, and then asserts
// the number the UI actually reads.
func TestBatchEnrollmentDenormalizesClassID(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Rollup Facility")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Rollup Program", programs.FundingType(programs.FederalGrants),
		[]programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)
	require.NoError(t, env.SetFacilitiesToProgram(program.ID, []uint{facility.ID}))

	instructor, err := env.CreateTestInstructor(facility.ID, "rollup")
	require.NoError(t, err)

	cohort, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)
	require.NotZero(t, cohort.ClassID, "the auto-parent hook should have created a class")

	residentA, err := env.CreateTestUser("rollupresa", models.Student, facility.ID, "RA1")
	require.NoError(t, err)
	residentB, err := env.CreateTestUser("rollupresb", models.Student, facility.ID, "RB1")
	require.NoError(t, err)

	// the handler path: one call, many users, a single batch INSERT
	skipped, err := env.Programs.CreateProgramClassEnrollments(int(cohort.ID), []int{int(residentA.ID), int(residentB.ID)})
	require.NoError(t, err)
	require.Zero(t, skipped, "capacity is 10, neither resident should be skipped")

	t.Run("class_id is denormalized on every batch-inserted row", func(t *testing.T) {
		var enrollments []programs.ProgramClassEnrollment
		require.NoError(t, env.DB.Where("cohort_id = ?", cohort.ID).Find(&enrollments).Error)
		require.Len(t, enrollments, 2)

		for _, e := range enrollments {
			require.NotZero(t, e.ClassID,
				"class_id was left at 0 -- the BeforeCreate hook did not fire on the batch insert, "+
					"so every class rollup will report 0 enrolled")
			require.Equal(t, cohort.ClassID, e.ClassID,
				"class_id must be the cohort's PARENT class, not the cohort id")
		}
	})

	t.Run("the class rollup the UI reads counts them", func(t *testing.T) {
		args := &programs.QueryContext{Ctx: context.Background()}
		class, err := env.Programs.GetClassByID(int(cohort.ClassID), args)
		require.NoError(t, err)
		require.Equal(t, int64(2), class.Enrolled,
			"classRollupsFor keys on e.class_id; a 0 here means the denormalization was lost")
	})
}
