package programs

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/teambition/rrule-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (db *Store) GetRoomsForFacility(facilityID uint) ([]Room, error) {
	var rooms []Room
	if err := db.Where("facility_id = ?", facilityID).Order("name ASC").Find(&rooms).Error; err != nil {
		return nil, newGetRecordsDBError(err, "rooms")
	}
	return rooms, nil
}

func (db *Store) GetRoomByIDForFacility(roomID, facilityID uint) (*Room, error) {
	var room Room
	if err := db.Where("id = ? AND facility_id = ?", roomID, facilityID).First(&room).Error; err != nil {
		return nil, newNotFoundDBError(err, "rooms")
	}
	return &room, nil
}

func (db *Store) CreateRoom(room *Room) (*Room, error) {
	if err := Validate().Struct(room); err != nil {
		return nil, newCreateDBError(err, "room")
	}
	if err := db.Create(room).Error; err != nil {
		return nil, newCreateDBError(err, "room")
	}
	return room, nil
}

func LockAndCheckConflicts(tx *gorm.DB, req *ConflictCheckRequest) ([]RoomConflict, error) {
	db := &Store{tx}
	if req.RoomID != 0 {
		var room Room
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&room, req.RoomID).Error; err != nil {
			return nil, newNotFoundDBError(err, "room")
		}
	}
	// The instructor isn't locked: users is a core table, and Programs' role
	// can read it but not lock its rows.
	if req.InstructorID != 0 {
		var instructor User
		if err := tx.First(&instructor, req.InstructorID).Error; err != nil {
			return nil, newNotFoundDBError(err, "instructor")
		}
	}
	return db.CheckConflicts(req)
}

const maxConflictsToReturn = 50

func (db *Store) CheckConflicts(req *ConflictCheckRequest) ([]RoomConflict, error) {
	if req.RoomID == 0 && req.InstructorID == 0 {
		return nil, nil
	}
	w, err := db.prepareConflictWindow(req)
	if err != nil {
		return nil, err
	}
	var conflicts []RoomConflict
	if req.RoomID != 0 {
		roomConflicts, err := checkRoomRRuleConflicts(db, w, req)
		if err != nil {
			return nil, err
		}
		conflicts = append(conflicts, roomConflicts...)
	}
	if req.InstructorID != 0 {
		instructorConflicts, err := checkInstructorRRuleConflicts(db, w, req)
		if err != nil {
			return nil, err
		}
		conflicts = append(conflicts, instructorConflicts...)
	}
	return conflicts, nil
}

func (db *Store) CheckRRuleConflicts(req *ConflictCheckRequest) ([]RoomConflict, error) {
	w, err := db.prepareConflictWindow(req)
	if err != nil {
		return nil, err
	}
	return checkRoomRRuleConflicts(db, w, req)
}

type conflictWindow struct {
	occurrences []time.Time
	duration    time.Duration
	tz          *time.Location
	facilityTZ  string
	rangeStart  time.Time
	rangeEnd    time.Time
}

func (db *Store) prepareConflictWindow(req *ConflictCheckRequest) (*conflictWindow, error) {
	if req.RecurrenceRule == "" {
		return nil, newDBError(errors.New("recurrence rule is required"), "invalid conflict check request")
	}
	if req.Duration == "" {
		return nil, newDBError(errors.New("duration is required"), "invalid conflict check request")
	}

	var facility Facility
	if err := db.Select("timezone").First(&facility, req.FacilityID).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facility")
	}

	tz, err := time.LoadLocation(facility.Timezone)
	if err != nil {
		tz = time.UTC
	}

	tempEvent := &ProgramClassEvent{RecurrenceRule: req.RecurrenceRule}
	rule, err := tempEvent.GetRRuleWithTimezone(facility.Timezone)
	if err != nil {
		return nil, newDBError(err, "invalid recurrence rule")
	}

	duration, err := time.ParseDuration(req.Duration)
	if err != nil {
		return nil, newDBError(err, "invalid duration")
	}

	now := time.Now()
	rangeStart := rule.GetDTStart()
	if rangeStart.Before(now.AddDate(-1, 0, 0)) {
		rangeStart = now.AddDate(-1, 0, 0)
	}
	until := rule.GetUntil()
	if until.IsZero() || until.Before(rangeStart) {
		until = rangeStart.AddDate(1, 0, 0)
	}
	if until.After(rangeStart.AddDate(1, 0, 0)) {
		until = rangeStart.AddDate(1, 0, 0)
	}

	return &conflictWindow{
		occurrences: rule.Between(rangeStart, until, true),
		duration:    duration,
		tz:          tz,
		facilityTZ:  facility.Timezone,
		rangeStart:  rangeStart,
		rangeEnd:    until,
	}, nil
}

func buildConflictsFromBookings(db *Store, w *conflictWindow, bookings []RoomBooking, req *ConflictCheckRequest, conflictType string) []RoomConflict {
	var conflicts []RoomConflict

	classIDs := make(map[uint]struct{})
	for _, booking := range bookings {
		classIDs[booking.ClassID] = struct{}{}
	}

	classNamesCache := make(map[uint]string)
	if len(classIDs) > 0 {
		ids := make([]uint, 0, len(classIDs))
		for id := range classIDs {
			ids = append(ids, id)
		}
		// The displayed name lives on the parent class, so this has to join -- a cohort
		// has no name of its own. Both tables have a `name`, so qualify every column.
		var classes []ProgramClassCohort
		if err := db.Model(&ProgramClassCohort{}).
			Select("program_class_cohorts.id, pc.name AS class_name").
			Joins("JOIN program_classes pc ON pc.id = program_class_cohorts.class_id").
			Where("program_class_cohorts.id IN ?", ids).
			Find(&classes).Error; err != nil {
			logrus.Warnf("failed to batch fetch class names for conflict display: %v", err)
		}
		for _, c := range classes {
			classNamesCache[c.ID] = c.ClassName
		}
	}

	for _, occ := range w.occurrences {
		if len(conflicts) >= maxConflictsToReturn {
			break
		}
		endTime := occ.Add(w.duration)
		for _, booking := range bookings {
			if req.ExcludeEventID != nil && booking.EventID == *req.ExcludeEventID {
				continue
			}
			if req.ExcludeClassID != nil && booking.ClassID == *req.ExcludeClassID {
				continue
			}
			if hasLocalTimeOverlap(occ, endTime, booking.StartTime, booking.EndTime, w.tz) {
				className := classNamesCache[booking.ClassID]
				if className == "" {
					className = "Unknown Class"
				}
				conflicts = append(conflicts, RoomConflict{
					ConflictingEventID: booking.EventID,
					ConflictingClassID: booking.ClassID,
					ClassName:          className,
					StartTime:          booking.StartTime,
					EndTime:            booking.EndTime,
					ConflictType:       conflictType,
				})
				if len(conflicts) >= maxConflictsToReturn {
					break
				}
			}
		}
	}

	return conflicts
}

func checkRoomRRuleConflicts(db *Store, w *conflictWindow, req *ConflictCheckRequest) ([]RoomConflict, error) {
	bookings, err := db.getRoomBookingsInRange(req.FacilityID, req.RoomID, w.rangeStart, w.rangeEnd, w.facilityTZ)
	if err != nil {
		return nil, err
	}
	return buildConflictsFromBookings(db, w, bookings, req, ConflictTypeRoom), nil
}

func (db *Store) getRoomBookingsInRange(facilityID, roomID uint, rangeStart, rangeEnd time.Time, facilityTimezone string) ([]RoomBooking, error) {
	var events []ProgramClassEvent
	if err := db.Table("program_class_events e").
		Select("DISTINCT e.*").
		Joins("JOIN program_class_cohorts c ON c.id = e.cohort_id").
		Joins("LEFT JOIN program_class_event_overrides o ON o.event_id = e.id AND o.deleted_at IS NULL").
		Where("c.facility_id = ? AND e.deleted_at IS NULL AND (e.room_id = ? OR o.room_id = ?)", facilityID, roomID, roomID).
		Preload("Overrides").
		Find(&events).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_events")
	}

	var bookings []RoomBooking
	for _, event := range events {
		eventBookings, err := expandEventToBookings(event, rangeStart, rangeEnd, roomID, facilityTimezone)
		if err != nil {
			return nil, err
		}
		bookings = append(bookings, eventBookings...)
	}
	return bookings, nil
}

func expandEventToBookings(event ProgramClassEvent, rangeStart, rangeEnd time.Time, targetRoomID uint, facilityTimezone string) ([]RoomBooking, error) {
	var bookings []RoomBooking

	if event.RoomID == nil {
		return bookings, nil
	}

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
		overrideOccurrences := overrideRule.Between(rangeStart, rangeEnd, true)
		for _, occ := range overrideOccurrences {
			dateKey := occ.Format("2006-01-02")
			if override.IsCancelled {
				cancelledDates[dateKey] = true
			} else {
				rescheduledDates[dateKey] = override
			}
		}
	}

	occurrences := rule.Between(rangeStart, rangeEnd, true)
	for _, occ := range occurrences {
		dateKey := occ.Format("2006-01-02")
		if cancelledDates[dateKey] {
			continue
		}
		if _, rescheduled := rescheduledDates[dateKey]; rescheduled {
			continue
		}
		if *event.RoomID != targetRoomID {
			continue
		}
		bookings = append(bookings, RoomBooking{
			RoomID:     *event.RoomID,
			StartTime:  occ,
			EndTime:    occ.Add(duration),
			EventID:    event.ID,
			ClassID:    event.CohortID,
			IsOverride: false,
		})
	}

	for _, override := range event.Overrides {
		if override.IsCancelled {
			continue
		}
		overrideRoomID := event.RoomID
		if override.RoomID != nil {
			overrideRoomID = override.RoomID
		}
		if overrideRoomID == nil || *overrideRoomID != targetRoomID {
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
				RoomID:     *overrideRoomID,
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

func hasLocalTimeOverlap(start1, end1, start2, end2 time.Time, tz *time.Location) bool {
	local1Start := start1.In(tz)
	local1End := end1.In(tz)
	local2Start := start2.In(tz)
	local2End := end2.In(tz)
	if local1Start.YearDay() != local2Start.YearDay() || local1Start.Year() != local2Start.Year() {
		return false
	}
	t1Start := local1Start.Hour()*60 + local1Start.Minute()
	t1End := local1End.Hour()*60 + local1End.Minute()
	t2Start := local2Start.Hour()*60 + local2Start.Minute()
	t2End := local2End.Hour()*60 + local2End.Minute()
	return t1Start < t2End && t2Start < t1End
}
