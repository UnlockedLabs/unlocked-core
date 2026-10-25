package integration

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestCreateClassHandler(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Test Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("testadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	// - [x] Create class when request is valid
	// - [ ] Create class at facility not offered is invalid
	// - [ ] Bad request when program id is invalid
	// - [ ] Internal service error when program id isn't found
	// - [x] Status conflict when program is inactive or archived

	t.Run("Create class when request is valid", func(t *testing.T) {
		runCreateClassTest(t, env, facility, facilityAdmin)
	})

	t.Run("Create class for inactive program is invalid", func(t *testing.T) {
		runCreateClassInactiveProgramTest(t, env, facility, facilityAdmin)
	})

	t.Run("Create class for archived program is invalid", func(t *testing.T) {
		runCreateClassArchivedProgramTest(t, env, facility, facilityAdmin)
	})

	t.Run("Create class at facility not offered is invalid", func(t *testing.T) {
		runCreateClassNotOfferedFacilityTest(t, env, facility, facilityAdmin)
	})
}

// successful class is created
func runCreateClassTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User) {
	program, err := env.CreateTestProgram("Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	t.Run("Create class with assigned instructor", func(t *testing.T) {
		// Create instructor user (using FacilityAdmin role as instructors should be privileged)
		instructor, err := env.CreateTestInstructor(facility.ID, "1")
		require.NoError(t, err)

		// Create class with instructor
		class := newClassWithInstructor(program, facility, &instructor.ID)

		resp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class).
			WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
			Do().
			ExpectStatus(http.StatusCreated)

		got := resp.GetData()

		require.NotZero(t, got.ID)
		require.Equal(t, class.InstructorID, got.InstructorID)
		require.Equal(t, class.Description, got.Description)
		require.WithinDuration(t, class.StartDt, got.StartDt, time.Millisecond)
		require.WithinDuration(t, *class.EndDt, *got.EndDt, time.Millisecond)
		require.Equal(t, class.Status, got.Status)
		require.Equal(t, class.CreditHours, got.CreditHours)
	})
}

func runCreateClassInactiveProgramTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User) {
	program, err := env.CreateTestProgram("Inactive Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, false, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "inactive")
	require.NoError(t, err)

	class := newClassWithInstructor(program, facility, &instructor.ID)

	NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID}).
		Do().
		ExpectStatus(http.StatusUnauthorized)
}

func runCreateClassArchivedProgramTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User) {
	archivedAt := time.Date(2022, 12, 1, 0, 0, 0, 0, time.UTC)
	program, err := env.CreateTestProgram("Archived Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, &archivedAt)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "archived")
	require.NoError(t, err)

	class := newClassWithInstructor(program, facility, &instructor.ID)

	NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusUnauthorized)
}

func runCreateClassNotOfferedFacilityTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User) {
	program, err := env.CreateTestProgram("Not Offered at Facility", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "notoffered")
	require.NoError(t, err)

	class := newClassWithInstructor(program, facility, &instructor.ID)

	NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusUnauthorized)
}

// A department admin (statewide) must create a class at the facility named by
// the ?facility_id= query param, not at their home facility or whatever the body
// carries. This guards the toggle-removal re-scoping.
func TestCreateClassFacilityScopingForDeptAdmin(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	homeFacility, err := env.CreateTestFacility("Home Facility")
	require.NoError(t, err)
	targetFacility, err := env.CreateTestFacility("Target Facility")
	require.NoError(t, err)

	deptAdmin, err := env.CreateTestUser("deptadmin", models.DepartmentAdmin, homeFacility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Scoped Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)
	// Offered only at the target facility, not the admin's home facility.
	err = env.SetFacilitiesToProgram(program.ID, []uint{targetFacility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(targetFacility.ID, "target")
	require.NoError(t, err)

	t.Run("class is created at the query-param facility, not the admin's home", func(t *testing.T) {
		// Body intentionally carries the home facility to prove it is ignored.
		class := newClassWithInstructor(program, homeFacility, &instructor.ID)

		resp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost,
			fmt.Sprintf("/api/programs/%d/classes?facility_id=%d", program.ID, targetFacility.ID), class).
			WithTestClaims(&handlers.Claims{Role: models.DepartmentAdmin, UserID: deptAdmin.ID, FacilityID: homeFacility.ID}).
			Do().
			ExpectStatus(http.StatusCreated)

		got := resp.GetData()
		require.Equal(t, targetFacility.ID, got.FacilityID)
	})

	t.Run("missing facility_id is rejected for a statewide admin", func(t *testing.T) {
		class := newClassWithInstructor(program, targetFacility, &instructor.ID)

		NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost,
			fmt.Sprintf("/api/programs/%d/classes", program.ID), class).
			WithTestClaims(&handlers.Claims{Role: models.DepartmentAdmin, UserID: deptAdmin.ID, FacilityID: homeFacility.ID}).
			Do().
			ExpectStatus(http.StatusUnauthorized)
	})
}

// creates a boilerplate class with required instructor
func newClassWithInstructor(program *programs.Program, facility *models.Facility, instructorID *uint) programs.ProgramClassCohort {
	endDt := time.Now().Add(time.Hour * 24)
	creditHours := int64(2)

	class := programs.ProgramClassCohort{
		ProgramID:    program.ID,
		FacilityID:   facility.ID,
		Capacity:     10,
		InstructorID: instructorID,
		Description:  "This is a test class created for integration testing purposes.",
		StartDt:      time.Now(),
		EndDt:        &endDt,
		Status:       programs.Scheduled,
		CreditHours:  &creditHours,
	}
	return class
}

func TestUpdateClasses(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Test Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("testadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	// Create program and make it available at facility
	program, err := env.CreateTestProgram("Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	t.Run("Update class from Scheduled to Active sets enrolled_at for existing enrollments", func(t *testing.T) {
		runUpdateClassScheduledToActiveTest(t, env, facility, facilityAdmin, program)
	})

	t.Run("Update Active class to Cancelled sets enrollment_ended_at for existing enrollments", func(t *testing.T) {
		runUpdateActiveClassToCancelledTest(t, env, facility, facilityAdmin, program)
	})

	t.Run("Update Paused class to Cancelled sets enrollment_ended_at for existing enrollments", func(t *testing.T) {
		runUpdatePausedClassToCancelledTest(t, env, facility, facilityAdmin, program)
	})

	t.Run("Update Active to Paused to Active preserves original enrolled_at timestamps", func(t *testing.T) {
		runUpdateActivePausedActiveTest(t, env, facility, facilityAdmin, program)
	})
	t.Run("Update class from Active to Completed", func(t *testing.T) {
		runUpdateActiveClassToCompletedTest(t, env, facility, facilityAdmin)
	})
}

func runUpdateClassScheduledToActiveTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program) {
	instructor, err := env.CreateTestInstructor(facility.ID, "scheduled")
	require.NoError(t, err)

	// Create a scheduled class using helper function
	class, err := env.CreateTestClass(program, facility, programs.Scheduled, &instructor.ID)
	require.NoError(t, err)

	// Create test users
	user1, err := env.CreateTestUser("testuser1", models.Student, facility.ID, "123")
	require.NoError(t, err)
	user2, err := env.CreateTestUser("testuser2", models.Student, facility.ID, "456")
	require.NoError(t, err)

	// Create enrollments using helper function
	enrollment1, err := env.CreateTestEnrollment(class.ID, user1.ID, programs.Enrolled)
	require.NoError(t, err)
	enrollment2, err := env.CreateTestEnrollment(class.ID, user2.ID, programs.Enrolled)
	require.NoError(t, err)

	// Verify enrollments start with enrolled_at = NULL
	enrolledAt1, endedAt1, err := env.GetEnrollmentTimestamps(enrollment1.ID)
	require.NoError(t, err)
	require.Nil(t, enrolledAt1, "enrolled_at should be NULL for scheduled class")
	require.Nil(t, endedAt1, "enrollment_ended_at should be NULL initially")

	enrolledAt2, endedAt2, err := env.GetEnrollmentTimestamps(enrollment2.ID)
	require.NoError(t, err)
	require.Nil(t, enrolledAt2, "enrolled_at should be NULL for scheduled class")
	require.Nil(t, endedAt2, "enrollment_ended_at should be NULL initially")

	// Update class status to Active via API using the bulk update endpoint
	updateData := map[string]any{
		"status": string(programs.Active),
	}

	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", class.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	// Verify enrolled_at timestamps were set
	enrolledAt1After, endedAt1After, err := env.GetEnrollmentTimestamps(enrollment1.ID)
	require.NoError(t, err)
	require.NotNil(t, enrolledAt1After, "enrolled_at should be set when class becomes Active")
	require.WithinDuration(t, time.Now(), *enrolledAt1After, time.Second*5, "enrolled_at should be recent")
	require.Nil(t, endedAt1After, "enrollment_ended_at should still be NULL")

	enrolledAt2After, endedAt2After, err := env.GetEnrollmentTimestamps(enrollment2.ID)
	require.NoError(t, err)
	require.NotNil(t, enrolledAt2After, "enrolled_at should be set when class becomes Active")
	require.WithinDuration(t, time.Now(), *enrolledAt2After, time.Second*5, "enrolled_at should be recent")
	require.Nil(t, endedAt2After, "enrollment_ended_at should still be NULL")
}

func runUpdateActiveClassToCancelledTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program) {
	instructor, err := env.CreateTestInstructor(facility.ID, "active")
	require.NoError(t, err)

	// Create an Active class
	class, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	_, err = env.CreateTestEvent(class.ID, "", instructor.ID)
	require.NoError(t, err)

	// Create test users
	user1, err := env.CreateTestUser("activeuser1", models.Student, facility.ID, "789")
	require.NoError(t, err)
	user2, err := env.CreateTestUser("activeuser2", models.Student, facility.ID, "790")
	require.NoError(t, err)

	// Create enrollments in Active class (should set enrolled_at immediately)
	enrollment1, err := env.CreateTestEnrollment(class.ID, user1.ID, programs.Enrolled)
	require.NoError(t, err)
	enrollment2, err := env.CreateTestEnrollment(class.ID, user2.ID, programs.Enrolled)
	require.NoError(t, err)

	// Verify initial state: enrolled_at set, enrollment_ended_at NULL
	enrolledAt1, endedAt1, err := env.GetEnrollmentTimestamps(enrollment1.ID)
	require.NoError(t, err)
	require.NotNil(t, enrolledAt1, "enrolled_at should be set for Active class")
	require.Nil(t, endedAt1, "enrollment_ended_at should be NULL initially")

	enrolledAt2, endedAt2, err := env.GetEnrollmentTimestamps(enrollment2.ID)
	require.NoError(t, err)
	require.NotNil(t, enrolledAt2, "enrolled_at should be set for Active class")
	require.Nil(t, endedAt2, "enrollment_ended_at should be NULL initially")

	// Update class status to Cancelled via API
	updateData := map[string]any{
		"status": string(programs.Cancelled),
	}

	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", class.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	// Verify enrollment statuses were updated and enrollment_ended_at was set
	var updatedEnrollments []programs.ProgramClassEnrollment
	err = env.DB.Where("cohort_id = ?", class.ID).Find(&updatedEnrollments).Error
	require.NoError(t, err)
	require.Len(t, updatedEnrollments, 2)

	for _, enrollment := range updatedEnrollments {
		require.Equal(t, programs.EnrollmentCancelled, enrollment.EnrollmentStatus, "enrollment should be cancelled when class is cancelled")
		require.NotNil(t, enrollment.EnrolledAt, "enrolled_at should remain set")
		require.NotNil(t, enrollment.EnrollmentEndedAt, "enrollment_ended_at should be set when class is cancelled")
		require.WithinDuration(t, time.Now(), *enrollment.EnrollmentEndedAt, time.Second*5, "enrollment_ended_at should be recent")
	}
}

func runUpdatePausedClassToCancelledTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program) {
	instructor, err := env.CreateTestInstructor(facility.ID, "paused")
	require.NoError(t, err)

	// Create an Active class first
	class, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	_, err = env.CreateTestEvent(class.ID, "", instructor.ID)
	require.NoError(t, err)

	// Create test users
	user1, err := env.CreateTestUser("pauseduser1", models.Student, facility.ID, "791")
	require.NoError(t, err)
	user2, err := env.CreateTestUser("pauseduser2", models.Student, facility.ID, "792")
	require.NoError(t, err)

	// Create enrollments in Active class (should set enrolled_at immediately)
	enrollment1, err := env.CreateTestEnrollment(class.ID, user1.ID, programs.Enrolled)
	require.NoError(t, err)
	enrollment2, err := env.CreateTestEnrollment(class.ID, user2.ID, programs.Enrolled)
	require.NoError(t, err)

	// Verify initial state: enrolled_at set, enrollment_ended_at NULL
	enrolledAt1, endedAt1, err := env.GetEnrollmentTimestamps(enrollment1.ID)
	require.NoError(t, err)
	require.NotNil(t, enrolledAt1, "enrolled_at should be set for Active class")
	require.Nil(t, endedAt1, "enrollment_ended_at should be NULL initially")

	enrolledAt2, endedAt2, err := env.GetEnrollmentTimestamps(enrollment2.ID)
	require.NoError(t, err)
	require.NotNil(t, enrolledAt2, "enrolled_at should be set for Active class")
	require.Nil(t, endedAt2, "enrollment_ended_at should be NULL initially")

	// Update class status from Active to Paused via API
	updateData := map[string]any{
		"status": string(programs.Paused),
	}

	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", class.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	// Update class status from Paused to Cancelled via API
	updateData = map[string]any{
		"status": string(programs.Cancelled),
	}

	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", class.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	// Verify enrollment statuses were updated and enrollment_ended_at was set
	var updatedEnrollments []programs.ProgramClassEnrollment
	err = env.DB.Where("cohort_id = ?", class.ID).Find(&updatedEnrollments).Error
	require.NoError(t, err)
	require.Len(t, updatedEnrollments, 2)

	for _, enrollment := range updatedEnrollments {
		require.Equal(t, programs.EnrollmentCancelled, enrollment.EnrollmentStatus, "enrollment should be cancelled when class is cancelled")
		require.NotNil(t, enrollment.EnrolledAt, "enrolled_at should remain set since class was previously Active")
		require.NotNil(t, enrollment.EnrollmentEndedAt, "enrollment_ended_at should be set when class is cancelled")
		require.WithinDuration(t, time.Now(), *enrollment.EnrollmentEndedAt, time.Second*5, "enrollment_ended_at should be recent")
	}
}

func runUpdateActivePausedActiveTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program) {
	instructor, err := env.CreateTestInstructor(facility.ID, "activepaused")
	require.NoError(t, err)

	// Create an Active class
	class, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	// Create test user and enrollment
	user, err := env.CreateTestUser("activepauseduser", models.Student, facility.ID, "793")
	require.NoError(t, err)

	enrollment, err := env.CreateTestEnrollment(class.ID, user.ID, programs.Enrolled)
	require.NoError(t, err)

	// Capture original enrolled_at timestamp
	originalEnrolledAt, endedAt, err := env.GetEnrollmentTimestamps(enrollment.ID)
	require.NoError(t, err)
	require.NotNil(t, originalEnrolledAt, "enrolled_at should be set for Active class")
	require.Nil(t, endedAt, "enrollment_ended_at should be NULL initially")

	// Wait to ensure any new timestamps would be different
	time.Sleep(time.Millisecond * 50)

	// Update class status from Active to Paused
	updateData := map[string]interface{}{
		"status": string(programs.Paused),
	}

	NewRequest[interface{}](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", class.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	// Verify enrolled_at timestamp remains unchanged after pausing
	enrolledAtAfterPause, endedAtAfterPause, err := env.GetEnrollmentTimestamps(enrollment.ID)
	require.NoError(t, err)
	require.Equal(t, originalEnrolledAt, enrolledAtAfterPause, "enrolled_at should not change when pausing")
	require.Nil(t, endedAtAfterPause, "enrollment_ended_at should still be NULL after pausing")

	// Update class status from Paused back to Active
	updateData = map[string]interface{}{
		"status": string(programs.Active),
	}

	NewRequest[interface{}](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", class.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	// Verify enrolled_at timestamp STILL remains unchanged (not updated to new timestamp)
	enrolledAtFinal, endedAtFinal, err := env.GetEnrollmentTimestamps(enrollment.ID)
	require.NoError(t, err)
	require.Equal(t, originalEnrolledAt, enrolledAtFinal, "enrolled_at should preserve original timestamp when going Paused->Active")
	require.Nil(t, endedAtFinal, "enrollment_ended_at should still be NULL after reactivating")
}

// When a class is marked Completed:
// - All enrollments become Completed
// - A ClassCompletion record is created for each affected student

func runUpdateActiveClassToCompletedTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User) {
	// Create program and make it available at facility
	program, err := env.CreateTestProgram("Active Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "completed")
	require.NoError(t, err)

	// Create an Active class
	class, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	_, err = env.CreateTestEvent(class.ID, "", instructor.ID)
	require.NoError(t, err)

	// Create test users
	user1, err := env.CreateTestUser("student1", models.Student, facility.ID, "794")
	require.NoError(t, err)
	user2, err := env.CreateTestUser("student2", models.Student, facility.ID, "795")
	require.NoError(t, err)
	// Create enrollments
	_, err = env.CreateTestEnrollment(class.ID, user1.ID, programs.Enrolled)
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(class.ID, user2.ID, programs.Enrolled)
	require.NoError(t, err)

	var preUpdateEnrollments []programs.ProgramClassEnrollment
	err = env.DB.Where("cohort_id = ? AND user_id in (?, ?)", class.ID, user1.ID, user2.ID).Find(&preUpdateEnrollments).Error
	require.NoError(t, err)

	for _, enrollment := range preUpdateEnrollments {
		require.Equal(t, programs.Enrolled, enrollment.EnrollmentStatus, "enrollment status should be Enrolled")
	}

	var preUpdateEnrollmentsCount int64
	err = env.DB.Model(&programs.ProgramClassEnrollment{}).
		Where("cohort_id = ? AND enrollment_status = ?", class.ID, programs.Enrolled).
		Count(&preUpdateEnrollmentsCount).Error
	require.NoError(t, err)

	updateData := map[string]any{
		"status": string(programs.Completed),
	}

	// Call to /api/program-classes API endpoint with updateData as the request payload
	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", class.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	// Exactly 2 enrollments are found for the users in the class with an EnrollmentStatus of EnrollmentCompleted
	var afterUpdateEnrollments []programs.ProgramClassEnrollment
	err = env.DB.Where("cohort_id = ? AND user_id in (?, ?)", class.ID, user1.ID, user2.ID).Find(&afterUpdateEnrollments).Error
	require.NoError(t, err)
	require.Len(t, afterUpdateEnrollments, 2)

	for _, enrollment := range afterUpdateEnrollments {
		require.Equal(t, programs.EnrollmentCompleted, enrollment.EnrollmentStatus, "enrollment status should be Completed")
	}

	// all  enrollments with the same class.ID should be completed
	var afterUpdateEnrollmentsCount int64
	err = env.DB.Model(&programs.ProgramClassEnrollment{}).
		Where("cohort_id = ? AND enrollment_status = ?", class.ID, programs.Enrolled).
		Count(&afterUpdateEnrollmentsCount).Error
	require.NoError(t, err)
	require.Equal(t, 0, int(afterUpdateEnrollmentsCount), "records with enrollment_status of Enrolled should update to Completed so the count should be 0")

	// query the program_completion table for records with a program_class_id matching the ID of the updated class.
	var programCompletions []programs.ClassCompletion
	err = env.DB.
		Where("user_id IN (?, ?)", user1.ID, user2.ID).
		Where("cohort_id = ?", class.ID).
		Find(&programCompletions).Error
	require.NoError(t, err)

	// Check if the same number of program completion records were created as the students enrolled (this is the expected behavior)
	require.Len(
		t,
		programCompletions,
		int(preUpdateEnrollmentsCount),
		"number of program completions (%d) should be equal to the previous number of enrollments (%d)",
		len(programCompletions),
		preUpdateEnrollmentsCount,
	)
}

func TestMain(m *testing.M) {
	logrus.SetOutput(io.Discard)
	// Run in UTC, as CI does, so results that depend on the server's local
	// time zone are the same on every machine.
	time.Local = time.UTC
	code := m.Run()
	os.Exit(code)
}
