package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

func TestClassCompletedAddsUntilDate(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Test Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("admin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "rrule")
	require.NoError(t, err)

	class, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	indefiniteRRule := "DTSTART:20240101T100000Z\nRRULE:FREQ=WEEKLY;BYDAY=TU,TH"
	event, err := env.CreateTestEventWithRRule(class.ID, indefiniteRRule, instructor.ID)
	require.NoError(t, err)

	originalRule, err := env.GetEventRecurrenceRule(event.ID)
	require.NoError(t, err)
	require.Equal(t, indefiniteRRule, originalRule)
	require.Empty(t, programs.GetUntilDateFromRule(originalRule))

	completionTime := time.Now()
	updateData := map[string]any{
		"status": string(programs.Completed),
	}

	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", class.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	updatedRule, err := env.GetEventRecurrenceRule(event.ID)
	require.NoError(t, err)
	require.Contains(t, updatedRule, "UNTIL=")

	untilDate := programs.GetUntilDateFromRule(updatedRule)
	require.NotEmpty(t, untilDate)

	parsedUntil, err := time.Parse("20060102T150405Z", untilDate)
	require.NoError(t, err)

	expectedDay := time.Date(completionTime.Year(), completionTime.Month(), completionTime.Day(), 23, 59, 59, 0, time.UTC)
	nextDay := expectedDay.AddDate(0, 0, 1)

	require.True(t, parsedUntil.Equal(expectedDay) || parsedUntil.Equal(nextDay))
	require.True(t, strings.HasSuffix(untilDate, "T235959Z"))
}

func TestClassCancelledAddsUntilDate(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Test Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("admin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "rrule")
	require.NoError(t, err)

	class, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	indefiniteRRule := "DTSTART:20240715T150000Z\nRRULE:FREQ=DAILY;INTERVAL=2"
	event, err := env.CreateTestEventWithRRule(class.ID, indefiniteRRule, instructor.ID)
	require.NoError(t, err)

	originalRule, err := env.GetEventRecurrenceRule(event.ID)
	require.NoError(t, err)
	require.Equal(t, indefiniteRRule, originalRule)
	require.Empty(t, programs.GetUntilDateFromRule(originalRule))

	cancellationTime := time.Now()
	updateData := map[string]any{
		"status": string(programs.Cancelled),
	}

	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", class.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	updatedRule, err := env.GetEventRecurrenceRule(event.ID)
	require.NoError(t, err)
	require.Contains(t, updatedRule, "UNTIL=")

	untilDate := programs.GetUntilDateFromRule(updatedRule)
	require.NotEmpty(t, untilDate)

	parsedUntil, err := time.Parse("20060102T150405Z", untilDate)
	require.NoError(t, err)

	expectedDay := time.Date(cancellationTime.Year(), cancellationTime.Month(), cancellationTime.Day(), 23, 59, 59, 0, time.UTC)
	nextDay := expectedDay.AddDate(0, 0, 1)

	require.True(t, parsedUntil.Equal(expectedDay) || parsedUntil.Equal(nextDay))
	require.True(t, strings.HasSuffix(untilDate, "T235959Z"))
}

func TestMultipleEventsGetUntilDate(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Test Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("admin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "rrule")
	require.NoError(t, err)

	class, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	event1, err := env.CreateTestEventWithRRule(class.ID, "DTSTART:20240721T100000Z\nRRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR", instructor.ID)
	require.NoError(t, err)

	event2, err := env.CreateTestEventWithRRule(class.ID, "DTSTART:20240722T140000Z\nRRULE:FREQ=WEEKLY;BYDAY=TU,TH", instructor.ID)
	require.NoError(t, err)

	rule1, err := env.GetEventRecurrenceRule(event1.ID)
	require.NoError(t, err)
	require.Empty(t, programs.GetUntilDateFromRule(rule1))

	rule2, err := env.GetEventRecurrenceRule(event2.ID)
	require.NoError(t, err)
	require.Empty(t, programs.GetUntilDateFromRule(rule2))

	completionTime := time.Now()
	updateData := map[string]any{
		"status": string(programs.Completed),
	}

	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", class.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	updatedRule1, err := env.GetEventRecurrenceRule(event1.ID)
	require.NoError(t, err)
	require.Contains(t, updatedRule1, "UNTIL=")

	updatedRule2, err := env.GetEventRecurrenceRule(event2.ID)
	require.NoError(t, err)
	require.Contains(t, updatedRule2, "UNTIL=")

	untilDate1 := programs.GetUntilDateFromRule(updatedRule1)
	untilDate2 := programs.GetUntilDateFromRule(updatedRule2)

	parsedUntil1, err := time.Parse("20060102T150405Z", untilDate1)
	require.NoError(t, err)
	parsedUntil2, err := time.Parse("20060102T150405Z", untilDate2)
	require.NoError(t, err)

	expectedDay := time.Date(completionTime.Year(), completionTime.Month(), completionTime.Day(), 23, 59, 59, 0, time.UTC)
	nextDay := expectedDay.AddDate(0, 0, 1)

	require.True(t, parsedUntil1.Equal(expectedDay) || parsedUntil1.Equal(nextDay))
	require.True(t, parsedUntil2.Equal(expectedDay) || parsedUntil2.Equal(nextDay))
	require.True(t, strings.HasSuffix(untilDate1, "T235959Z"))
	require.True(t, strings.HasSuffix(untilDate2, "T235959Z"))
}

func TestExistingUntilDateReplaced(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Test Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("admin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "rrule")
	require.NoError(t, err)

	class, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	futureDate := time.Now().AddDate(1, 0, 0)
	existingUntilDate := programs.FormatDateForUntil(futureDate)
	ruleWithExistingUntil := fmt.Sprintf("DTSTART:20240721T100000Z\nRRULE:FREQ=WEEKLY;BYDAY=TU,TH;UNTIL=%s", existingUntilDate)

	event, err := env.CreateTestEventWithRRule(class.ID, ruleWithExistingUntil, instructor.ID)
	require.NoError(t, err)

	originalRule, err := env.GetEventRecurrenceRule(event.ID)
	require.NoError(t, err)
	require.Contains(t, originalRule, "UNTIL="+existingUntilDate)

	completionTime := time.Now()
	updateData := map[string]any{
		"status": string(programs.Completed),
	}

	NewRequest[any](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/program-classes?id=%d", class.ID), updateData).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: facilityAdmin.ID, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK)

	updatedRule, err := env.GetEventRecurrenceRule(event.ID)
	require.NoError(t, err)

	newUntilDate := programs.GetUntilDateFromRule(updatedRule)

	parsedNewUntil, err := time.Parse("20060102T150405Z", newUntilDate)
	require.NoError(t, err)

	expectedDay := time.Date(completionTime.Year(), completionTime.Month(), completionTime.Day(), 23, 59, 59, 0, time.UTC)
	nextDay := expectedDay.AddDate(0, 0, 1)

	require.True(t, parsedNewUntil.Equal(expectedDay) || parsedNewUntil.Equal(nextDay))
	require.True(t, strings.HasSuffix(newUntilDate, "T235959Z"))
	require.NotEqual(t, existingUntilDate, newUntilDate)
	require.NotContains(t, updatedRule, existingUntilDate)
}
