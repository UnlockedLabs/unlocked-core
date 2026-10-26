package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

func TestEnrollUsersInClass(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Test Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("testadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	// Create program and make it available at facility
	program, err := env.CreateTestProgram("Active Class Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	t.Run("Enroll users in Active class sets enrolled_at immediately", func(t *testing.T) {
		runEnrollInActiveClassTest(t, env, facility, facilityAdmin, program)
	})

	t.Run("Enroll users in Scheduled class leaves enrolled_at NULL", func(t *testing.T) {
		runEnrollInScheduledClassTest(t, env, facility, facilityAdmin, program)
	})
}

func runEnrollInActiveClassTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program) {
	instructor, err := env.CreateTestInstructor(facility.ID, "enrollactive")
	require.NoError(t, err)

	// Create an Active class
	activeClass, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	// Create test users
	user1, err := env.CreateTestUser("activeuser1", models.Student, facility.ID, "111")
	require.NoError(t, err)
	user2, err := env.CreateTestUser("activeuser2", models.Student, facility.ID, "222")
	require.NoError(t, err)

	// Enroll users via API endpoint
	enrollmentData := map[string]any{
		"user_ids": []int{int(user1.ID), int(user2.ID)},
	}

	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/enrollments", activeClass.ID), enrollmentData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	// Verify enrolled_at timestamps were set immediately for Active class
	var enrollments []programs.ProgramClassEnrollment
	err = env.DB.Where("cohort_id = ?", activeClass.ID).Find(&enrollments).Error
	require.NoError(t, err)
	require.Len(t, enrollments, 2, "Should have 2 enrollments")

	for _, enrollment := range enrollments {
		require.NotNil(t, enrollment.EnrolledAt, "enrolled_at should be set immediately for Active class")
		require.WithinDuration(t, time.Now(), *enrollment.EnrolledAt, time.Second*5, "enrolled_at should be recent")
		require.Nil(t, enrollment.EnrollmentEndedAt, "enrollment_ended_at should be NULL initially")
		require.Equal(t, programs.Enrolled, enrollment.EnrollmentStatus, "status should be Enrolled")
	}
}

func runEnrollInScheduledClassTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program) {
	instructor, err := env.CreateTestInstructor(facility.ID, "enrollscheduled")
	require.NoError(t, err)

	// Create a Scheduled class
	scheduledClass, err := env.CreateTestClass(program, facility, programs.Scheduled, &instructor.ID)
	require.NoError(t, err)

	// Create test users
	user1, err := env.CreateTestUser("scheduleduser1", models.Student, facility.ID, "333")
	require.NoError(t, err)
	user2, err := env.CreateTestUser("scheduleduser2", models.Student, facility.ID, "444")
	require.NoError(t, err)

	// Enroll users via API endpoint
	enrollmentData := map[string]any{
		"user_ids": []int{int(user1.ID), int(user2.ID)},
	}

	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/enrollments", scheduledClass.ID), enrollmentData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	// Verify enrolled_at timestamps are NULL for Scheduled class
	var enrollments []programs.ProgramClassEnrollment
	err = env.DB.Where("cohort_id = ?", scheduledClass.ID).Find(&enrollments).Error
	require.NoError(t, err)
	require.Len(t, enrollments, 2, "Should have 2 enrollments")

	for _, enrollment := range enrollments {
		require.Nil(t, enrollment.EnrolledAt, "enrolled_at should be NULL for Scheduled class")
		require.Nil(t, enrollment.EnrollmentEndedAt, "enrollment_ended_at should be NULL initially")
		require.Equal(t, programs.Enrolled, enrollment.EnrollmentStatus, "status should be Enrolled")
	}
}

func TestUpdateProgramClassEnrollments(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Test Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("testadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	t.Run("Update enrollment to terminal status on Active class sets enrollment_ended_at", func(t *testing.T) {
		runUpdateToTerminalStatusTest(t, env, facility, facilityAdmin)
	})

	t.Run("Update enrollment to Cancelled status on Active class return 400 status code", func(t *testing.T) {
		runUpdateToCancelledStatusTest(t, env, facility, facilityAdmin)
	})
}

func runUpdateToTerminalStatusTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User) {
	// Create program and make it available at facility
	program, err := env.CreateTestProgram("Terminal Status Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "updateterminal")
	require.NoError(t, err)

	// Create an Active class
	activeClass, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	// Define all terminal statuses
	terminalStatuses := []programs.ProgramEnrollmentStatus{
		programs.EnrollmentCompleted,
		programs.EnrollmentIncompleteWithdrawn,
		programs.EnrollmentIncompleteDropped,
		programs.EnrollmentIncompleteFailedToComplete,
		programs.EnrollmentIncompleteTransfered,
		programs.EnrollmentIncompleteSegregated,
	}

	// Test each terminal status
	for i, status := range terminalStatuses {
		t.Run(string(status), func(t *testing.T) {
			// Create a unique test user for this status
			user, err := env.CreateTestUser(fmt.Sprintf("terminaluser%d", i), models.Student, facility.ID, fmt.Sprintf("test%d", 100+i))
			require.NoError(t, err)

			// Create enrollment in Active class (should set enrolled_at immediately)
			enrollment, err := env.CreateTestEnrollment(activeClass.ID, user.ID, programs.Enrolled)
			require.NoError(t, err)

			// Verify initial state: enrolled_at set, enrollment_ended_at NULL
			enrolledAt, endedAt, err := env.GetEnrollmentTimestamps(enrollment.ID)
			require.NoError(t, err)
			require.NotNil(t, enrolledAt, "enrolled_at should be set for Active class enrollment")
			require.Nil(t, endedAt, "enrollment_ended_at should be NULL initially")

			// Update enrollment status to terminal state via API
			updateData := map[string]any{
				"enrollment_status": string(status),
				"user_ids":          []int{int(user.ID)},
			}

			NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes/%d/enrollments", activeClass.ID), updateData).
				WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
				Do().
				ExpectStatus(http.StatusOK)

			// Verify enrollment_ended_at timestamp was set
			var updatedEnrollment programs.ProgramClassEnrollment
			err = env.DB.Where("cohort_id = ? AND user_id = ?", activeClass.ID, user.ID).First(&updatedEnrollment).Error
			require.NoError(t, err)

			require.Equal(t, status, updatedEnrollment.EnrollmentStatus, "status should be updated")
			require.NotNil(t, updatedEnrollment.EnrolledAt, "enrolled_at should remain set")
			require.NotNil(t, updatedEnrollment.EnrollmentEndedAt, "enrollment_ended_at should be set for terminal status")
			require.WithinDuration(t, time.Now(), *updatedEnrollment.EnrollmentEndedAt, time.Second*5, "enrollment_ended_at should be recent")
		})
	}
}

func runUpdateToCancelledStatusTest(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User) {
	// Create program and make it available at facility
	program, err := env.CreateTestProgram("Cancelled Status Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "terminal")
	require.NoError(t, err)

	// Create an Active class
	activeClass, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	cancelledStatus := programs.EnrollmentCancelled
	user, err := env.CreateTestUser("statususer1", models.Student, facility.ID, "status1001")
	require.NoError(t, err)

	// Create enrollment in Active class (should set enrolled_at immediately)
	enrollment, err := env.CreateTestEnrollment(activeClass.ID, user.ID, programs.Enrolled)
	require.NoError(t, err)

	// Verify initial state: enrolled_at set, enrollment_ended_at NULL
	enrolledAt, endedAt, err := env.GetEnrollmentTimestamps(enrollment.ID)
	require.NoError(t, err)
	require.NotNil(t, enrolledAt, "enrolled_at should be set for Active class enrollment")
	require.Nil(t, endedAt, "enrollment_ended_at should be NULL initially")

	// Update enrollment status to terminal state via API
	updateData := map[string]any{
		"enrollment_status": string(cancelledStatus),
		"user_ids":          []int{int(user.ID)},
	}

	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes/%d/enrollments", activeClass.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusBadRequest)
}
func TestGetHistoricalEnrollmentBatch(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Historical Test Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("histadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	// Create program and make it available at facility
	program, err := env.CreateTestProgram("Historical Enrollment Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "terminal")
	require.NoError(t, err)

	// Create an Active class
	activeClass, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	// Create test users
	user1, err := env.CreateTestUser("histuser1", models.Student, facility.ID, "hist1001")
	require.NoError(t, err)
	user2, err := env.CreateTestUser("histuser2", models.Student, facility.ID, "hist1002")
	require.NoError(t, err)
	user3, err := env.CreateTestUser("histuser3", models.Student, facility.ID, "hist1003")
	require.NoError(t, err)

	// Create enrollments with specific dates
	// User1: enrolled on 2025-08-13
	enrolledAt1 := time.Date(2025, 8, 13, 0, 0, 0, 0, time.UTC)
	enrollment1 := &programs.ProgramClassEnrollment{
		CohortID:         activeClass.ID,
		UserID:           user1.ID,
		EnrollmentStatus: programs.Enrolled,
		EnrolledAt:       &enrolledAt1,
	}
	err = env.DB.Create(enrollment1).Error
	require.NoError(t, err)

	// User2: enrolled on 2025-08-14
	enrolledAt2 := time.Date(2025, 8, 14, 0, 0, 0, 0, time.UTC)
	enrollment2 := &programs.ProgramClassEnrollment{
		CohortID:         activeClass.ID,
		UserID:           user2.ID,
		EnrollmentStatus: programs.Enrolled,
		EnrolledAt:       &enrolledAt2,
	}
	err = env.DB.Create(enrollment2).Error
	require.NoError(t, err)

	// User3: enrolled on 2025-08-15, ended on 2025-08-16
	enrolledAt3 := time.Date(2025, 8, 15, 0, 0, 0, 0, time.UTC)
	endedAt3 := time.Date(2025, 8, 16, 0, 0, 0, 0, time.UTC)
	enrollment3 := &programs.ProgramClassEnrollment{
		CohortID:          activeClass.ID,
		UserID:            user3.ID,
		EnrollmentStatus:  programs.EnrollmentCompleted,
		EnrolledAt:        &enrolledAt3,
		EnrollmentEndedAt: &endedAt3,
	}
	err = env.DB.Create(enrollment3).Error
	require.NoError(t, err)

	t.Run("Single date with varying enrollment levels", func(t *testing.T) {
		runSingleDateHistoricalEnrollmentTest(t, env, activeClass, facilityAdmin, facility)
	})

	t.Run("Batch multiple dates with varying enrollment levels", func(t *testing.T) {
		runBatchDateHistoricalEnrollmentTest(t, env, activeClass, facilityAdmin, facility)
	})

	t.Run("Edge cases and validation", func(t *testing.T) {
		runHistoricalEnrollmentEdgeCasesTest(t, env, activeClass, facilityAdmin, facility)
	})
}

func runSingleDateHistoricalEnrollmentTest(t *testing.T, env *TestEnv, activeClass *programs.ProgramClassCohort, facilityAdmin *models.User, facility *models.Facility) {
	// Test single date queries
	testCases := []struct {
		date     string
		expected int64
		desc     string
	}{
		{"2025-08-12", 0, "no enrollments before 2025-08-13"},
		{"2025-08-13", 1, "only user1 enrolled by 2025-08-13"},
		{"2025-08-14", 2, "user1 and user2 enrolled by 2025-08-14"},
		{"2025-08-15", 3, "all three users enrolled on 2025-08-15"},
		{"2025-08-16", 2, "user3 enrollment ended on 2025-08-16, so only user1 and user2"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			resp := NewRequest[map[string]int64](env.Client, t, http.MethodGet, fmt.Sprintf("/api/program-classes/%d/historical-enrollment-batch?dates=%s", activeClass.ID, tc.date), nil).
				WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
				Do().
				ExpectStatus(http.StatusOK)

			result := resp.GetData()
			require.Contains(t, result, tc.date, "Response should contain the requested date")
			require.Equal(t, tc.expected, result[tc.date], tc.desc)
		})
	}
}

func runBatchDateHistoricalEnrollmentTest(t *testing.T, env *TestEnv, activeClass *programs.ProgramClassCohort, facilityAdmin *models.User, facility *models.Facility) {
	// Test batch query with multiple dates
	dates := "2025-08-12,2025-08-13,2025-08-14,2025-08-15,2025-08-16"
	resp := NewRequest[map[string]int64](env.Client, t, http.MethodGet, fmt.Sprintf("/api/program-classes/%d/historical-enrollment-batch?dates=%s", activeClass.ID, dates), nil).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	result := resp.GetData()

	// Verify all dates are present in response
	expectedResults := map[string]int64{
		"2025-08-12": 0, // no enrollments before 2025-08-13
		"2025-08-13": 1, // only user1 enrolled by 2025-08-13
		"2025-08-14": 2, // user1 and user2 enrolled by 2025-08-14
		"2025-08-15": 3, // all three users enrolled on 2025-08-15
		"2025-08-16": 2, // user3 enrollment ended on 2025-08-16, so only user1 and user2
	}

	require.Len(t, result, 5, "Should return results for all 5 dates")

	for date, expected := range expectedResults {
		require.Contains(t, result, date, "Response should contain date %s", date)
		require.Equal(t, expected, result[date], "Incorrect enrollment count for %s", date)
	}

	// Verify this was a single API call that returned all results
	t.Logf("Successfully retrieved historical enrollment data for %d dates in a single batch request", len(expectedResults))
}

func runHistoricalEnrollmentEdgeCasesTest(t *testing.T, env *TestEnv, activeClass *programs.ProgramClassCohort, facilityAdmin *models.User, facility *models.Facility) {
	// Test case 1: Missing dates parameter
	t.Run("Missing dates parameter", func(t *testing.T) {
		NewRequest[any](env.Client, t, http.MethodGet, fmt.Sprintf("/api/program-classes/%d/historical-enrollment-batch", activeClass.ID), nil).
			WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
			Do().
			ExpectStatus(http.StatusBadRequest)
	})

	// Test case 2: Empty dates parameter
	t.Run("Empty dates parameter", func(t *testing.T) {
		NewRequest[any](env.Client, t, http.MethodGet, fmt.Sprintf("/api/program-classes/%d/historical-enrollment-batch?dates=", activeClass.ID), nil).
			WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
			Do().
			ExpectStatus(http.StatusBadRequest)
	})

	// Test case 3: Invalid date format
	t.Run("Invalid date format", func(t *testing.T) {
		invalidDates := []string{
			"2025/08/13",   // wrong format
			"2025-13-01",   // invalid month
			"2025-02-30",   // invalid day
			"invalid-date", // completely invalid
			"2025-8-1",     // missing zero padding
		}

		for _, invalidDate := range invalidDates {
			t.Run(fmt.Sprintf("date_%s", invalidDate), func(t *testing.T) {
				NewRequest[any](env.Client, t, http.MethodGet, fmt.Sprintf("/api/program-classes/%d/historical-enrollment-batch?dates=%s", activeClass.ID, invalidDate), nil).
					WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
					Do().
					ExpectStatus(http.StatusBadRequest)
			})
		}
	})

	// Test case 4: Mixed valid and invalid dates
	t.Run("Mixed valid and invalid dates", func(t *testing.T) {
		dates := "2025-08-13,invalid-date,2025-08-14"
		NewRequest[any](env.Client, t, http.MethodGet, fmt.Sprintf("/api/program-classes/%d/historical-enrollment-batch?dates=%s", activeClass.ID, dates), nil).
			WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
			Do().
			ExpectStatus(http.StatusBadRequest)
	})
}

// TestEligibleResidentsFilteredByClassFacility verifies that GetEligibleResidentsForClass
// returns only residents at the class's own facility, regardless of the caller's facility context.
func TestEligibleResidentsFilteredByClassFacility(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facilityA, err := env.CreateTestFacility("Facility A")
	require.NoError(t, err)
	facilityB, err := env.CreateTestFacility("Facility B")
	require.NoError(t, err)

	deptAdmin, err := env.CreateTestUser("deptadmin", models.DepartmentAdmin, facilityB.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Eligible Residents Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)
	err = env.SetFacilitiesToProgram(program.ID, []uint{facilityA.ID, facilityB.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facilityA.ID, "eligibletest")
	require.NoError(t, err)

	// Class is at facility A
	class, err := env.CreateTestClass(program, facilityA, programs.Active, &instructor.ID)
	require.NoError(t, err)

	// Students at facility A
	enrolledA, err := env.CreateTestUser("enra", models.Student, facilityA.ID, "ea001")
	require.NoError(t, err)
	unenrolledA, err := env.CreateTestUser("unenra", models.Student, facilityA.ID, "ua001")
	require.NoError(t, err)

	// Student at facility B — must never appear
	_, err = env.CreateTestUser("unenrb", models.Student, facilityB.ID, "ub001")
	require.NoError(t, err)

	// Deactivated student at facility A — must be excluded
	deactivatedA, err := env.CreateTestUser("deacta", models.Student, facilityA.ID, "da001")
	require.NoError(t, err)
	require.NoError(t, env.DB.Model(deactivatedA).Update("deactivated_at", "2024-01-01").Error)

	// Enroll enrolledA so they no longer appear as eligible
	_, err = env.CreateTestEnrollment(class.ID, enrolledA.ID, programs.Enrolled)
	require.NoError(t, err)

	t.Run("dept admin with facilityB context sees only unenrolled facilityA residents", func(t *testing.T) {
		// Dept admin passes facility_id=B in the query — backend must ignore it and use class's facility
		resp := NewRequest[[]models.User](env.Client, t, http.MethodGet,
			fmt.Sprintf("/api/users?include=only_unenrolled&cohort_id=%d&facility_id=%d&per_page=50", class.ID, facilityB.ID), nil).
			WithTestClaims(&handlers.Claims{Role: models.DepartmentAdmin, UserID: deptAdmin.ID, FacilityID: facilityB.ID}).
			Do().
			ExpectStatus(http.StatusOK)

		users := resp.GetData()
		ids := make([]uint, len(users))
		for i, u := range users {
			ids[i] = u.ID
		}

		require.Contains(t, ids, unenrolledA.ID, "unenrolled facility-A student should be returned")
		require.NotContains(t, ids, enrolledA.ID, "already-enrolled student should be excluded")
		require.NotContains(t, ids, deactivatedA.ID, "deactivated student should be excluded")
		for _, u := range users {
			require.Equal(t, facilityA.ID, u.FacilityID, "only facility-A residents should be returned")
		}
	})
}
