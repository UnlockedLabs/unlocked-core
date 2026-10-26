package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

// TestMultiRowEventSessionsAllSurface reproduces ID-692: after "Apply to all future
// sessions" a class ends up with two ProgramClassEvent rows (an UNTIL-capped original
// plus a new row). The sessions tab must show sessions from BOTH rows, not just the
// newest one.
func TestMultiRowEventSessionsAllSurface(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Multi-Row Facility")
	require.NoError(t, err)

	admin, err := env.CreateTestUser("multirowadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Multi-Row Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)
	require.NoError(t, env.SetFacilitiesToProgram(program.ID, []uint{facility.ID}))

	class, err := env.CreateTestClass(program, facility, programs.Active, &admin.ID)
	require.NoError(t, err)

	now := time.Now().UTC()
	fmtTS := func(t time.Time) string { return t.Format("20060102T150405Z") }

	// Original row: weekly, started 8 weeks ago, capped (UNTIL) 4 weeks ago.
	oldStart := now.AddDate(0, 0, -56)
	oldUntil := now.AddDate(0, 0, -28)
	oldRule := fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=WEEKLY;UNTIL=%s", fmtTS(oldStart), fmtTS(oldUntil))
	_, err = env.CreateTestEventWithRRule(class.ID, oldRule, admin.ID)
	require.NoError(t, err)

	// New row created by "Apply to all future sessions": weekly from 3 weeks ago at a
	// different time, running 4 weeks into the future.
	newStart := now.AddDate(0, 0, -21).Truncate(time.Hour).Add(13 * time.Hour)
	newUntil := now.AddDate(0, 0, 28)
	newRule := fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=WEEKLY;UNTIL=%s", fmtTS(newStart), fmtTS(newUntil))
	_, err = env.CreateTestEventWithRRule(class.ID, newRule, admin.ID)
	require.NoError(t, err)

	qryCtx := &programs.QueryContext{
		Ctx:      env.Context,
		Timezone: "UTC",
		All:      true,
	}

	instances, err := env.Programs.GetClassEventInstancesWithAttendanceForRecurrence(int(class.ID), qryCtx, "", "", nil, true)
	require.NoError(t, err)
	require.NotEmpty(t, instances, "expected sessions from both event rows")

	var earliest, latest string
	for _, inst := range instances {
		if earliest == "" || inst.Date < earliest {
			earliest = inst.Date
		}
		if inst.Date > latest {
			latest = inst.Date
		}
	}

	// Past sessions from the capped original row must still be present (the bug dropped
	// these). The earliest session should be near the original 8-weeks-ago DTSTART.
	pastThreshold := now.AddDate(0, 0, -50).Format("2006-01-02")
	require.LessOrEqual(t, earliest, pastThreshold,
		"past sessions from the original capped row should still surface (earliest=%s)", earliest)

	// Future sessions from the new row must also be present.
	futureThreshold := now.AddDate(0, 0, 20).Format("2006-01-02")
	require.GreaterOrEqual(t, latest, futureThreshold,
		"future sessions from the new row should surface (latest=%s)", latest)
}

// TestCancelAllFutureSessionsAcrossMultipleRows reproduces the write-side twin of
// ID-692: when a class has multiple active recurrence rows, "cancel all future
// sessions" must cap/cancel every row, not just the clicked one. Previously stray
// rows kept generating active future sessions.
func TestCancelAllFutureSessionsAcrossMultipleRows(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Cancel-Future Facility")
	require.NoError(t, err)

	admin, err := env.CreateTestUser("cancelfutureadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Cancel-Future Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)
	require.NoError(t, env.SetFacilitiesToProgram(program.ID, []uint{facility.ID}))

	class, err := env.CreateTestClass(program, facility, programs.Active, &admin.ID)
	require.NoError(t, err)

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dstart := func(t time.Time) string { return t.Format("20060102") + "T090000Z" }
	until := func(t time.Time) string { return t.Format("20060102") + "T235959Z" }

	// Row A: the original series spanning past through future.
	ruleA := fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=WEEKLY;UNTIL=%s", dstart(today.AddDate(0, 0, -28)), until(today.AddDate(0, 0, 28)))
	rowA, err := env.CreateTestEventWithRRule(class.ID, ruleA, admin.ID)
	require.NoError(t, err)

	// Row B: a stray future-only row left behind by a prior reschedule. The old code
	// never touched this on a cancel, so its future sessions leaked through.
	ruleB := fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=WEEKLY;UNTIL=%s", dstart(today.AddDate(0, 0, 7)), until(today.AddDate(0, 0, 28)))
	_, err = env.CreateTestEventWithRRule(class.ID, ruleB, admin.ID)
	require.NoError(t, err)

	// "Cancel all future sessions" from today: a cancelled series from today onward,
	// and a closed series whose UNTIL marks the cut (today - 1).
	newSeries := programs.ProgramClassEvent{
		Duration:       "2h",
		RecurrenceRule: fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=WEEKLY;UNTIL=%s", dstart(today), until(today.AddDate(0, 0, 28))),
		RoomID:         rowA.RoomID,
		InstructorID:   rowA.InstructorID,
		IsCancelled:    true,
	}
	closedSeries := programs.ProgramClassEvent{
		RecurrenceRule: fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=WEEKLY;UNTIL=%s", dstart(today.AddDate(0, 0, -28)), until(today.AddDate(0, 0, -1))),
	}

	writeCtx := &programs.QueryContext{Ctx: env.Context, Timezone: "UTC", UserID: admin.ID}
	require.NoError(t, env.Programs.CreateRescheduleEventSeries(writeCtx, class.ID, newSeries, closedSeries))

	readCtx := &programs.QueryContext{Ctx: env.Context, Timezone: "UTC", All: true}
	instances, err := env.Programs.GetClassEventInstancesWithAttendanceForRecurrence(int(class.ID), readCtx, "", "", nil, true)
	require.NoError(t, err)
	require.NotEmpty(t, instances)

	todayStr := today.Format("2006-01-02")
	var futureCancelled, pastActive int
	for _, inst := range instances {
		if inst.Date >= todayStr {
			require.True(t, inst.IsCancelled,
				"every session on/after the cut must be cancelled, but %s is active", inst.Date)
			futureCancelled++
		} else if !inst.IsCancelled {
			pastActive++
		}
	}
	require.Positive(t, futureCancelled, "future sessions should appear as cancelled")
	require.Positive(t, pastActive, "past sessions before the cut should be preserved as active")

	// The stray future-only row (B) must be soft-deleted, not left generating sessions.
	var activeRows int64
	require.NoError(t, env.DB.Model(&programs.ProgramClassEvent{}).
		Where("cohort_id = ? AND is_cancelled = ?", class.ID, false).
		Count(&activeRows).Error)
	require.Equal(t, int64(1), activeRows, "only the capped original row should remain active")
}

// TestUncancelSeriesRestoresFutureSessions verifies that a series cancellation can be
// undone: the sessions-tab "Undo" routes to uncancel-series, which must bring the
// future sessions back as active.
func TestUncancelSeriesRestoresFutureSessions(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Uncancel Facility")
	require.NoError(t, err)

	admin, err := env.CreateTestUser("uncanceladmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Uncancel Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)
	require.NoError(t, env.SetFacilitiesToProgram(program.ID, []uint{facility.ID}))

	class, err := env.CreateTestClass(program, facility, programs.Active, &admin.ID)
	require.NoError(t, err)

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dstart := func(t time.Time) string { return t.Format("20060102") + "T090000Z" }
	until := func(t time.Time) string { return t.Format("20060102") + "T235959Z" }

	rowA, err := env.CreateTestEventWithRRule(class.ID,
		fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=WEEKLY;UNTIL=%s", dstart(today.AddDate(0, 0, -28)), until(today.AddDate(0, 0, 28))), admin.ID)
	require.NoError(t, err)

	writeCtx := &programs.QueryContext{Ctx: env.Context, Timezone: "UTC", UserID: admin.ID}
	require.NoError(t, env.Programs.CreateRescheduleEventSeries(writeCtx, class.ID,
		programs.ProgramClassEvent{
			Duration:       "2h",
			RecurrenceRule: fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=WEEKLY;UNTIL=%s", dstart(today), until(today.AddDate(0, 0, 28))),
			RoomID:         rowA.RoomID,
			InstructorID:   rowA.InstructorID,
			IsCancelled:    true,
		},
		programs.ProgramClassEvent{
			RecurrenceRule: fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=WEEKLY;UNTIL=%s", dstart(today.AddDate(0, 0, -28)), until(today.AddDate(0, 0, -1))),
		}))

	// Locate the cancelled series row created by the cancel.
	var cancelledSeries programs.ProgramClassEvent
	require.NoError(t, env.DB.Where("cohort_id = ? AND is_cancelled = ?", class.ID, true).
		Order("created_at DESC").First(&cancelledSeries).Error)

	// Undo: restore the series from today onward.
	todayStr := today.Format("2006-01-02")
	require.NoError(t, env.Programs.UncancelEventSeries(writeCtx, cancelledSeries.ID, todayStr))

	readCtx := &programs.QueryContext{Ctx: env.Context, Timezone: "UTC", All: true}
	instances, err := env.Programs.GetClassEventInstancesWithAttendanceForRecurrence(int(class.ID), readCtx, "", "", nil, true)
	require.NoError(t, err)

	var restoredFuture int
	for _, inst := range instances {
		if inst.Date >= todayStr {
			require.False(t, inst.IsCancelled,
				"session on/after the restore date should be active again, but %s is still cancelled", inst.Date)
			restoredFuture++
		}
	}
	require.Positive(t, restoredFuture, "future sessions should be restored as active")
}
