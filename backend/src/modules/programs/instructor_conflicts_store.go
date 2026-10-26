package programs

import (
	"fmt"
	"sort"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/teambition/rrule-go"
)

func checkInstructorRRuleConflicts(db *Store, w *conflictWindow, req *ConflictCheckRequest) ([]RoomConflict, error) {
	bookings, err := db.getInstructorBookingsInRange(req.FacilityID, req.InstructorID, w.rangeStart, w.rangeEnd, w.facilityTZ)
	if err != nil {
		return nil, err
	}
	return buildConflictsFromBookings(db, w, bookings, req, ConflictTypeInstructor), nil
}

func (db *Store) getInstructorBookingsInRange(facilityID, instructorID uint, rangeStart, rangeEnd time.Time, facilityTimezone string) ([]RoomBooking, error) {
	var events []ProgramClassEvent
	if err := db.Table("program_class_events e").
		Select("DISTINCT e.*").
		Joins("JOIN program_class_cohorts c ON c.id = e.cohort_id").
		Joins("LEFT JOIN program_class_event_overrides o ON o.event_id = e.id AND o.deleted_at IS NULL").
		Where("c.facility_id = ? AND e.deleted_at IS NULL AND (e.instructor_id = ? OR o.instructor_id = ?)", facilityID, instructorID, instructorID).
		Preload("Overrides").
		Find(&events).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_events")
	}

	var bookings []RoomBooking
	for _, event := range events {
		eventBookings, err := expandEventToInstructorBookings(event, rangeStart, rangeEnd, instructorID, facilityTimezone)
		if err != nil {
			return nil, err
		}
		bookings = append(bookings, eventBookings...)
	}
	return bookings, nil
}

func expandEventToInstructorBookings(event ProgramClassEvent, rangeStart, rangeEnd time.Time, targetInstructorID uint, facilityTimezone string) ([]RoomBooking, error) {
	var bookings []RoomBooking

	rule, err := event.GetRRuleWithTimezone(facilityTimezone)
	if err != nil {
		return nil, fmt.Errorf("invalid recurrence rule for event %d: %w", event.ID, err)
	}

	duration, err := time.ParseDuration(event.Duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration '%s' for event %d: %w", event.Duration, event.ID, err)
	}

	cancelledDates := make(map[string]bool)
	rescheduledDates := make(map[string]ProgramClassEventOverride)
	for _, override := range event.Overrides {
		overrideRule, err := rrule.StrToRRule(override.OverrideRrule)
		if err != nil {
			logrus.Warnf("skipping override %d with invalid rrule: %v", override.ID, err)
			continue
		}
		for _, occ := range overrideRule.Between(rangeStart, rangeEnd, true) {
			dateKey := occ.Format("2006-01-02")
			if override.IsCancelled {
				cancelledDates[dateKey] = true
			} else {
				rescheduledDates[dateKey] = override
			}
		}
	}

	if event.InstructorID != nil && *event.InstructorID == targetInstructorID {
		for _, occ := range rule.Between(rangeStart, rangeEnd, true) {
			dateKey := occ.Format("2006-01-02")
			if cancelledDates[dateKey] {
				continue
			}
			if _, rescheduled := rescheduledDates[dateKey]; rescheduled {
				continue
			}
			bookings = append(bookings, RoomBooking{
				StartTime: occ,
				EndTime:   occ.Add(duration),
				EventID:   event.ID,
				ClassID:   event.CohortID,
			})
		}
	}

	for _, override := range event.Overrides {
		if override.IsCancelled {
			continue
		}
		effectiveInstructor := event.InstructorID
		if override.InstructorID != nil {
			effectiveInstructor = override.InstructorID
		}
		if effectiveInstructor == nil || *effectiveInstructor != targetInstructorID {
			continue
		}
		overrideRule, err := rrule.StrToRRule(override.OverrideRrule)
		if err != nil {
			logrus.Warnf("skipping override %d with invalid rrule: %v", override.ID, err)
			continue
		}
		overrideDuration, err := time.ParseDuration(override.Duration)
		if err != nil {
			overrideDuration = duration
		}
		for _, occ := range overrideRule.Between(rangeStart, rangeEnd, true) {
			bookings = append(bookings, RoomBooking{
				StartTime:  occ,
				EndTime:    occ.Add(overrideDuration),
				EventID:    event.ID,
				ClassID:    event.CohortID,
				IsOverride: true,
			})
		}
	}

	sort.Slice(bookings, func(i, j int) bool {
		return bookings[i].StartTime.Before(bookings[j].StartTime)
	})

	return bookings, nil
}
