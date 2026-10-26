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

// Changing ONLY the instructor on a session must not be reported as a room conflict.
//
// The schedule's single-session editor always sends the CURRENT room_id alongside the
// field being edited, and handleEventOverrides used to run a room-only conflict check
// (CheckRRuleConflicts ignores InstructorID) whenever room_id was present. So an
// instructor-only edit re-validated a room that was not changing and came back
// "room is already booked during this time".
func TestChangeInstructorOnlyIsNotARoomConflict(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Conflict Facility")
	require.NoError(t, err)

	admin, err := env.CreateTestUser("conflictadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Conflict Program", programs.FundingType(programs.FederalGrants),
		[]programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)
	require.NoError(t, env.SetFacilitiesToProgram(program.ID, []uint{facility.ID}))

	instructorA, err := env.CreateTestInstructor(facility.ID, "conflicta")
	require.NoError(t, err)
	instructorB, err := env.CreateTestInstructor(facility.ID, "conflictb")
	require.NoError(t, err)

	cohort, err := env.CreateTestClass(program, facility, programs.Active, &instructorA.ID)
	require.NoError(t, err)

	event, err := env.CreateTestEvent(cohort.ID, "", instructorA.ID)
	require.NoError(t, err)

	room := programs.Room{FacilityID: facility.ID, Name: "Conflict Room"}
	require.NoError(t, env.DB.Create(&room).Error)
	require.NoError(t, env.DB.Model(&programs.ProgramClassEvent{}).
		Where("id = ?", event.ID).
		Update("room_id", room.ID).Error)

	// A DIFFERENT class already books the same room at the same time. This is the state
	// that made the old code 409: the event's own bookings are excluded by event/cohort id,
	// so without a competing booking there is nothing to conflict with and the test would
	// pass either way. The clash is pre-existing and the instructor edit does not touch it.
	otherCohort, err := env.CreateTestClass(program, facility, programs.Active, &instructorA.ID)
	require.NoError(t, err)
	otherEvent, err := env.CreateTestEvent(otherCohort.ID, "DTSTART;TZID=UTC:20260901T090000\nRRULE:FREQ=DAILY", instructorA.ID)
	require.NoError(t, err)
	require.NoError(t, env.DB.Model(&programs.ProgramClassEvent{}).
		Where("id = ?", otherEvent.ID).
		Update("room_id", room.ID).Error)

	claims := &handlers.Claims{Role: models.FacilityAdmin, UserID: admin.ID, FacilityID: facility.ID}

	// Exactly what the schedule sends for "change instructor on this session": the new
	// instructor, and the room the session already had.
	body := []map[string]any{{
		"event_id":       event.ID,
		"cohort_id":      cohort.ID,
		"override_rrule": "DTSTART;TZID=UTC:20260901T090000\nRRULE:FREQ=DAILY;COUNT=1",
		"duration":       "1h0m0s",
		"room_id":        room.ID,
		"instructor_id":  instructorB.ID,
		"is_cancelled":   false,
	}}

	NewRequest[any](env.Client, t, http.MethodPut,
		fmt.Sprintf("/api/program-classes/%d/events/%d", cohort.ID, event.ID), body).
		WithTestClaims(claims).
		Do().
		ExpectStatus(http.StatusOK)
}
