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

// TestInstructorConflictDetection covers ID-642: assigning an instructor who is
// already teaching another class at an overlapping time must be detected.
func TestInstructorConflictDetection(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Instructor Conflict Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("instconflictadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Instructor Conflict Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	room := &programs.Room{FacilityID: facility.ID, Name: "Instructor Conflict Room"}
	require.NoError(t, env.DB.Create(room).Error)

	t.Run("Detects overlapping instructor bookings", func(t *testing.T) {
		testInstructorDetectsOverlap(t, env, facility, facilityAdmin, program, room.ID)
	})

	t.Run("No conflict for different times", func(t *testing.T) {
		testInstructorNoConflictDifferentTimes(t, env, facility, facilityAdmin, program, room.ID)
	})

	t.Run("Handler returns 409 when changing class instructor to a double-booked one", func(t *testing.T) {
		testHandlerReturns409OnInstructorChange(t, env, facility, facilityAdmin, program, room.ID)
	})
}

// helper: create a class with an instructor and a single daily event in the given window.
func createClassWithEvent(t *testing.T, env *TestEnv, facility *models.Facility, admin *models.User, program *programs.Program, name string, instructorID, roomID uint, recurrenceRule, duration string, start time.Time, end time.Time) *programs.ProgramClassCohort {
	creditHours := int64(2)
	class := programs.ProgramClassCohort{
		ProgramID:    program.ID,
		FacilityID:   facility.ID,
		Capacity:     10,
		InstructorID: &instructorID,
		Description:  name,
		StartDt:      start,
		EndDt:        &end,
		Status:       programs.Scheduled,
		CreditHours:  &creditHours,
	}
	resp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: admin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)
	created := resp.GetData()
	// `name` now belongs to the CLASS tier -- that is what conflicts display.
	require.NoError(t, env.NameCohortClass(created.ID, name))

	eventPayload := map[string]interface{}{
		"duration":        duration,
		"room_id":         roomID,
		"recurrence_rule": recurrenceRule,
		"instructor_id":   instructorID,
	}
	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/events", created.ID), eventPayload).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: admin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)
	return created
}

func testInstructorDetectsOverlap(t *testing.T, env *TestEnv, facility *models.Facility, admin *models.User, program *programs.Program, roomID uint) {
	instructor, err := env.CreateTestInstructor(facility.ID, "instoverlap")
	require.NoError(t, err)

	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	createClassWithEvent(t, env, facility, admin, program, "Instructor Morning Class", instructor.ID, roomID,
		"DTSTART:20260302T090000Z\nRRULE:FREQ=DAILY;UNTIL=20260331T000000Z", "2h", start, end)

	// Same instructor, overlapping window (9:30) -> conflict.
	conflicts, err := env.Programs.CheckConflicts(&programs.ConflictCheckRequest{
		FacilityID:     facility.ID,
		InstructorID:   instructor.ID,
		RecurrenceRule: "DTSTART:20260302T093000Z\nRRULE:FREQ=DAILY;UNTIL=20260310T000000Z",
		Duration:       "1h",
	})
	require.NoError(t, err)
	require.NotEmpty(t, conflicts, "should detect instructor double-booking for overlapping times")
	require.Equal(t, programs.ConflictTypeInstructor, conflicts[0].ConflictType, "conflict should be tagged as instructor")
	require.Equal(t, "Instructor Morning Class", conflicts[0].ClassName)
}

func testInstructorNoConflictDifferentTimes(t *testing.T, env *TestEnv, facility *models.Facility, admin *models.User, program *programs.Program, roomID uint) {
	instructor, err := env.CreateTestInstructor(facility.ID, "instnoconf")
	require.NoError(t, err)

	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	createClassWithEvent(t, env, facility, admin, program, "Instructor AM Class", instructor.ID, roomID,
		"DTSTART:20260504T090000Z\nRRULE:FREQ=DAILY;UNTIL=20260531T000000Z", "2h", start, end)

	// Same instructor but a non-overlapping afternoon window (2-4 PM) -> no conflict.
	conflicts, err := env.Programs.CheckConflicts(&programs.ConflictCheckRequest{
		FacilityID:     facility.ID,
		InstructorID:   instructor.ID,
		RecurrenceRule: "DTSTART:20260504T140000Z\nRRULE:FREQ=DAILY;UNTIL=20260510T000000Z",
		Duration:       "2h",
	})
	require.NoError(t, err)
	require.Empty(t, conflicts, "should NOT detect instructor conflict for non-overlapping times")
}

// testHandlerReturns409OnInstructorChange reproduces the ticket scenario: change
// class X's instructor to the instructor of overlapping class Y -> 409.
func testHandlerReturns409OnInstructorChange(t *testing.T, env *TestEnv, facility *models.Facility, admin *models.User, program *programs.Program, roomID uint) {
	instructor1, err := env.CreateTestInstructor(facility.ID, "instchangea")
	require.NoError(t, err)
	instructor2, err := env.CreateTestInstructor(facility.ID, "instchangeb")
	require.NoError(t, err)

	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)

	// Class 1 taught by instructor1 at 10:00.
	createClassWithEvent(t, env, facility, admin, program, "Class Y (instructor1)", instructor1.ID, roomID,
		"DTSTART:20260601T100000Z\nRRULE:FREQ=DAILY;UNTIL=20260630T000000Z", "2h", start, end)

	// Class 2 taught by instructor2 at an overlapping 10:30 (different room avoids a room conflict).
	room2 := &programs.Room{FacilityID: facility.ID, Name: "Instructor Conflict Room 2"}
	require.NoError(t, env.DB.Create(room2).Error)
	class2 := createClassWithEvent(t, env, facility, admin, program, "Class X (instructor2)", instructor2.ID, room2.ID,
		"DTSTART:20260601T103000Z\nRRULE:FREQ=DAILY;UNTIL=20260630T000000Z", "1h", start, end)

	// Changing class X's instructor to instructor1 collides with class Y -> 409.
	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/programs/%d/classes/%d", program.ID, class2.ID), map[string]interface{}{
		"instructor_id": instructor1.ID,
	}).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: admin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusConflict)

	// Changing to a free instructor succeeds.
	instructor3, err := env.CreateTestInstructor(facility.ID, "instchangec")
	require.NoError(t, err)
	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/programs/%d/classes/%d", program.ID, class2.ID), map[string]interface{}{
		"instructor_id": instructor3.ID,
	}).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: admin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)
}
