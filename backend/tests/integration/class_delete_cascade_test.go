package integration

import (
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

// Deleting the last cohort of a class must take the class with it, or the class tier
// accumulates rows no screen can reach: a class with no runs still appears in the create
// form's class dropdown and still occupies its name.
//
// The class is SOFT deleted, so these assertions must use Unscoped() to tell "hidden"
// apart from "gone" -- a plain First() cannot distinguish them.
func TestDeleteClassRemovesOrphanedParent(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Cascade Facility")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Cascade Program", programs.FundingType(programs.FederalGrants),
		[]programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)
	require.NoError(t, env.SetFacilitiesToProgram(program.ID, []uint{facility.ID}))

	instructor, err := env.CreateTestInstructor(facility.ID, "cascade")
	require.NoError(t, err)

	t.Run("last cohort deletes its parent class", func(t *testing.T) {
		cohort, err := env.CreateTestClass(program, facility, programs.Scheduled, &instructor.ID)
		require.NoError(t, err)
		classID := cohort.ClassID
		require.NotZero(t, classID, "the auto-parent hook should have created a class")

		require.NoError(t, env.Programs.DeleteClass(int(cohort.ID)))

		var visible programs.ProgramClass
		err = env.DB.Where("id = ?", classID).First(&visible).Error
		require.Error(t, err, "class should no longer be visible once its last cohort is gone")

		var row programs.ProgramClass
		require.NoError(t, env.DB.Unscoped().Where("id = ?", classID).First(&row).Error,
			"class must be SOFT deleted, not hard deleted -- FKs point at this row")
		require.True(t, row.DeletedAt.Valid, "deleted_at should be set on the parent class")
	})

	t.Run("a class with another cohort is left alone", func(t *testing.T) {
		keep, err := env.CreateTestClass(program, facility, programs.Scheduled, &instructor.ID)
		require.NoError(t, err)
		classID := keep.ClassID

		// A second cohort under the SAME class -- this is what a multi-cohort class is.
		sibling := &programs.ProgramClassCohort{
			ClassID:      classID,
			ProgramID:    program.ID,
			FacilityID:   facility.ID,
			Capacity:     10,
			InstructorID: &instructor.ID,
			Description:  "sibling cohort",
			StartDt:      keep.StartDt,
			Status:       programs.Scheduled,
		}
		require.NoError(t, env.DB.Create(sibling).Error)

		require.NoError(t, env.Programs.DeleteClass(int(keep.ID)))

		var row programs.ProgramClass
		require.NoError(t, env.DB.Where("id = ?", classID).First(&row).Error,
			"class must survive while a sibling cohort still runs under it")

		// ...and removing that sibling too now takes the class.
		require.NoError(t, env.Programs.DeleteClass(int(sibling.ID)))
		require.Error(t, env.DB.Where("id = ?", classID).First(&programs.ProgramClass{}).Error,
			"class should go once its final cohort is deleted")
	})

	t.Run("a class still holding a certificate is left alone", func(t *testing.T) {
		cohort, err := env.CreateTestClass(program, facility, programs.Scheduled, &instructor.ID)
		require.NoError(t, err)
		classID := cohort.ClassID

		student, err := env.CreateTestUser("cascadestudent", models.Student, facility.ID, "CAS001")
		require.NoError(t, err)

		// A certificate earned under this class, with no live cohort of its own -- the
		// shape left behind when a sibling cohort was deleted earlier. Hiding the class
		// would orphan the certificate's class link.
		completion := programs.ClassCompletion{
			UserID:       student.ID,
			ClassID:      &classID,
			FacilityName: facility.Name,
			CreditType:   "Completion",
			AdminEmail:   "admin@example.com",
			ProgramOwner: "owner",
			ProgramName:  program.Name,
			ClassName:    "Cascade Class",
		}
		require.NoError(t, env.DB.Create(&completion).Error)

		require.NoError(t, env.Programs.DeleteClass(int(cohort.ID)))

		var row programs.ProgramClass
		require.NoError(t, env.DB.Where("id = ?", classID).First(&row).Error,
			"a class referenced by a live certificate must not be removed")
	})
}
