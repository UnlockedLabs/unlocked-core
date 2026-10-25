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

func TestSchedulingConflictDetection(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Conflict Test Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("conflictadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Conflict Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructorA, err := env.CreateTestInstructor(facility.ID, "conflicta")
	require.NoError(t, err)

	instructorB, err := env.CreateTestInstructor(facility.ID, "conflictb")
	require.NoError(t, err)

	// Create Class A: Mon/Wed/Fri 10:00-11:00
	classA, err := env.CreateTestClass(program, facility, programs.Active, &instructorA.ID)
	require.NoError(t, err)
	classB, err := env.CreateTestClass(program, facility, programs.Active, &instructorB.ID)
	require.NoError(t, err)

	// Create test room for events
	room := programs.Room{FacilityID: facility.ID, Name: "Conflict Test Room"}
	err = env.DB.Create(&room).Error
	require.NoError(t, err)

	// Update Class A event to be specific
	eventA := programs.ProgramClassEvent{
		CohortID:       classA.ID,
		Duration:       "1h",
		RecurrenceRule: "FREQ=WEEKLY;BYDAY=MO,WE,FR;DTSTART=20240101T100000Z", // UTC
		RoomID:         &room.ID,
		InstructorID:   &instructorA.ID,
	}
	err = env.DB.Create(&eventA).Error
	require.NoError(t, err)

	// Update Class B event to overlap
	eventB := programs.ProgramClassEvent{
		CohortID:       classB.ID,
		Duration:       "1h",
		RecurrenceRule: "FREQ=WEEKLY;BYDAY=MO,WE,FR;DTSTART=20240101T103000Z", // UTC, overlaps by 30 mins
		RoomID:         &room.ID,
		InstructorID:   &instructorB.ID,
	}
	err = env.DB.Create(&eventB).Error
	require.NoError(t, err)

	user, err := env.CreateTestUser("conflictuser", models.Student, facility.ID, "999")
	require.NoError(t, err)

	// Enroll in Class A
	enrollmentDataA := map[string]interface{}{
		"user_ids": []int{int(user.ID)},
	}
	NewRequest[interface{}](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/enrollments", classA.ID), enrollmentDataA).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	// Enroll in Class B (Should fail with 409 Conflict)
	enrollmentDataB := map[string]interface{}{
		"user_ids": []int{int(user.ID)},
	}
	NewRequest[interface{}](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/enrollments", classB.ID), enrollmentDataB).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusConflict)

	// Enroll in Class B with confirmation (Should succeed)
	enrollmentDataBConfirm := map[string]interface{}{
		"user_ids": []int{int(user.ID)},
		"confirm":  true,
	}
	NewRequest[interface{}](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/enrollments", classB.ID), enrollmentDataBConfirm).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)
}
