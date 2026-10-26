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

func TestRoomConflictDetection(t *testing.T) {
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

	t.Run("Detects overlapping bookings", func(t *testing.T) {
		room := &programs.Room{FacilityID: facility.ID, Name: "Conflict Room 1"}
		require.NoError(t, env.DB.Create(room).Error)
		testDetectsOverlappingBookings(t, env, facility, facilityAdmin, program, room.ID)
	})

	t.Run("Excludes own event ID when updating", func(t *testing.T) {
		room := &programs.Room{FacilityID: facility.ID, Name: "Conflict Room 2"}
		require.NoError(t, env.DB.Create(room).Error)
		testExcludesOwnEventID(t, env, facility, facilityAdmin, program, room.ID)
	})

	t.Run("No conflict for different times", func(t *testing.T) {
		room := &programs.Room{FacilityID: facility.ID, Name: "Conflict Room 3"}
		require.NoError(t, env.DB.Create(room).Error)
		testNoConflictForDifferentTimes(t, env, facility, facilityAdmin, program, room.ID)
	})

	t.Run("Handler returns 409 on room conflict", func(t *testing.T) {
		room := &programs.Room{FacilityID: facility.ID, Name: "Conflict Room 4"}
		require.NoError(t, env.DB.Create(room).Error)
		testHandlerReturns409OnConflict(t, env, facility, facilityAdmin, program, room.ID)
	})

	t.Run("Cancelling a series bypasses room conflict check", func(t *testing.T) {
		room := &programs.Room{FacilityID: facility.ID, Name: "Cancel Bypass Room"}
		require.NoError(t, env.DB.Create(room).Error)
		testCancelSeriesBypassesConflict(t, env, facility, facilityAdmin, program, room.ID)
	})

	t.Run("Rejects room from different facility", func(t *testing.T) {
		otherFacility, err := env.CreateTestFacility("Other Facility")
		require.NoError(t, err)
		otherRoom := &programs.Room{FacilityID: otherFacility.ID, Name: "Other Room"}
		require.NoError(t, env.DB.Create(otherRoom).Error)
		testRejectsRoomFromDifferentFacility(t, env, facility, facilityAdmin, program, otherRoom.ID)
	})

	t.Run("Detects conflicts with timezone-aware RRULEs across DST", func(t *testing.T) {
		room := &programs.Room{FacilityID: facility.ID, Name: "DST Test Room"}
		require.NoError(t, env.DB.Create(room).Error)
		testDetectsConflictsWithTimezoneAwareRRules(t, env, facility, facilityAdmin, program, room.ID)
	})

	t.Run("Backwards compatible with UTC-based RRULEs", func(t *testing.T) {
		room := &programs.Room{FacilityID: facility.ID, Name: "UTC Compat Room"}
		require.NoError(t, env.DB.Create(room).Error)
		testBackwardsCompatibleUTCRules(t, env, facility, facilityAdmin, program, room.ID)
	})
}

func testDetectsOverlappingBookings(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program, roomID uint) {
	instructor, err := env.CreateTestInstructor(facility.ID, "conflict1")
	require.NoError(t, err)

	classStartDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	classEndDate := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	creditHours := int64(2)

	class1 := programs.ProgramClassCohort{
		ProgramID:    program.ID,
		FacilityID:   facility.ID,
		Capacity:     10,
		InstructorID: &instructor.ID,
		Description:  "First class to book the room",
		StartDt:      classStartDate,
		EndDt:        &classEndDate,
		Status:       programs.Scheduled,
		CreditHours:  &creditHours,
	}

	class1Resp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class1).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	createdClass1 := class1Resp.GetData()
	require.NoError(t, env.NameCohortClass(createdClass1.ID, "First Class"))

	event1Payload := map[string]interface{}{
		"duration":        "2h",
		"room_id":         roomID,
		"recurrence_rule": "DTSTART:20260302T090000Z\nRRULE:FREQ=DAILY;UNTIL=20260331T000000Z",
		"instructor_id":   instructor.ID,
	}

	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/events", createdClass1.ID), event1Payload).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	conflicts, err := env.Programs.CheckRRuleConflicts(&programs.ConflictCheckRequest{
		FacilityID:     facility.ID,
		RoomID:         roomID,
		RecurrenceRule: "DTSTART:20260302T093000Z\nRRULE:FREQ=DAILY;UNTIL=20260310T000000Z",
		Duration:       "1h",
	})
	require.NoError(t, err)
	require.NotEmpty(t, conflicts, "Should detect conflicts for overlapping times")
	require.Equal(t, "First Class", conflicts[0].ClassName, "Conflict should identify the class name")
}

func testExcludesOwnEventID(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program, roomID uint) {
	instructor, err := env.CreateTestInstructor(facility.ID, "conflict2")
	require.NoError(t, err)

	classStartDate := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	classEndDate := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	creditHours := int64(2)

	class := programs.ProgramClassCohort{
		ProgramID:    program.ID,
		FacilityID:   facility.ID,
		Capacity:     10,
		InstructorID: &instructor.ID,
		Description:  "Class for testing self-exclusion",
		StartDt:      classStartDate,
		EndDt:        &classEndDate,
		Status:       programs.Scheduled,
		CreditHours:  &creditHours,
	}

	classResp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	createdClass := classResp.GetData()

	eventPayload := map[string]interface{}{
		"duration":        "2h",
		"room_id":         roomID,
		"recurrence_rule": "DTSTART:20260402T140000Z\nRRULE:FREQ=DAILY;UNTIL=20260430T000000Z",
		"instructor_id":   instructor.ID,
	}

	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/events", createdClass.ID), eventPayload).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	var event programs.ProgramClassEvent
	err = env.DB.Where("cohort_id = ?", createdClass.ID).First(&event).Error
	require.NoError(t, err)

	conflictsWithoutExclusion, err := env.Programs.CheckRRuleConflicts(&programs.ConflictCheckRequest{
		FacilityID:     facility.ID,
		RoomID:         roomID,
		RecurrenceRule: "DTSTART:20260402T140000Z\nRRULE:FREQ=DAILY;UNTIL=20260410T000000Z",
		Duration:       "2h",
	})
	require.NoError(t, err)
	require.NotEmpty(t, conflictsWithoutExclusion, "Should detect conflicts when not excluding own event")

	conflictsWithExclusion, err := env.Programs.CheckRRuleConflicts(&programs.ConflictCheckRequest{
		FacilityID:     facility.ID,
		RoomID:         roomID,
		RecurrenceRule: "DTSTART:20260402T140000Z\nRRULE:FREQ=DAILY;UNTIL=20260410T000000Z",
		Duration:       "2h",
		ExcludeEventID: &event.ID,
	})
	require.NoError(t, err)
	require.Empty(t, conflictsWithExclusion, "Should NOT detect conflicts when excluding own event ID")
}

func testNoConflictForDifferentTimes(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program, roomID uint) {
	instructor, err := env.CreateTestInstructor(facility.ID, "conflict3")
	require.NoError(t, err)

	classStartDate := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	classEndDate := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	creditHours := int64(2)

	class := programs.ProgramClassCohort{
		ProgramID:    program.ID,
		FacilityID:   facility.ID,
		Capacity:     10,
		InstructorID: &instructor.ID,
		Description:  "Morning class",
		StartDt:      classStartDate,
		EndDt:        &classEndDate,
		Status:       programs.Scheduled,
		CreditHours:  &creditHours,
	}

	classResp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	createdClass := classResp.GetData()

	eventPayload := map[string]interface{}{
		"duration":        "2h",
		"room_id":         roomID,
		"recurrence_rule": "DTSTART:20260504T090000Z\nRRULE:FREQ=DAILY;UNTIL=20260531T000000Z",
		"instructor_id":   instructor.ID,
	}

	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/events", createdClass.ID), eventPayload).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	conflicts, err := env.Programs.CheckRRuleConflicts(&programs.ConflictCheckRequest{
		FacilityID:     facility.ID,
		RoomID:         roomID,
		RecurrenceRule: "DTSTART:20260504T140000Z\nRRULE:FREQ=DAILY;UNTIL=20260510T000000Z",
		Duration:       "2h",
	})
	require.NoError(t, err)
	require.Empty(t, conflicts, "Should NOT detect conflicts for non-overlapping times (9-11 AM vs 2-4 PM)")
}

// testCancelSeriesBypassesConflict verifies that cancelling a series (event_series
// with is_cancelled=true) is NOT blocked by a room conflict — a cancellation frees
// the room rather than booking it.
func testCancelSeriesBypassesConflict(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program, roomID uint) {
	instructor1, err := env.CreateTestInstructor(facility.ID, "cancelbypassa")
	require.NoError(t, err)
	instructor2, err := env.CreateTestInstructor(facility.ID, "cancelbypassb")
	require.NoError(t, err)

	classStartDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	classEndDate := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	creditHours := int64(2)

	mkClass := func(name string, instructorID uint) *programs.ProgramClassCohort {
		resp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), programs.ProgramClassCohort{
			ProgramID:    program.ID,
			FacilityID:   facility.ID,
			Capacity:     10,
			InstructorID: &instructorID,
			Description:  name,
			StartDt:      classStartDate,
			EndDt:        &classEndDate,
			Status:       programs.Scheduled,
			CreditHours:  &creditHours,
		}).
			WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
			Do().
			ExpectStatus(http.StatusCreated)
		return resp.GetData()
	}

	// Blocker class occupies the room daily at 10:00-12:00.
	blocker := mkClass("Room Blocker", instructor1.ID)
	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/events", blocker.ID), map[string]interface{}{
		"duration":        "2h",
		"room_id":         roomID,
		"recurrence_rule": "DTSTART:20260601T100000Z\nRRULE:FREQ=DAILY;UNTIL=20260630T000000Z",
		"instructor_id":   instructor1.ID,
	}).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	// Second class with a non-overlapping event (14:00) so creation succeeds.
	toCancel := mkClass("Class To Cancel", instructor2.ID)
	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/events", toCancel.ID), map[string]interface{}{
		"duration":        "2h",
		"room_id":         roomID,
		"recurrence_rule": "DTSTART:20260601T140000Z\nRRULE:FREQ=DAILY;UNTIL=20260630T000000Z",
		"instructor_id":   instructor2.ID,
	}).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	var toCancelEvent programs.ProgramClassEvent
	require.NoError(t, env.DB.Where("cohort_id = ?", toCancel.ID).First(&toCancelEvent).Error)

	// Cancel the series. The cancelled event_series carries the room and a rule that
	// overlaps the blocker (10:00) — pre-fix this returned 409, now it must succeed.
	cancelPayload := map[string]interface{}{
		"event_series": map[string]interface{}{
			"id":              0,
			"duration":        "2h",
			"room_id":         roomID,
			"instructor_id":   instructor2.ID,
			"is_cancelled":    true,
			"recurrence_rule": "DTSTART:20260601T100000Z\nRRULE:FREQ=DAILY;UNTIL=20260630T000000Z",
		},
		"closed_event_series": map[string]interface{}{
			"id":              toCancelEvent.ID,
			"recurrence_rule": "DTSTART:20260601T140000Z\nRRULE:FREQ=DAILY;UNTIL=20260531T235959Z",
		},
	}
	NewRequest[any](env.Client, t, http.MethodPut, fmt.Sprintf("/api/program-classes/%d/events", toCancel.ID), cancelPayload).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)
}

func testHandlerReturns409OnConflict(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program, roomID uint) {
	instructor1, err := env.CreateTestInstructor(facility.ID, "conflict4a")
	require.NoError(t, err)

	instructor2, err := env.CreateTestInstructor(facility.ID, "conflict4b")
	require.NoError(t, err)

	classStartDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	classEndDate := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	creditHours := int64(2)

	class1 := programs.ProgramClassCohort{
		ProgramID:    program.ID,
		FacilityID:   facility.ID,
		Capacity:     10,
		InstructorID: &instructor1.ID,
		Description:  "Class that already has the room",
		StartDt:      classStartDate,
		EndDt:        &classEndDate,
		Status:       programs.Scheduled,
		CreditHours:  &creditHours,
	}

	class1Resp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class1).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	createdClass1 := class1Resp.GetData()

	event1Payload := map[string]interface{}{
		"duration":        "2h",
		"room_id":         roomID,
		"recurrence_rule": "DTSTART:20260601T100000Z\nRRULE:FREQ=DAILY;UNTIL=20260630T000000Z",
		"instructor_id":   instructor1.ID,
	}

	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/events", createdClass1.ID), event1Payload).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	class2 := programs.ProgramClassCohort{
		ProgramID:    program.ID,
		FacilityID:   facility.ID,
		Capacity:     10,
		InstructorID: &instructor2.ID,
		Description:  "Class that tries to book the same room",
		StartDt:      classStartDate,
		EndDt:        &classEndDate,
		Status:       programs.Scheduled,
		CreditHours:  &creditHours,
	}

	class2Resp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class2).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	createdClass2 := class2Resp.GetData()

	conflictingEventPayload := map[string]interface{}{
		"duration":        "1h",
		"room_id":         roomID,
		"recurrence_rule": "DTSTART:20260601T103000Z\nRRULE:FREQ=DAILY;UNTIL=20260610T000000Z",
		"instructor_id":   instructor2.ID,
	}

	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/events", createdClass2.ID), conflictingEventPayload).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusConflict)
}

func testRejectsRoomFromDifferentFacility(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program, otherFacilityRoomID uint) {
	instructor, err := env.CreateTestInstructor(facility.ID, "conflict5")
	require.NoError(t, err)

	classStartDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	classEndDate := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	creditHours := int64(2)

	class := programs.ProgramClassCohort{
		ProgramID:    program.ID,
		FacilityID:   facility.ID,
		Capacity:     10,
		InstructorID: &instructor.ID,
		Description:  "Class trying to use room from different facility",
		StartDt:      classStartDate,
		EndDt:        &classEndDate,
		Status:       programs.Scheduled,
		CreditHours:  &creditHours,
	}

	classResp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	createdClass := classResp.GetData()

	eventPayload := map[string]interface{}{
		"duration":        "2h",
		"room_id":         otherFacilityRoomID,
		"recurrence_rule": "DTSTART:20260702T100000Z\nRRULE:FREQ=DAILY;UNTIL=20260731T000000Z",
		"instructor_id":   instructor.ID,
	}

	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/events", createdClass.ID), eventPayload).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusBadRequest)
}

// testDetectsConflictsWithTimezoneAwareRRules tests that timezone-aware RRULEs
// correctly detect conflicts across DST transitions. Two classes at "10 AM local time"
// created in different DST periods should conflict when using DTSTART;TZID= format.
func testDetectsConflictsWithTimezoneAwareRRules(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program, roomID uint) {
	// Use America/New_York timezone for DST testing
	facility.Timezone = "America/New_York"
	require.NoError(t, env.DB.Save(facility).Error)

	instructor, err := env.CreateTestInstructor(facility.ID, "conflict6")
	require.NoError(t, err)

	// Class 1: Created in July (EDT) at 10 AM local time, repeating on Thursdays
	// Using timezone-aware RRULE format: DTSTART;TZID=America/New_York:20250701T100000
	classStartDate := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	creditHours := int64(2)

	class1 := programs.ProgramClassCohort{
		ProgramID:    program.ID,
		FacilityID:   facility.ID,
		Capacity:     10,
		InstructorID: &instructor.ID,
		Description:  "Class created in summer at 10 AM",
		StartDt:      classStartDate,
		Status:       programs.Scheduled,
		CreditHours:  &creditHours,
	}

	class1Resp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class1).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	createdClass1 := class1Resp.GetData()
	require.NoError(t, env.NameCohortClass(createdClass1.ID, "Summer Class"))

	// Timezone-aware RRULE: 10 AM in America/New_York, every Thursday
	event1Payload := map[string]interface{}{
		"duration":        "1h",
		"room_id":         roomID,
		"recurrence_rule": "DTSTART;TZID=America/New_York:20250703T100000\nRRULE:FREQ=WEEKLY;BYDAY=TH",
		"instructor_id":   instructor.ID,
	}

	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/events", createdClass1.ID), event1Payload).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	// Now try to create Class 2 in December (EST) at 10 AM local time
	// With timezone-aware RRULEs, this should conflict because both are at 10 AM wall time
	conflicts, err := env.Programs.CheckRRuleConflicts(&programs.ConflictCheckRequest{
		FacilityID:     facility.ID,
		RoomID:         roomID,
		RecurrenceRule: "DTSTART;TZID=America/New_York:20251204T100000\nRRULE:FREQ=WEEKLY;BYDAY=TH",
		Duration:       "1h",
	})
	require.NoError(t, err)
	require.NotEmpty(t, conflicts, "Should detect conflicts when both classes are at 10 AM wall time (timezone-aware RRULEs preserve wall time across DST)")
	require.Equal(t, "Summer Class", conflicts[0].ClassName, "Conflict should identify the Summer Class")

	// Verify that classes at different wall times don't conflict
	// 11 AM local time should NOT conflict with 10 AM class
	noConflicts, err := env.Programs.CheckRRuleConflicts(&programs.ConflictCheckRequest{
		FacilityID:     facility.ID,
		RoomID:         roomID,
		RecurrenceRule: "DTSTART;TZID=America/New_York:20251204T110000\nRRULE:FREQ=WEEKLY;BYDAY=TH",
		Duration:       "1h",
	})
	require.NoError(t, err)
	require.Empty(t, noConflicts, "Should NOT detect conflicts when classes are at different wall times (10 AM vs 11 AM)")
}

// testBackwardsCompatibleUTCRules verifies that old UTC-based RRULEs still work.
// Note: UTC-based RRULEs will have the DST shift behavior (this is expected for legacy data).
func testBackwardsCompatibleUTCRules(t *testing.T, env *TestEnv, facility *models.Facility, facilityAdmin *models.User, program *programs.Program, roomID uint) {
	facility.Timezone = "America/New_York"
	require.NoError(t, env.DB.Save(facility).Error)

	instructor, err := env.CreateTestInstructor(facility.ID, "conflict7")
	require.NoError(t, err)

	classStartDate := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	creditHours := int64(2)

	class1 := programs.ProgramClassCohort{
		ProgramID:    program.ID,
		FacilityID:   facility.ID,
		Capacity:     10,
		InstructorID: &instructor.ID,
		Description:  "Class using old UTC-based RRULE format",
		StartDt:      classStartDate,
		Status:       programs.Scheduled,
		CreditHours:  &creditHours,
	}

	class1Resp := NewRequest[*programs.ProgramClassCohort](env.Client, t, http.MethodPost, fmt.Sprintf("/api/programs/%d/classes", program.ID), class1).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	createdClass1 := class1Resp.GetData()

	// Old UTC-based RRULE format: 14:00 UTC = 10:00 AM EDT in August
	event1Payload := map[string]interface{}{
		"duration":        "1h",
		"room_id":         roomID,
		"recurrence_rule": "DTSTART:20260806T140000Z\nRRULE:FREQ=WEEKLY;BYDAY=TH",
		"instructor_id":   instructor.ID,
	}

	NewRequest[any](env.Client, t, http.MethodPost, fmt.Sprintf("/api/program-classes/%d/events", createdClass1.ID), event1Payload).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusCreated)

	// Another UTC-based RRULE at the same UTC time should conflict
	conflicts, err := env.Programs.CheckRRuleConflicts(&programs.ConflictCheckRequest{
		FacilityID:     facility.ID,
		RoomID:         roomID,
		RecurrenceRule: "DTSTART:20260806T140000Z\nRRULE:FREQ=WEEKLY;BYDAY=TH",
		Duration:       "1h",
	})
	require.NoError(t, err)
	require.NotEmpty(t, conflicts, "UTC-based RRULEs at same UTC time should still conflict (backwards compatibility)")
}
