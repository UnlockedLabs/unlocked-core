package programs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/teambition/rrule-go"
)

func TestGenerateEventInstances_SimpleRecurrence_NoOverrides(t *testing.T) {
	rule, err := rrule.NewRRule(rrule.ROption{
		Freq:    rrule.WEEKLY,
		Count:   5,
		Dtstart: time.Date(2024, 9, 2, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal("failed to create rrule", err)
	}
	event := ProgramClassEvent{
		CohortID:       100,
		Duration:       time.Hour.String(),
		RecurrenceRule: rule.String(),
		Overrides:      []ProgramClassEventOverride{},
	}

	startDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 9, 30, 23, 59, 59, 0, time.UTC)

	instances := generateEventInstances(event, startDate, endDate)

	expectedDates := []time.Time{
		time.Date(2024, 9, 2, 10, 0, 0, 0, time.UTC),
		time.Date(2024, 9, 9, 10, 0, 0, 0, time.UTC),
		time.Date(2024, 9, 16, 10, 0, 0, 0, time.UTC),
		time.Date(2024, 9, 23, 10, 0, 0, 0, time.UTC),
		time.Date(2024, 9, 30, 10, 0, 0, 0, time.UTC),
	}

	assert.Len(t, instances, len(expectedDates), "Should generate 5 instances")

	for i, instance := range instances {
		assert.Equal(t, event.ID, instance.EventID, "EventID should match")
		assert.Equal(t, event.CohortID, instance.CohortID, "CohortID should match")
		assert.Equal(t, expectedDates[i], instance.StartTime, "StartTime should match")
		assert.Equal(t, event.Duration, instance.Duration.String(), "Duration should match")
		assert.False(t, instance.IsCancelled, "IsCancelled should be false")
	}
}

func TestGenerateEventInstances_CancellationOverride(t *testing.T) {
	event := ProgramClassEvent{
		CohortID: 200,
		Duration: time.Hour.String(),
		RecurrenceRule: "DTSTART:20240901T090000Z\n" +
			"RRULE:FREQ=DAILY;COUNT=7",
		Overrides: []ProgramClassEventOverride{
			{
				EventID:       2,
				OverrideRrule: "DTSTART:20240904T090000Z\nRRULE:FREQ=DAILY;COUNT=1",
				IsCancelled:   true,
			},
		},
	}

	startDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 9, 7, 23, 59, 59, 0, time.UTC)

	instances := generateEventInstances(event, startDate, endDate)

	expectedDates := []time.Time{
		time.Date(2024, 9, 1, 9, 0, 0, 0, time.UTC),
		time.Date(2024, 9, 2, 9, 0, 0, 0, time.UTC),
		time.Date(2024, 9, 3, 9, 0, 0, 0, time.UTC),
		// Skipping September 4 due to cancellation
		time.Date(2024, 9, 5, 9, 0, 0, 0, time.UTC),
		time.Date(2024, 9, 6, 9, 0, 0, 0, time.UTC),
		time.Date(2024, 9, 7, 9, 0, 0, 0, time.UTC),
	}

	assert.Len(t, instances, len(expectedDates), "Should generate 6 instances after cancellation")

	for i, instance := range instances {
		assert.Equal(t, event.ID, instance.EventID, "EventID should match")
		assert.Equal(t, expectedDates[i], instance.StartTime, "StartTime should match")
		assert.False(t, instance.IsCancelled, "IsCancelled should be false")
	}
}

func TestGenerateEventInstances_ModificationOverride(t *testing.T) {
	newDuration := 90 * time.Minute
	rule, err := rrule.NewRRule(rrule.ROption{
		Dtstart:   time.Date(2024, 9, 1, 10, 0, 0, 0, time.UTC),
		Freq:      rrule.WEEKLY,
		Count:     4,
		Byweekday: []rrule.Weekday{rrule.SU},
	})
	if err != nil {
		t.Fatalf("failed to create rrule: %v", err)
	}
	overRule, err := rrule.NewRRule(rrule.ROption{
		Dtstart:   time.Date(2024, 9, 15, 10, 0, 0, 0, time.UTC),
		Freq:      rrule.WEEKLY,
		Byweekday: []rrule.Weekday{rrule.SU},
	})
	if err != nil {
		t.Fatalf("failed to create rrule: %v", err)
	}
	event := ProgramClassEvent{
		CohortID:       300,
		Duration:       time.Hour.String(),
		RecurrenceRule: rule.String(),
		Overrides: []ProgramClassEventOverride{
			{
				EventID:       3,
				OverrideRrule: overRule.String(),
				IsCancelled:   false,
				Duration:      newDuration.String(),
			},
		},
	}

	startDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 9, 28, 23, 59, 59, 0, time.UTC)

	instances := generateEventInstances(event, startDate, endDate)

	expectedInstances := []EventInstance{
		{
			EventID:     event.ID,
			StartTime:   time.Date(2024, 9, 1, 10, 0, 0, 0, time.UTC),
			Duration:    time.Duration(1 * time.Hour),
			IsCancelled: false,
		},
		{
			EventID:     event.ID,
			StartTime:   time.Date(2024, 9, 8, 10, 0, 0, 0, time.UTC),
			Duration:    time.Duration(1 * time.Hour),
			IsCancelled: false,
		},
		{
			EventID:     event.ID,
			StartTime:   time.Date(2024, 9, 15, 10, 0, 0, 0, time.UTC),
			Duration:    newDuration,
			IsCancelled: false,
		},
		{
			EventID:     event.ID,
			StartTime:   time.Date(2024, 9, 22, 10, 0, 0, 0, time.UTC),
			Duration:    newDuration,
			IsCancelled: false,
		},
	}

	assert.Len(t, instances, len(expectedInstances), "Should generate 4 instances with modified duration from September 15")

	for i, instance := range instances {
		expected := expectedInstances[i]
		assert.Equal(t, expected.EventID, instance.EventID, "EventID should match")
		assert.Equal(t, expected.StartTime, instance.StartTime, "StartTime should match")
		assert.Equal(t, expected.Duration, instance.Duration, "Duration should match")
		assert.False(t, instance.IsCancelled, "IsCancelled should be false")
	}
}

func TestGenerateEventInstances_MultipleOverrides(t *testing.T) {
	newDuration := 2 * time.Hour
	rule, err := rrule.NewRRule(rrule.ROption{
		Dtstart: time.Date(2024, 9, 1, 8, 0, 0, 0, time.UTC),
		Freq:    rrule.DAILY,
		Count:   7,
	})
	if err != nil {
		t.Fatal("failed to create rrule", err)
	}

	rule1, err := rrule.NewRRule(rrule.ROption{
		Dtstart: time.Date(2024, 9, 3, 8, 0, 0, 0, time.UTC),
		Freq:    rrule.DAILY,
		Count:   1,
	})
	if err != nil {
		t.Fatal("failed to create rrule", err)
	}
	override1 := ProgramClassEventOverride{
		EventID:       4,
		OverrideRrule: rule1.String(),
		IsCancelled:   true,
	}

	rule2, err := rrule.NewRRule(rrule.ROption{
		Dtstart: time.Date(2024, 9, 5, 8, 0, 0, 0, time.UTC),
		Freq:    rrule.DAILY,
		Until:   time.Date(2024, 9, 7, 8, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal("failed to create rrule", err)
	}
	override2 := ProgramClassEventOverride{
		EventID:       4,
		IsCancelled:   false,
		Duration:      newDuration.String(),
		OverrideRrule: rule2.String(),
	}
	event := ProgramClassEvent{
		CohortID:       400,
		Duration:       time.Hour.String(),
		RecurrenceRule: rule.String(),
		Overrides:      []ProgramClassEventOverride{override1, override2},
	}
	startDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 9, 7, 23, 59, 59, 0, time.UTC)
	instances := generateEventInstances(event, startDate, endDate)

	expectedInstances := []EventInstance{
		{
			EventID:     event.ID,
			StartTime:   time.Date(2024, 9, 1, 8, 0, 0, 0, time.UTC),
			Duration:    time.Duration(1 * time.Hour),
			IsCancelled: false,
		},
		{
			EventID:     event.ID,
			StartTime:   time.Date(2024, 9, 2, 8, 0, 0, 0, time.UTC),
			Duration:    time.Duration(1 * time.Hour),
			IsCancelled: false,
		},
		// Skipping September 3 due to cancellation
		{
			EventID:     event.ID,
			StartTime:   time.Date(2024, 9, 4, 8, 0, 0, 0, time.UTC),
			Duration:    time.Duration(1 * time.Hour),
			IsCancelled: false,
		},
		{
			EventID:     event.ID,
			StartTime:   time.Date(2024, 9, 5, 8, 0, 0, 0, time.UTC),
			Duration:    newDuration,
			IsCancelled: false,
		},
		{
			EventID:     event.ID,
			StartTime:   time.Date(2024, 9, 6, 8, 0, 0, 0, time.UTC),
			Duration:    newDuration,
			IsCancelled: false,
		},
		{
			EventID:     event.ID,
			StartTime:   time.Date(2024, 9, 7, 8, 0, 0, 0, time.UTC),
			Duration:    newDuration, // Modified duration
			IsCancelled: false,
		},
	}

	assert.Len(t, instances, len(expectedInstances), "Should generate 6 instances with cancellations and modifications")

	for i, instance := range instances {
		expected := expectedInstances[i]
		assert.Equal(t, expected.EventID, instance.EventID, "EventID should match")
		assert.Equal(t, expected.StartTime, instance.StartTime, "StartTime should match")
		assert.Equal(t, expected.Duration, instance.Duration, "Duration should match")
		assert.False(t, instance.IsCancelled, "IsCancelled should be false")
	}
}

func TestGenerateEventInstances_NoOccurrencesInRange(t *testing.T) {
	rule, err := rrule.NewRRule(rrule.ROption{
		Dtstart: time.Date(2024, 10, 1, 8, 0, 0, 0, time.UTC),
		Freq:    rrule.DAILY,
		Count:   5,
	})
	if err != nil {
		t.Fatal("failed to create rrule", err)
	}
	event := ProgramClassEvent{
		CohortID:       500,
		Duration:       time.Hour.String(),
		RecurrenceRule: rule.String(),
		Overrides:      []ProgramClassEventOverride{},
	}

	startDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 9, 30, 23, 59, 59, 0, time.UTC)

	instances := generateEventInstances(event, startDate, endDate)
	assert.Len(t, instances, 0, "Should generate no instances as event occurs outside the date range")
}

func TestGenerateEventInstances_InvalidRRULE(t *testing.T) {
	event := ProgramClassEvent{
		CohortID:       600,
		Duration:       time.Hour.String(),
		RecurrenceRule: "INVALID_RRULE",
		Overrides:      []ProgramClassEventOverride{},
	}

	startDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 9, 30, 23, 59, 59, 0, time.UTC)
	instances := generateEventInstances(event, startDate, endDate)
	assert.Len(t, instances, 0, "Instances should be nil when an error occurs")
}

// dedupeClassEventInstances merges instances expanded from multiple event rows (the
// state left behind by "Apply to all future sessions"). It must keep distinct
// sessions, collapse true duplicates, and let the newest row win on a collision.
func TestDedupeClassEventInstances_PrefersNewestRowOnCollision(t *testing.T) {
	// EventID 1 is the older capped row, EventID 2 is the newer row. Both produce the
	// same date and start time — the newest row should win.
	instances := []ClassEventInstance{
		{EventID: 1, Date: "2026-01-05", ClassTime: "10:00-11:00"},
		{EventID: 2, Date: "2026-01-05", ClassTime: "10:00-11:00"},
		{EventID: 1, Date: "2026-01-12", ClassTime: "10:00-11:00"},
	}

	result := dedupeClassEventInstances(instances)

	assert.Len(t, result, 2, "duplicate date+time should collapse to one instance")
	// Sorted by date descending.
	assert.Equal(t, "2026-01-12", result[0].Date)
	assert.Equal(t, "2026-01-05", result[1].Date)
	assert.Equal(t, uint(2), result[1].EventID, "newest row should win on collision")
}

func TestDedupeClassEventInstances_KeepsDistinctTimesSameDate(t *testing.T) {
	instances := []ClassEventInstance{
		{EventID: 1, Date: "2026-01-05", ClassTime: "09:00-10:00"},
		{EventID: 1, Date: "2026-01-05", ClassTime: "13:00-14:00"},
	}

	result := dedupeClassEventInstances(instances)

	assert.Len(t, result, 2, "different start times on the same date are distinct sessions")
}

func TestDedupeClassEventInstances_MergesAcrossRowsWithoutGap(t *testing.T) {
	// Simulates the bug: an older capped row's past sessions plus a newer row's
	// future sessions must all survive the merge.
	pastFromOldRow := []ClassEventInstance{
		{EventID: 1, Date: "2026-01-05", ClassTime: "10:00-11:00"},
		{EventID: 1, Date: "2026-01-12", ClassTime: "10:00-11:00"},
	}
	futureFromNewRow := []ClassEventInstance{
		{EventID: 2, Date: "2026-01-19", ClassTime: "14:00-15:00"},
		{EventID: 2, Date: "2026-01-26", ClassTime: "14:00-15:00"},
	}

	result := dedupeClassEventInstances(append(pastFromOldRow, futureFromNewRow...))

	dates := make([]string, len(result))
	for i, inst := range result {
		dates[i] = inst.Date
	}
	assert.Equal(t, []string{"2026-01-26", "2026-01-19", "2026-01-12", "2026-01-05"}, dates,
		"all sessions from both rows should be present, sorted by date descending")
}

func TestDedupeEventDates_PrefersNewestRowOnCollision(t *testing.T) {
	dates := []EventDates{
		{EventID: 1, Date: "2026-01-05", ClassTime: "10:00-11:00"},
		{EventID: 2, Date: "2026-01-05", ClassTime: "10:00-11:00"},
		{EventID: 1, Date: "2026-01-12", ClassTime: "10:00-11:00"},
	}

	result := dedupeEventDates(dates)

	assert.Len(t, result, 2, "duplicate date+classTime should collapse to one entry")
	foundCollisionDate := false
	for _, d := range result {
		if d.Date == "2026-01-05" {
			foundCollisionDate = true
			assert.Equal(t, uint(2), d.EventID, "newest row should win on collision")
		}
	}
	assert.True(t, foundCollisionDate, "expected deduped result to include 2026-01-05")
}

func TestDedupeEventDates_KeepsDistinctTimesSameDate(t *testing.T) {
	dates := []EventDates{
		{EventID: 1, Date: "2026-01-05", ClassTime: "09:00-10:00"},
		{EventID: 1, Date: "2026-01-05", ClassTime: "13:00-14:00"},
	}

	result := dedupeEventDates(dates)

	assert.Len(t, result, 2, "different class times on the same date are distinct entries")
}
