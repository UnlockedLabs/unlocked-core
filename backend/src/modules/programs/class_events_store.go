package programs

import (
	"cmp"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/teambition/rrule-go"
	"gorm.io/gorm"
)

/*
Everything stored in database will ALWAYS be in UTC
and converted when returning to the client
*/

func (db *Store) GetClassEvents(args *QueryContext, classId int) ([]ProgramClassEvent, error) {
	events := []ProgramClassEvent{}
	tx := db.Model(&ProgramClassEvent{}).Preload("Overrides").Where("cohort_id = ?", classId)
	if !args.All {
		if err := tx.Count(&args.Total).Error; err != nil {
			return nil, newGetRecordsDBError(err, "program_class_events")
		}
		tx = tx.Limit(args.PerPage).Offset(args.CalcOffset())
	}
	if err := tx.Find(&events).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_events")
	}
	return events, nil
}

func (db *Store) CreateNewEvent(classId int, form *ProgramClassEvent) (*ProgramClassEvent, error) {
	err := Validate().Struct(form)
	if err != nil {
		return nil, newCreateDBError(err, "program_class_event")
	}
	if err := db.Create(form).Error; err != nil {
		return nil, newCreateDBError(err, "program_class_event")
	}
	return form, nil
}

func (db *Store) GetEventById(eventId int) (*ProgramClassEvent, error) {
	event := ProgramClassEvent{}
	if err := db.Preload("Overrides").First(&event, eventId).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_event")
	}
	return &event, nil
}

var dtStartDateRegex = regexp.MustCompile(`DTSTART[^\n:]*:(\d{8})`)

func getRuleDTStartDate(rule string) string {
	m := dtStartDateRegex.FindStringSubmatch(rule)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// ruleStartsAfterUntil reports whether DTSTART falls after UNTIL (a dead, zero-occurrence row).
func ruleStartsAfterUntil(rule string) bool {
	start := getRuleDTStartDate(rule)
	until := GetUntilDateFromRule(rule)
	if start == "" || len(until) < 8 {
		return false
	}
	return start > until[:8]
}

// CreateRescheduleEventSeries applies a series-level reschedule or cancel from a cut
// date (closedSeries' UNTIL, = cut - 1). It reconciles every existing row so the new
// series owns all sessions on/after the cut: rows starting before the cut are capped to
// it, rows starting on/after it are soft-deleted. This stays correct when a class has
// multiple recurrence rows, not just the one being edited.
func (db *Store) CreateRescheduleEventSeries(ctx *QueryContext, classID uint, newSeries, closedSeries ProgramClassEvent) error {
	untilDate := GetUntilDateFromRule(closedSeries.RecurrenceRule)
	if len(untilDate) < 8 {
		return newDBError(errors.New("closed_event_series is missing a valid UNTIL date"), "invalid event series request")
	}
	cutMinusOne := untilDate[:8] // YYYYMMDD of (cut date - 1)

	tx := db.WithContext(ctx.Ctx).Begin()
	if tx.Error != nil {
		return newDBError(tx.Error, "unable to start the database transaction")
	}

	var existing []ProgramClassEvent
	if err := tx.Where("cohort_id = ?", classID).Find(&existing).Error; err != nil {
		tx.Rollback()
		return newGetRecordsDBError(err, "program_class_events")
	}

	for i := range existing {
		row := existing[i]
		if dtStart := getRuleDTStartDate(row.RecurrenceRule); dtStart != "" && dtStart > cutMinusOne {
			if err := tx.Delete(&ProgramClassEvent{}, row.ID).Error; err != nil {
				tx.Rollback()
				return newUpdateDBError(err, "program_class_events")
			}
			continue
		}
		// Skip rows that already end on/before the cut; only ever shorten, never extend.
		if existingUntil := GetUntilDateFromRule(row.RecurrenceRule); len(existingUntil) >= 8 && existingUntil[:8] <= cutMinusOne {
			continue
		}
		cappedRule := ReplaceOrAddUntilDate(row.RecurrenceRule, untilDate)
		if cappedRule == row.RecurrenceRule {
			continue
		}
		if err := tx.Model(&ProgramClassEvent{}).Where("id = ?", row.ID).Update("recurrence_rule", cappedRule).Error; err != nil {
			tx.Rollback()
			return newUpdateDBError(err, "program_class_events")
		}
	}

	// Create the new series from the cut, unless it would be empty (DTSTART after UNTIL).
	if newSeries.RecurrenceRule != "" && !ruleStartsAfterUntil(newSeries.RecurrenceRule) {
		newSeries.ID = 0
		newSeries.CohortID = classID
		logField := "event_rescheduled_series"
		if newSeries.IsCancelled {
			logField = "event_cancelled_series"
		}
		changeLogEntry := NewChangeLogEntry(TableNameCohort, logField, StringPtr(""), &newSeries.RecurrenceRule, classID, ctx.UserID)
		if err := tx.Create(&newSeries).Error; err != nil {
			tx.Rollback()
			return newCreateDBError(err, "program_class_events")
		}
		if err := tx.Create(&changeLogEntry).Error; err != nil {
			tx.Rollback()
			return newCreateDBError(err, "change_log_entries")
		}
	}

	if err := tx.Commit().Error; err != nil {
		return newDBError(err, "unable to commit the database transaction")
	}
	return nil
}

// UncancelEventSeries restores a series-level cancellation from restoreDate onwards.
// It closes the cancelled series with UNTIL = restoreDate-1 and creates a new
// non-cancelled series starting from restoreDate with the same recurrence pattern.
func (db *Store) UncancelEventSeries(ctx *QueryContext, eventID uint, restoreDate string) error {
	tx := db.WithContext(ctx.Ctx).Begin()
	if tx.Error != nil {
		return newDBError(tx.Error, "unable to start database transaction")
	}

	var cancelledSeries ProgramClassEvent
	if err := tx.First(&cancelledSeries, eventID).Error; err != nil {
		tx.Rollback()
		return newGetRecordsDBError(err, "program_class_events")
	}

	restoreDt, err := time.Parse("2006-01-02", restoreDate)
	if err != nil {
		tx.Rollback()
		return newDBError(err, "invalid restore_date")
	}
	untilStr := FormatDateForUntil(restoreDt.AddDate(0, 0, -1))

	// Split the recurrence rule into DTSTART and RRULE lines
	lines := strings.Split(cancelledSeries.RecurrenceRule, "\n")
	var dtStartLine, rruleLine string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "DTSTART"):
			dtStartLine = l
		case strings.HasPrefix(l, "RRULE:"):
			rruleLine = l
		}
	}

	// Close the cancelled series: add UNTIL to the RRULE line
	closedRrule := ReplaceOrAddUntilDate(rruleLine, untilStr)
	var closedRule string
	if dtStartLine != "" {
		closedRule = dtStartLine + "\n" + closedRrule
	} else {
		closedRule = closedRrule
	}
	if err := tx.Model(&ProgramClassEvent{}).Where("id = ?", eventID).
		Update("recurrence_rule", closedRule).Error; err != nil {
		tx.Rollback()
		return newUpdateDBError(err, "program_class_events")
	}

	// Build new non-cancelled series starting from restoreDate
	var newDtStartLine string
	if dtStartLine != "" {
		colonIdx := strings.LastIndex(dtStartLine, ":")
		if colonIdx >= 0 && colonIdx+9 <= len(dtStartLine) {
			newDtStartLine = dtStartLine[:colonIdx+1] + restoreDt.Format("20060102") + dtStartLine[colonIdx+9:]
		} else {
			newDtStartLine = dtStartLine
		}
	}
	var newSeriesRule string
	if newDtStartLine != "" {
		newSeriesRule = newDtStartLine + "\n" + rruleLine
	} else {
		newSeriesRule = rruleLine
	}

	newSeries := ProgramClassEvent{
		CohortID:       cancelledSeries.CohortID,
		Duration:       cancelledSeries.Duration,
		RecurrenceRule: newSeriesRule,
		RoomID:         cancelledSeries.RoomID,
		InstructorID:   cancelledSeries.InstructorID,
	}
	if err := tx.Create(&newSeries).Error; err != nil {
		tx.Rollback()
		return newCreateDBError(err, "program_class_events")
	}

	return tx.Commit().Error
}

// UncancelOverride flips an override from cancelled back to non-cancelled via a direct
// UPDATE. Used when undoing only the cancellation of a rescheduled class — the override
// stays in place (preserving the reschedule link) but is no longer cancelled.
func (db *Store) UncancelOverride(ctx *QueryContext, overrideID uint) error {
	return db.WithContext(ctx.Ctx).
		Model(&ProgramClassEventOverride{}).
		Where("id = ?", overrideID).
		Updates(map[string]interface{}{
			"is_cancelled": false,
			"reason":       "",
		}).Error
}

// Soft deletes ProgramClassEventOverride by its ID, removes any associated attendance records, and logs the change.
// If the ProgramClassEventOverride to be deleted has an exiting LinkedOverrideEventID (rescheduled event) that ProgramClassEventOverride is deleted
func (db *Store) DeleteOverrideEvent(args *QueryContext, eventID int, classID int, undoAppliedFuture bool) error {
	trans := db.WithContext(args.Ctx).Begin()
	if trans.Error != nil {
		return newDBError(trans.Error, "unable to start the database transaction")
	}

	var (
		changeLogEntry *ChangeLogEntry
		eventDate      *string
		eventIDs       = make([]uint, 0, 1)
		override       ProgramClassEventOverride
		err            error
	)

	if err := trans.Model(&ProgramClassEventOverride{}).Where("id = ?", eventID).Find(&override).Error; err != nil {
		trans.Rollback()
		return newGetRecordsDBError(err, "program_class_event_overrides")
	}

	// Collect all overrides in the chain for deletion.
	// Follows both directions: forward links (LinkedOverrideEventID) and reverse
	// links (overrides linking TO this one). BFS traverses the full chain so
	// undoing A in A→B→C deletes all three overrides.
	eventIDs = append(eventIDs, override.ID)
	visited := map[uint]bool{override.ID: true}
	queue := []uint{override.ID}
	// Seed the queue with the forward link if it exists
	if override.LinkedOverrideEventID != nil {
		fwd := *override.LinkedOverrideEventID
		if !visited[fwd] {
			visited[fwd] = true
			eventIDs = append(eventIDs, fwd)
			queue = append(queue, fwd)
		}
	}
	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]
		// Reverse links: overrides that point TO currentID
		var reverseLinked []ProgramClassEventOverride
		if err := trans.Where("linked_override_event_id = ?", currentID).Find(&reverseLinked).Error; err == nil {
			for _, rl := range reverseLinked {
				if !visited[rl.ID] {
					visited[rl.ID] = true
					eventIDs = append(eventIDs, rl.ID)
					queue = append(queue, rl.ID)
				}
			}
		}
		// Forward link: the override that currentID points to
		var current ProgramClassEventOverride
		if err := trans.Where("id = ?", currentID).First(&current).Error; err == nil {
			if current.LinkedOverrideEventID != nil {
				fwd := *current.LinkedOverrideEventID
				if !visited[fwd] {
					visited[fwd] = true
					eventIDs = append(eventIDs, fwd)
					queue = append(queue, fwd)
				}
			}
		}
	}

	eventDate, err = override.GetFormattedOverrideDate("2006-01-02")
	if err != nil {
		trans.Rollback()
		return newDBError(err, "unable to parse override date")
	}

	if err := deleteEventAttedanceByDate(trans, override.EventID, *eventDate); err != nil {
		trans.Rollback()
		return err
	}

	if err := trans.Delete(&ProgramClassEventOverride{}, "id IN (?)", eventIDs).Error; err != nil {
		trans.Rollback()
		return newDeleteDBError(err, "program class event override")
	}

	if err := db.syncClassDateBoundaries(trans, uint(classID)); err != nil {
		trans.Rollback()
		return err
	}

	changeLogEntry = NewChangeLogEntry(TableNameCohort, "event_restored", &override.OverrideRrule, StringPtr(""), uint(classID), args.UserID)
	if err := trans.Create(&changeLogEntry).Error; err != nil {
		trans.Rollback()
		return newCreateDBError(err, "change_log_entries")
	}

	if err := trans.Commit().Error; err != nil {
		return newDBError(err, "unable to commit the database transaction")
	}
	return nil
}

// Creates multiple ProgramClassEventOverride records within a single database transaction. It performs the following operations based upon conditions:
// - Cancels an event
// - Links two override events together for rescheduling purposes.
// - Deletes attendance records for events that have been rescheduled or cancelled.
// - Logs a single change
func (db *Store) CreateOverrideEvents(ctx *QueryContext, overrideEvents []*ProgramClassEventOverride) error {
	trans := db.WithContext(ctx.Ctx).Begin() //only need to pass context here, it will be used/shared within the transaction
	if trans.Error != nil {
		return newDBError(trans.Error, "unable to start the database transaction")
	}

	var (
		changeLogEntry   *ChangeLogEntry
		eventDate        *string
		err              error
		linkedOverrideID *uint
		isOverrideUpdate bool
	)

	changeLogEntry = NewChangeLogEntry(TableNameCohort, "", nil, nil, 0, ctx.UserID)
	var parentCancelIDs []uint
	for _, overrideEvent := range overrideEvents {
		if !overrideEvent.IsCancelled && linkedOverrideID != nil {
			overrideEvent.LinkedOverrideEventID = linkedOverrideID
			// Clean up old reschedule targets: when re-rescheduling (A→B then B→C),
			// the old target (B's non-cancelled override) must be removed so only
			// the new target (C) remains linked to A's cancel override.
			if err := trans.Where("linked_override_event_id = ? AND is_cancelled = false AND id != ? AND deleted_at IS NULL",
				*linkedOverrideID, overrideEvent.ID).
				Delete(&ProgramClassEventOverride{}).Error; err != nil {
				trans.Rollback()
				return newDBError(err, "unable to clean up old reschedule targets")
			}
		}
		if overrideEvent.ID == 0 {
			var existing ProgramClassEventOverride
			if err := trans.Unscoped().
				Where("event_id = ? AND override_rrule = ? AND deleted_at IS NULL",
					overrideEvent.EventID, overrideEvent.OverrideRrule).
				First(&existing).Error; err == nil {
				// If the existing override is a reschedule target linked from a cancel,
				// remember the parent cancel so we can preserve it after the upsert
				if existing.LinkedOverrideEventID != nil && *existing.LinkedOverrideEventID != existing.ID {
					parentCancelIDs = append(parentCancelIDs, *existing.LinkedOverrideEventID)
				}
				overrideEvent.ID = existing.ID
				// Preserve the chain link when overwriting a reschedule target with a cancel.
				// Example: After A→B, B's override has linked=A_cancel. When B is cancelled,
				// the incoming cancel has linked=nil. Carry forward so the A←B chain survives.
				if overrideEvent.LinkedOverrideEventID == nil && existing.LinkedOverrideEventID != nil {
					overrideEvent.LinkedOverrideEventID = existing.LinkedOverrideEventID
				}
			}
		}
		isOverrideUpdate = overrideEvent.ID > 0
		if isOverrideUpdate {
			// Existing override found — do a direct UPDATE to avoid GORM's
			// soft-delete behavior with Create+OnConflict which deletes the
			// existing record and creates a new one (breaking chain links).
			if err := trans.Model(&ProgramClassEventOverride{}).
				Where("id = ?", overrideEvent.ID).
				Updates(map[string]interface{}{
					"duration":                 overrideEvent.Duration,
					"override_rrule":           overrideEvent.OverrideRrule,
					"is_cancelled":             overrideEvent.IsCancelled,
					"room_id":                  overrideEvent.RoomID,
					"reason":                   overrideEvent.Reason,
					"linked_override_event_id": overrideEvent.LinkedOverrideEventID,
					"instructor_id":            overrideEvent.InstructorID,
				}).Error; err != nil {
				trans.Rollback()
				return newCreateDBError(err, "program_class_event_overrides")
			}
		} else {
			if err := trans.Create(&overrideEvent).Error; err != nil {
				trans.Rollback()
				return newCreateDBError(err, "program_class_event_overrides")
			}
		}
		if overrideEvent.IsCancelled && len(overrideEvents) < 2 {
			changeLogEntry.FieldName = "event_cancelled"
			changeLogEntry.OldValue = StringPtr("")
			changeLogEntry.ParentRefID = overrideEvent.CohortID
			changeLogEntry.NewValue = &overrideEvent.OverrideRrule
		} else if !overrideEvent.IsCancelled && len(overrideEvents) > 1 {
			eventSummary, err := overrideEvent.GetRescheduleSummary(ctx.Timezone)
			if err != nil {
				trans.Rollback()
				return newDBError(err, "unable to parse event summary")
			}
			changeLogEntry.FieldName = "event_rescheduled"
			changeLogEntry.ParentRefID = overrideEvent.CohortID
			changeLogEntry.NewValue = eventSummary
		} else if overrideEvent.IsCancelled {
			linkedOverrideID = &overrideEvent.ID
			changeLogEntry.OldValue = &overrideEvent.OverrideRrule
		}
		if !overrideEvent.IsCancelled && overrideEvent.InstructorID != nil {
			var instructor User
			if err := trans.Select("name_first", "name_last").First(&instructor, *overrideEvent.InstructorID).Error; err == nil {
				eventDate, _ := overrideEvent.GetFormattedOverrideDate("1/02/2006")
				summary := instructor.NameFirst + " " + instructor.NameLast + " on " + *eventDate
				changeLogEntry.FieldName = "event_substitute_instructor"
				changeLogEntry.ParentRefID = overrideEvent.CohortID
				changeLogEntry.NewValue = &summary
			}
		}
		if !overrideEvent.IsCancelled && overrideEvent.RoomID != nil && len(overrideEvents) == 1 {
			var room Room
			if err := trans.Select("name").First(&room, *overrideEvent.RoomID).Error; err == nil {
				eventDate, _ := overrideEvent.GetFormattedOverrideDate("1/02/2006")
				summary := room.Name + " on " + *eventDate
				changeLogEntry.FieldName = "event_room_changed"
				changeLogEntry.ParentRefID = overrideEvent.CohortID
				changeLogEntry.NewValue = &summary
			}
		}
		if overrideEvent.IsCancelled || isOverrideUpdate { //delete attendance
			eventDate, err = overrideEvent.GetFormattedOverrideDate("2006-01-02")
			if err != nil {
				trans.Rollback()
				return newDBError(err, "unable to parse override date")
			}
			if err := deleteEventAttedanceByDate(trans, overrideEvent.EventID, *eventDate); err != nil {
				trans.Rollback()
				return err
			}
		}
	}

	// Restore parent cancel overrides that may have been soft-deleted by the upsert.
	// This preserves the A→B→C chain: when rescheduling B→C, A's cancel must survive.
	if len(parentCancelIDs) > 0 {
		if err := trans.Unscoped().Model(&ProgramClassEventOverride{}).
			Where("id IN ?", parentCancelIDs).
			Update("deleted_at", nil).Error; err != nil {
			trans.Rollback()
			return newDBError(err, "unable to restore parent cancel overrides")
		}
	}

	if err := trans.Create(&changeLogEntry).Error; err != nil {
		trans.Rollback()
		return newCreateDBError(err, "change_log_entries")
	}

	if len(overrideEvents) > 0 {
		if err := db.syncClassDateBoundaries(trans, overrideEvents[0].CohortID); err != nil {
			trans.Rollback()
			return err
		}
	}

	//end transaction
	if err := trans.Commit().Error; err != nil {
		return newDBError(err, "unable to commit the database transaction")
	}
	return nil
}

func (db *Store) syncClassDateBoundaries(trans *gorm.DB, classID uint) error {
	// Boundaries must be derived from all rows, since a class can have multiple.
	var events []ProgramClassEvent
	if err := trans.Preload("Overrides").Where("cohort_id = ?", classID).Order("created_at ASC").Find(&events).Error; err != nil {
		return newGetRecordsDBError(err, "program_class_events")
	}
	if len(events) == 0 {
		return newGetRecordsDBError(gorm.ErrRecordNotFound, "program_class_events")
	}

	var class ProgramClassCohort
	if err := trans.First(&class, "id = ?", classID).Error; err != nil {
		return newGetRecordsDBError(err, "program class cohort")
	}

	startBoundary := class.StartDt
	endBoundary := startBoundary.AddDate(5, 0, 0)
	if class.EndDt != nil {
		endBoundary = class.EndDt.AddDate(0, 3, 0)
	}

	var originalBaseStart, ruleUntil, earliestReschedule time.Time
	allDates := make([]time.Time, 0)
	hasOverrides := false

	for i := range events {
		event := events[i]
		rRule, err := event.GetRRule()
		if err != nil {
			return err
		}

		baseStart := rRule.OrigOptions.Dtstart.In(time.UTC)
		if originalBaseStart.IsZero() || baseStart.Before(originalBaseStart) {
			originalBaseStart = baseStart
		}
		// rrule-go's GetUntil() returns a far-future date (~year 2318) instead of zero
		// when no UNTIL is present, so check the raw string for an explicit UNTIL clause
		if strings.Contains(event.RecurrenceRule, "UNTIL=") {
			if u := rRule.GetUntil(); u.After(ruleUntil) {
				ruleUntil = u
			}
		}

		baseOccurrences := rRule.Between(startBoundary, endBoundary, true)

		if len(event.Overrides) > 0 {
			hasOverrides = true
		}
		cancelledDates := make(map[string]bool)
		rescheduledDates := make([]time.Time, 0)
		for _, override := range event.Overrides {
			overrideRule, err := rrule.StrToRRule(override.OverrideRrule)
			if err != nil {
				logrus.Warnf("unable to parse override rule: %s", override.OverrideRrule)
				continue
			}
			occurrences := overrideRule.All()
			if len(occurrences) == 0 {
				continue
			}

			overrideDate := occurrences[0].In(time.UTC)
			if override.IsCancelled {
				cancelledDates[overrideDate.Format("2006-01-02")] = true
				continue
			}

			rescheduledDates = append(rescheduledDates, overrideDate)
			if earliestReschedule.IsZero() || overrideDate.Before(earliestReschedule) {
				earliestReschedule = overrideDate
			}
		}

		for _, occurrence := range baseOccurrences {
			if !cancelledDates[occurrence.Format("2006-01-02")] {
				allDates = append(allDates, occurrence)
			}
		}
		allDates = append(allDates, rescheduledDates...)
	}

	if len(allDates) == 0 {
		return nil
	}

	sort.Slice(allDates, func(i, j int) bool {
		return allDates[i].Before(allDates[j])
	})

	computedEnd := allDates[len(allDates)-1]

	updates := make(map[string]any)

	targetStart := originalBaseStart
	if !earliestReschedule.IsZero() && earliestReschedule.Before(originalBaseStart) {
		targetStart = earliestReschedule
	}
	if !targetStart.Equal(class.StartDt) {
		updates["start_dt"] = targetStart
	}

	if class.EndDt == nil || !computedEnd.Equal(*class.EndDt) {
		if (!hasOverrides && !ruleUntil.IsZero()) || (!ruleUntil.IsZero() && ruleUntil.After(computedEnd)) {
			updates["end_dt"] = ruleUntil
		} else {
			updates["end_dt"] = computedEnd
		}
	} else if (!hasOverrides && !ruleUntil.IsZero() && !ruleUntil.Equal(*class.EndDt)) || (!ruleUntil.IsZero() && ruleUntil.After(*class.EndDt)) {
		updates["end_dt"] = ruleUntil
	}

	if len(updates) == 0 {
		return nil
	}

	if err := trans.Model(&ProgramClassCohort{}).Where("id = ?", classID).Updates(updates).Error; err != nil {
		return newUpdateDBError(err, "program class cohort")
	}

	return nil
}

func deleteEventAttedanceByDate(trans *gorm.DB, eventID uint, eventDate string) error {
	if err := trans.Unscoped().Where("event_id = ? AND date = ?", eventID, eventDate).Delete(&ProgramClassEventAttendance{}).Error; err != nil {
		return newDeleteDBError(err, "class_event_attendance")
	}
	return nil
}

func (db *Store) NewEventOverride(eventId int, form *OverrideForm) (*ProgramClassEventOverride, error) {
	event, err := db.GetEventById(eventId)
	if err != nil {
		return nil, newGetRecordsDBError(err, "program_class_event")
	}
	override := ProgramClassEventOverride{
		EventID: uint(eventId),
	}
	if form.RoomID != nil {
		override.RoomID = form.RoomID
	}
	if form.Duration != "" {
		override.Duration = form.Duration
	}
	if form.IsCancelled {
		override.IsCancelled = true
	}
	var rruleStr string
	switch form.OverrideType {
	case OverrideAll:
		if form.IsCancelled {
			rruleOptions := rrule.ROption{
				Freq:    rrule.DAILY,
				Count:   0, // No occurrences
				Dtstart: time.Now(),
			}
			rule, err := rrule.NewRRule(rruleOptions)
			if err != nil {
				return nil, newCreateDBError(err, "program_class_event_override")
			}
			rruleStr = rule.String()
		} else {
			until, err := event.RRuleUntil()
			if err != nil {
				return nil, newGetRecordsDBError(err, "program_class_event")
			}
			rruleOptions := rrule.ROption{
				Freq:      rrule.WEEKLY,
				Dtstart:   time.Now(),
				Until:     until,
				Byweekday: []rrule.Weekday{rrule.MO, rrule.TU, rrule.WE, rrule.TH, rrule.FR, rrule.SA, rrule.SU}, // Modify as needed
			}
			rule, err := rrule.NewRRule(rruleOptions)
			if err != nil {
				return nil, newCreateDBError(err, "program_class_event_override")
			}
			rruleStr = rule.String()
		}
	case OverrideSelf:
		overrideDateOnly, err := time.ParseInLocation("2006-01-02", form.Date, time.UTC)
		if err != nil {
			return nil, newCreateDBError(err, "program_class_event_override")
		}

		eventRule, err := event.GetRRule()
		if err != nil {
			return nil, newCreateDBError(err, "program_class_event")
		}
		eventDtstartUTC := eventRule.OrigOptions.Dtstart.In(time.UTC)

		overrideDate := time.Date(
			overrideDateOnly.Year(), overrideDateOnly.Month(), overrideDateOnly.Day(),
			eventDtstartUTC.Hour(), eventDtstartUTC.Minute(), eventDtstartUTC.Second(), eventDtstartUTC.Nanosecond(),
			time.UTC,
		)

		rruleOptions := rrule.ROption{
			Freq:    rrule.DAILY,
			Count:   1,
			Dtstart: overrideDate,
		}
		rule, err := rrule.NewRRule(rruleOptions)
		if err != nil {
			return nil, newCreateDBError(err, "program_class_event_override")
		}
		rruleStr = rule.String()

	case OverrideForwards:
		overrideDate, err := time.Parse("2006-01-02", form.Date)
		if err != nil {
			return nil, newCreateDBError(err, "program_class_event_override")
		}
		rule, err := event.GetRRule()
		if err != nil {
			return nil, newCreateDBError(err, "program_class_event_override")
		}
		rruleOptions := rrule.ROption{
			Freq:    rule.Options.Freq,
			Dtstart: overrideDate,
			Until:   rule.Options.Until,
		}
		rule, err = rrule.NewRRule(rruleOptions)
		if err != nil {
			return nil, newCreateDBError(err, "program_class_event_override")
		}
		rruleStr = rule.String()
	default:
		return nil, newCreateDBError(err, "program_class_event_override")
	}
	override.OverrideRrule = rruleStr
	if err := db.Create(&override).Error; err != nil {
		return nil, newCreateDBError(err, "program_class_event_override")
	}
	return &override, nil
}

func (db *Store) GetFacilityCalendar(args *QueryContext, dtRng *DateRange, classID int) ([]FacilityProgramClassEvent, error) {
	events := make([]FacilityProgramClassEvent, 0, 10)
	tx := db.WithContext(args.Ctx).Table("program_class_events pcev").
		Select(`pcev.id, pcev.created_at, pcev.updated_at, pcev.deleted_at, pcev.cohort_id, pcev.duration, pcev.recurrence_rule, pcev.room_id,
		pcev.instructor_id, pcev.is_cancelled, pcev.reason,
		r.name as room,
		c.program_id as program_id,
		p.name as program_name,
		COALESCE(iu.name_first || ' ' || iu.name_last, '') as instructor_name,
		cl.name as class_name,
		c.status as class_status,
        STRING_AGG(CONCAT(u.id, ':', u.name_last, ', ', u.name_first), '|' ORDER BY u.name_last) FILTER (WHERE e.enrollment_status = 'Enrolled') AS enrolled_users`).
		Joins("JOIN program_class_cohorts c ON c.id = pcev.cohort_id AND c.archived_at IS NULL AND c.deleted_at IS NULL").
		Joins("JOIN program_classes cl ON cl.id = c.class_id").
		Joins("JOIN programs p ON p.id = c.program_id").
		Where("pcev.deleted_at IS NULL").
		Joins("LEFT JOIN rooms r ON r.id = pcev.room_id").
		Joins("LEFT JOIN users iu ON iu.id = pcev.instructor_id").
		Joins("LEFT JOIN program_class_enrollments e ON e.cohort_id = c.id").
		Joins("LEFT JOIN users u ON e.user_id = u.id").
		Where("c.facility_id = ?", args.FacilityID)

	if classID > 0 {
		tx = tx.Where("c.id = ?", classID)
	}
	if !args.IsAdmin {
		tx = tx.Where("u.id = ? AND e.enrollment_status = 'Enrolled'", args.UserID)
	}
	tx = tx.Group("pcev.id, iu.name_first, iu.name_last, cl.name, c.status, c.program_id, p.name, r.name")
	if err := tx.Scan(&events).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_events")
	}

	eventIDs := make([]uint, 0, len(events))
	for _, event := range events { //gathering all for next query
		eventIDs = append(eventIDs, event.ID)
	}

	overrideEvents, err := db.GetProgramClassEventOverrides(args, eventIDs...)
	if err != nil {
		return nil, err
	}

	overrideEventMap := make(map[uint][]ProgramClassEventOverride)
	for _, overrideEvent := range overrideEvents { //easy organization
		overrideEventMap[overrideEvent.EventID] = append(overrideEventMap[overrideEvent.EventID], overrideEvent)
	}

	facilityEvents := make([]FacilityProgramClassEvent, 0, 10)
	for _, event := range events {
		rRule, err := event.GetRRuleWithTimezone(args.Timezone)
		if err != nil {
			return nil, err
		}
		occurrences := rRule.Between(dtRng.Start, dtRng.End, true)
		duration, err := time.ParseDuration(event.Duration)
		if err != nil {
			return nil, err
		}

		//daylight saving time fix (VIP) prevents the time shift
		canonicalHour, canonicalMinute := getCanonicalHourAndMinute(occurrences, args.Timezone)
		overrides := overrideEventMap[event.ID]
		var (
			isCancelled, isRescheduled, isRoomOrInstructorOverride bool
			overrideID                                             uint
			overrideReason                                         string
		)
		userLocation, _ := time.LoadLocation(args.Timezone)
		for _, occurrence := range occurrences {
			localOccurrence := occurrence.In(userLocation)
			consistentOccurrence := time.Date(
				localOccurrence.Year(),
				localOccurrence.Month(),
				localOccurrence.Day(),
				canonicalHour,
				canonicalMinute,
				0,
				0,
				userLocation,
			)
			// Series-level cancellation: every occurrence is cancelled, bypass override logic
			if event.IsCancelled {
				endTime := consistentOccurrence.Add(duration)
				pce := event.ProgramClassEvent
				if event.Reason != nil {
					r := *event.Reason
					pce.Reason = &r
				}
				facilityEvent := FacilityProgramClassEvent{
					ProgramClassEvent: pce,
					Room:              event.Room,
					InstructorName:    event.InstructorName,
					ProgramID:         event.ProgramID,
					ProgramName:       event.ProgramName,
					ClassName:         event.ClassName,
					EnrolledUsers:     event.EnrolledUsers,
					ClassStatus:       event.ClassStatus,
					StartTime:         &consistentOccurrence,
					EndTime:           &endTime,
					Frequency:         rRule.OrigOptions.Freq.String(),
					IsCancelled:       true,
				}
				facilityEvents = append(facilityEvents, facilityEvent)
				continue
			}

			isCancelled, isRescheduled, isRoomOrInstructorOverride, overrideID, overrideReason = checkEventCancelledAndRescheduled(consistentOccurrence, overrides, args.Timezone)

			if (isCancelled && isRescheduled) || isRoomOrInstructorOverride {
				continue
			}

			endTime := consistentOccurrence.Add(duration)
			pce := event.ProgramClassEvent
			if isCancelled && overrideReason != "" {
				r := overrideReason
				pce.Reason = &r
			}
			facilityEvent := FacilityProgramClassEvent{
				ProgramClassEvent: pce,
				Room:              event.Room,
				InstructorName:    event.InstructorName,
				ProgramID:         event.ProgramID,
				ProgramName:       event.ProgramName,
				ClassName:         event.ClassName,
				EnrolledUsers:     event.EnrolledUsers,
				ClassStatus:       event.ClassStatus,
				StartTime:         &consistentOccurrence,
				EndTime:           &endTime,
				Frequency:         rRule.OrigOptions.Freq.String(),
				IsCancelled:       isCancelled,
				IsOverride:        isCancelled,
				OverrideID:        overrideID,
			}
			facilityEvents = append(facilityEvents, facilityEvent)
		}

		linkedOverridesMap := getLinkedOverrideEventMap(event, overrides)
		for _, override := range overrides { //adding the rescheduled ones to instances slice
			if override.IsCancelled && override.LinkedOverrideEventID == nil { //skip only cancelled ones with no links
				continue
			}
			rRule, err := rrule.StrToRRule(override.OverrideRrule)
			if err != nil {
				logrus.Warnf("unable to parse reschedule override rule: %s", override.OverrideRrule)
				continue
			}
			if len(rRule.All()) == 0 {
				logrus.Warnf("cancelled override rule does not contain a date instance, rule is: %s", override.OverrideRrule)
				continue
			}
			overrideDate := rRule.All()[0]
			duration, err := time.ParseDuration(override.Duration)
			if err != nil {
				logrus.Errorf("error parsing duration for event: %v", err)
			}

			userLocation, _ := time.LoadLocation(args.Timezone)
			localOverrideTime := overrideDate.In(userLocation)
			consistentOverrideDate := time.Date(
				localOverrideTime.Year(),
				localOverrideTime.Month(),
				localOverrideTime.Day(),
				localOverrideTime.Hour(),
				localOverrideTime.Minute(),
				0,
				0,
				userLocation,
			)
			overrideEndTime := consistentOverrideDate.Add(duration)
			var linkedOverrideEvent *FacilityProgramClassEvent
			if override.LinkedOverrideEventID != nil {
				linkedOverrideEvent = linkedOverridesMap[*override.LinkedOverrideEventID]
			}
			pce := event.ProgramClassEvent
			roomName := event.Room
			originalRoomName := ""
			instructorName := event.InstructorName
			originalInstructorName := ""
			if override.RoomRef != nil {
				newRoomName := override.RoomRef.Name
				if newRoomName != event.Room {
					originalRoomName = event.Room
				}
				roomName = newRoomName
				pce.RoomID = override.RoomID
			}
			if override.InstructorID != nil {
				pce.InstructorID = override.InstructorID
				if override.Instructor != nil {
					newInstructorName := strings.TrimSpace(override.Instructor.NameFirst + " " + override.Instructor.NameLast)
					if newInstructorName != event.InstructorName {
						originalInstructorName = event.InstructorName
					}
					instructorName = newInstructorName
				} else if event.InstructorID == nil || *event.InstructorID != *override.InstructorID {
					originalInstructorName = event.InstructorName
					instructorName = "Unknown Instructor"
				}
			}
			facilityEvent := FacilityProgramClassEvent{
				ProgramClassEvent:      pce,
				Room:                   roomName,
				OriginalRoom:           originalRoomName,
				InstructorName:         instructorName,
				OriginalInstructorName: originalInstructorName,
				ProgramID:              event.ProgramID,
				ProgramName:            event.ProgramName,
				ClassName:              event.ClassName,
				EnrolledUsers:          event.EnrolledUsers,
				ClassStatus:            event.ClassStatus,
				StartTime:              &consistentOverrideDate,
				EndTime:                &overrideEndTime,
				Frequency:              rRule.OrigOptions.Freq.String(),
				IsOverride:             true,
				IsCancelled:            override.IsCancelled,
				OverrideID:             override.ID,
				LinkedOverrideEvent:    linkedOverrideEvent,
			}
			facilityEvents = append(facilityEvents, facilityEvent)
		}
	}
	slices.SortFunc(facilityEvents, func(a, b FacilityProgramClassEvent) int {
		return b.StartTime.Compare(*a.StartTime)
	})

	return facilityEvents, nil
}

// returns the hour and minute from the first occurrence within the slice of time.Time instances
func getCanonicalHourAndMinute(occurrences []time.Time, timezone string) (int, int) {
	var canonicalHour, canonicalMinute int
	if len(occurrences) > 0 {
		firstOccurrence := occurrences[0]
		userLocation, err := time.LoadLocation(timezone)
		if err == nil {
			localTime := firstOccurrence.In(userLocation)
			canonicalHour = localTime.Hour()
			canonicalMinute = localTime.Minute()
		} else {
			logrus.Warnf("unable to load the user's timezone while trying to get the canonical hour and minute. Error is: %v", err)
			canonicalHour = firstOccurrence.Hour()
			canonicalMinute = firstOccurrence.Minute()
		}
	}
	return canonicalHour, canonicalMinute
}

func getLinkedOverrideEventMap(event FacilityProgramClassEvent, overrides []ProgramClassEventOverride) map[uint]*FacilityProgramClassEvent {
	overrideEventMap := make(map[uint]*FacilityProgramClassEvent)
	for _, override := range overrides {
		if override.LinkedOverrideEventID != nil {
			overrideEventMap[*override.LinkedOverrideEventID] = &FacilityProgramClassEvent{
				ProgramClassEvent: event.ProgramClassEvent,
				InstructorName:    event.InstructorName,
				ProgramName:       event.ProgramName,
				ClassName:         event.ClassName,
				EnrolledUsers:     event.EnrolledUsers,
			}
			continue
		}

		if override.IsCancelled && FormatNullableString(override.Reason) == "rescheduled" {
			programClassEvent, ok := overrideEventMap[override.ID]
			if !ok {
				continue
			}
			rRule, err := rrule.StrToRRule(override.OverrideRrule)
			if err != nil {
				logrus.Warnf("unable to parse reschedule override rule: %s", override.OverrideRrule)
				continue
			}
			if len(rRule.All()) == 0 {
				logrus.Warnf("cancelled override rule does not contain a date instance, rule is: %s", override.OverrideRrule)
				continue
			}
			overrideDate := rRule.All()[0]
			duration, err := time.ParseDuration(override.Duration)
			if err != nil {
				logrus.Errorf("error parsing duration for event: %v", err)
			}
			overrideEndTime := overrideDate.Add(duration)
			programClassEvent.StartTime = &overrideDate
			programClassEvent.EndTime = &overrideEndTime
			programClassEvent.Frequency = rRule.OrigOptions.Freq.String()
			programClassEvent.IsOverride = true
			programClassEvent.IsCancelled = true
			programClassEvent.OverrideID = override.ID
			overrideEventMap[override.ID] = programClassEvent
		}
	}
	return overrideEventMap
}

func checkEventCancelledAndRescheduled(occurrence time.Time, overrides []ProgramClassEventOverride, timezone string) (bool, bool, bool, uint, string) {
	var (
		isCancelled        = false
		isRescheduled      = false
		isRoomOrInstructor = false
		overrideID         uint
		overrideReason     string
	)

	for _, override := range overrides {
		if !override.IsCancelled && override.LinkedOverrideEventID != nil { //skip
			continue
		}
		rRule, err := rrule.StrToRRule(override.OverrideRrule)
		if err != nil {
			logrus.Warnf("unable to parse override rule: %s", override.OverrideRrule)
			continue
		}
		if len(rRule.All()) == 0 {
			logrus.Warnf("override rule does not contain a date instance, rule is: %s", override.OverrideRrule)
			continue
		}
		overrideDate := rRule.All()[0]

		//START day light savings time fix here
		userLocation, _ := time.LoadLocation(timezone)
		localOverrideTime := overrideDate.In(userLocation)
		canonicalOverrideHour := localOverrideTime.Hour()
		canonicalOverrideMinute := localOverrideTime.Minute()
		consistentOverrideDate := time.Date(
			localOverrideTime.Year(),
			localOverrideTime.Month(),
			localOverrideTime.Day(),
			canonicalOverrideHour,
			canonicalOverrideMinute,
			0, 0,
			userLocation,
		)
		//END day light savings time fix here
		if consistentOverrideDate.Equal(occurrence) {
			isRescheduled = FormatNullableString(override.Reason) == "rescheduled"
			isCancelled = override.IsCancelled
			isRoomOrInstructor = !override.IsCancelled && !isRescheduled
			overrideID = override.ID
			overrideReason = FormatNullableString(override.Reason)
			break
		}
	}
	return isCancelled, isRescheduled, isRoomOrInstructor, overrideID, overrideReason
}

func applyOverrides(event ProgramClassEvent, start, end time.Time) []EventInstance {
	var instances []EventInstance
	mainSet := rrule.Set{}

	duration, err := time.ParseDuration(event.Duration)
	if err != nil {
		logrus.Errorf("Error parsing duration: %s", err)
		duration = time.Hour // default duration
	}

	mainRuleOptions, err := rrule.StrToROption(event.RecurrenceRule)
	if err != nil {
		logrus.Errorf("Error parsing main event RRULE: %s", err)
		return instances
	}
	mainRuleOptions.Dtstart = mainRuleOptions.Dtstart.In(time.UTC)
	mainRule, err := rrule.NewRRule(*mainRuleOptions)
	if err != nil {
		logrus.Errorf("Error creating main RRULE: %s", err)
		return instances
	}
	mainSet.RRule(mainRule)

	var rDates []EventInstance

	for _, override := range event.Overrides {
		overrideOptions, err := rrule.StrToROption(override.OverrideRrule)
		if err != nil {
			logrus.Errorf("Error parsing override RRULE: %s", err)
			continue
		}
		overrideOptions.Dtstart = overrideOptions.Dtstart.In(time.UTC)
		overrideRule, err := rrule.NewRRule(*overrideOptions)
		if err != nil {
			logrus.Errorf("Error creating override RRULE: %s", err)
			continue
		}
		overrideOccurrences := overrideRule.Between(start.UTC(), end.UTC(), true)
		if override.IsCancelled {
			for _, occ := range overrideOccurrences {
				mainSet.ExDate(occ.UTC())
			}
		} else {
			for _, occ := range overrideOccurrences {
				roomName := getRoomName(event.RoomRef)
				rDateInstance := EventInstance{
					EventID:     event.ID,
					CohortID:    event.CohortID,
					Duration:    duration,
					StartTime:   occ.UTC(),
					IsCancelled: false,
					Room:        roomName,
				}
				if override.Duration != "" {
					newDuration, err := time.ParseDuration(override.Duration)
					if err == nil {
						rDateInstance.Duration = newDuration
					}
				}
				if override.RoomRef != nil {
					rDateInstance.Room = override.RoomRef.Name
				}
				rDates = append(rDates, rDateInstance)
				mainSet.ExDate(occ.UTC())
			}
		}
	}
	occurrences := mainSet.Between(start.UTC(), end.UTC(), true)

	roomName := getRoomName(event.RoomRef)
	for _, occ := range occurrences {
		instance := EventInstance{
			EventID:     event.ID,
			CohortID:    event.CohortID,
			StartTime:   occ.UTC(),
			Duration:    duration,
			IsCancelled: false,
			Room:        roomName,
		}
		instances = append(instances, instance)
	}

	instances = append(instances, rDates...)
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].StartTime.Before(instances[j].StartTime)
	})

	return instances
}

func generateEventInstances(event ProgramClassEvent, startDate, endDate time.Time) []EventInstance {
	eventInstances := applyOverrides(event, startDate, endDate)
	return eventInstances
}

func GenerateEventInstances(event ProgramClassEvent, startDate, endDate time.Time) []EventInstance {
	return generateEventInstances(event, startDate, endDate)
}

// GetClassEventInstancesWithAttendanceForRecurrence returns all occurrences for events
// for a given class based on each event's recurrence rule (from DTSTART until UNTIL)
// along with their associated attendance records.
func (db *Store) GetClassEventInstancesWithAttendanceForRecurrence(classId int, qryCtx *QueryContext, month, year string, userId *int, allInstances bool) ([]ClassEventInstance, error) {
	loc, err := time.LoadLocation(qryCtx.Timezone)
	if err != nil {
		logrus.Error("failed to load timezone")
		return nil, newDBError(err, "failed to load timezone")
	}

	// Load all rows, not just the newest: "apply to all future sessions" leaves a class
	// with multiple rows whose RRULEs together describe the full schedule.
	var events []ProgramClassEvent
	if err := db.WithContext(qryCtx.Ctx).
		Model(&ProgramClassEvent{}).
		Preload("Overrides").
		Where("cohort_id = ?", classId).
		Order("created_at ASC").
		Find(&events).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_events")
	}
	if len(events) == 0 {
		return []ClassEventInstance{}, nil
	}

	type parsedEvent struct {
		event ProgramClassEvent
		rule  *rrule.RRule
	}
	parsed := make([]parsedEvent, 0, len(events))
	var earliestStart time.Time
	for i := range events {
		rule, err := events[i].GetRRuleWithTimezone(qryCtx.Timezone)
		if err != nil {
			logrus.Errorf("event has invalid rule, event: %v", events[i])
			continue
		}
		parsed = append(parsed, parsedEvent{event: events[i], rule: rule})
		dtStart := rule.GetDTStart()
		if earliestStart.IsZero() || dtStart.Before(earliestStart) {
			earliestStart = dtStart
		}
	}
	if len(parsed) == 0 {
		return []ClassEventInstance{}, nil
	}

	var startTime, untilTime time.Time

	if month == "" || year == "" {
		if userId != nil {
			var enrollment ProgramClassEnrollment
			err := db.WithContext(qryCtx.Ctx).
				Model(&ProgramClassEnrollment{}).
				Where("cohort_id = ? AND user_id = ?", classId, *userId).
				First(&enrollment).Error
			if err != nil {
				return nil, newGetRecordsDBError(err, "program_class_enrollments")
			}
			startTime = earliestStart
			if enrollment.CreatedAt.After(startTime) {
				startTime = enrollment.CreatedAt.Truncate(24 * time.Hour)
			}
			untilTime = time.Now().AddDate(0, 0, 1).Truncate(24 * time.Hour)
			if enrollment.EnrollmentStatus != "Enrolled" {
				untilTime = enrollment.UpdatedAt.AddDate(0, 0, 1).Truncate(24 * time.Hour)
			}
		} else {
			startTime = earliestStart
			if allInstances {
				// rrule-go's GetUntil() returns a far-future date (~year 2318) instead of zero
				// when no UNTIL is present, so check the raw string for an explicit UNTIL clause.
				untilTime = time.Now().AddDate(1, 0, 0).Truncate(24 * time.Hour)
				var latestUntil time.Time
				for _, pe := range parsed {
					if strings.Contains(pe.event.RecurrenceRule, "UNTIL=") {
						if u := pe.rule.GetUntil().AddDate(0, 0, 1); u.After(latestUntil) {
							latestUntil = u
						}
					}
				}
				if !latestUntil.IsZero() {
					untilTime = latestUntil
				}
				maxFuture := time.Now().AddDate(0, 6, 0).Truncate(24 * time.Hour)
				if untilTime.After(maxFuture) {
					untilTime = maxFuture
				}
			} else {
				untilTime = time.Now().AddDate(0, 1, 0).Truncate(24 * time.Hour)
			}
		}
	} else {
		yearInt, err := strconv.Atoi(year)
		if err != nil {
			return nil, newDBError(err, "invalid year query parameter")
		}
		monthInt, err := strconv.Atoi(month)
		if err != nil {
			return nil, newDBError(err, "invalid month query parameter")
		}
		startTime = time.Date(yearInt, time.Month(monthInt), 1, 0, 0, 0, 0, loc)
		untilTime = startTime.AddDate(0, 1, 0)
	}

	// Fetch attendance across all event IDs so records on older (capped) rows still surface.
	classEventIDs := make([]uint, 0, len(events))
	for i := range events {
		classEventIDs = append(classEventIDs, events[i].ID)
	}
	var attendances []ProgramClassEventAttendance
	startDateStr := startTime.Format("2006-01-02")
	endDateStr := untilTime.Format("2006-01-02")
	tx := db.WithContext(qryCtx.Ctx).
		Model(&ProgramClassEventAttendance{}).
		Where("event_id IN (?) AND date >= ? AND date < ?", classEventIDs, startDateStr, endDateStr)
	if userId != nil {
		tx = tx.Where("user_id = ?", *userId)
	}
	if err := tx.
		Order("date DESC").
		Find(&attendances).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_event_attendances")
	}

	var eventInstances []ClassEventInstance
	for _, pe := range parsed {
		occurrences := pe.rule.Between(startTime, untilTime, true)
		classTime, canonicalHour, canonicalMinute := eventClassTime(pe.event, pe.rule, occurrences, loc)
		eventInstances = append(eventInstances, createEventInstances(pe.event, occurrences, loc, classTime, attendances, canonicalHour, canonicalMinute)...)
	}
	eventInstances = dedupeClassEventInstances(eventInstances)

	qryCtx.Total = int64(len(eventInstances))
	if !qryCtx.All {
		offset := qryCtx.CalcOffset()
		end := offset + qryCtx.PerPage
		if offset >= len(eventInstances) {
			eventInstances = []ClassEventInstance{}
		} else if end > len(eventInstances) {
			eventInstances = eventInstances[offset:]
		} else {
			eventInstances = eventInstances[offset:end]
		}
	}

	return eventInstances, nil
}

func createEventInstances(event ProgramClassEvent, occurrences []time.Time, loc *time.Location, classTime string, attendances []ProgramClassEventAttendance, canonicalHour, canonicalMinute int) []ClassEventInstance {
	// Pre-index attendance by date for O(1) lookup instead of scanning all records per occurrence
	attendanceByDate := make(map[string][]ProgramClassEventAttendance, len(attendances))
	for i := range attendances {
		attendanceByDate[attendances[i].Date] = append(attendanceByDate[attendances[i].Date], attendances[i])
	}

	eventInstances := make([]ClassEventInstance, 0, len(occurrences))
	//daylight saving time fix (VIP) prevents the time shift
	for _, occ := range occurrences {
		occDateStr := occ.In(loc).Format("2006-01-02")
		consistentOccurrence := time.Date(
			occ.Year(),
			occ.Month(),
			occ.Day(),
			canonicalHour,
			canonicalMinute,
			0,
			0,
			loc,
		)
		isCancelled, isRescheduled, _, overrideID, _ := checkEventCancelledAndRescheduled(consistentOccurrence, event.Overrides, loc.String())

		eventInstance := ClassEventInstance{
			EventID:           event.ID,
			ClassTime:         classTime,
			Date:              occDateStr,
			AttendanceRecords: attendanceByDate[occDateStr],
			// event.IsCancelled is a series-level cancellation: every occurrence is cancelled.
			IsCancelled:   isCancelled || event.IsCancelled,
			IsRescheduled: isRescheduled,
		}
		if isCancelled {
			eventInstance.OverrideID = overrideID
		}
		if isCancelled && isRescheduled {
			// Find the final reschedule target by following the chain.
			// Simple case: non-cancelled override linked to this cancel (A→B).
			// Chain case: cancelled override linked to this cancel (A→B→C),
			// then follow B's cancel to find C's non-cancelled reschedule target.
			currentID := overrideID
			visited := map[uint]bool{currentID: true}
			for {
				foundTarget := false
				// Look for non-cancelled reschedule target linked to currentID
				for _, other := range event.Overrides {
					if !other.IsCancelled && other.LinkedOverrideEventID != nil && *other.LinkedOverrideEventID == currentID {
						rRule, err := rrule.StrToRRule(other.OverrideRrule)
						if err == nil && len(rRule.All()) > 0 {
							eventInstance.RescheduledToDate = rRule.All()[0].In(loc).Format("2006-01-02")
						}
						foundTarget = true
						break
					}
				}
				if foundTarget {
					break
				}
				// No non-cancelled target found — look for a cancelled intermediate
				// (chain case: B was re-rescheduled, so B's override is now cancelled)
				nextID := uint(0)
				for _, other := range event.Overrides {
					if other.IsCancelled && other.LinkedOverrideEventID != nil && *other.LinkedOverrideEventID == currentID && !visited[other.ID] {
						nextID = other.ID
						break
					}
				}
				if nextID == 0 {
					break
				}
				visited[nextID] = true
				currentID = nextID
			}
		}
		eventInstances = append(eventInstances, eventInstance)
	}
	// Collect non-cancelled, non-reschedule overrides (room/instructor/time changes)
	// grouped by date so we can match them to base instances by time
	type sameDateOverride struct {
		override  ProgramClassEventOverride
		startTime string
	}
	sameDateOverridesByDate := make(map[string][]sameDateOverride)
	for _, override := range event.Overrides {
		isReschedule := override.LinkedOverrideEventID != nil && *override.LinkedOverrideEventID != override.ID
		if override.IsCancelled || isReschedule {
			continue
		}
		parsedRule, err := rrule.StrToRRule(override.OverrideRrule)
		if err != nil || len(parsedRule.All()) == 0 {
			continue
		}
		overrideDate := parsedRule.All()[0].In(loc)
		dateStr := overrideDate.Format("2006-01-02")
		startTime := overrideDate.Format("15:04")
		sameDateOverridesByDate[dateStr] = append(sameDateOverridesByDate[dateStr], sameDateOverride{
			override:  override,
			startTime: startTime,
		})
	}
	// Apply room/instructor/time overrides to base instances, matching by time
	for i := range eventInstances {
		overrides, ok := sameDateOverridesByDate[eventInstances[i].Date]
		if !ok {
			continue
		}
		instStartTime := strings.Split(eventInstances[i].ClassTime, "-")[0]
		for _, sdo := range overrides {
			if sdo.startTime == instStartTime {
				parsedRule, _ := rrule.StrToRRule(sdo.override.OverrideRrule)
				overrideDate := parsedRule.All()[0]
				duration, err := time.ParseDuration(sdo.override.Duration)
				if err == nil {
					overrideEnd := overrideDate.Add(duration)
					eventInstances[i].ClassTime = overrideDate.In(loc).Format("15:04") + "-" + overrideEnd.In(loc).Format("15:04")
				}
				eventInstances[i].OverrideID = sdo.override.ID
				break
			}
		}
	}
	// Index existing instances by date for dedup
	instanceByDate := make(map[string]int, len(eventInstances))
	for i, inst := range eventInstances {
		instanceByDate[inst.Date] = i
	}
	// Add rescheduled sessions (overrides with LinkedOverrideEventID pointing to a different override).
	// Includes both non-cancelled targets (normal reschedule) and cancelled targets
	// (Scenario 1: B was rescheduled from A, then B was cancelled — B is both a
	// reschedule target and cancelled, needs to show as "Cancelled + Rescheduled Class").
	for _, override := range event.Overrides {
		isReschedule := override.LinkedOverrideEventID != nil && *override.LinkedOverrideEventID != override.ID
		if !isReschedule {
			continue
		}
		parsedRule, err := rrule.StrToRRule(override.OverrideRrule)
		if err != nil {
			logrus.Warnf("unable to parse reschedule override rule: %s", override.OverrideRrule)
			continue
		}
		allOccs := parsedRule.All()
		if len(allOccs) == 0 {
			logrus.Warnf("cancelled override rule does not contain a date instance, rule is: %s", override.OverrideRrule)
			continue
		}
		overrideDate := allOccs[0]
		overrideDateStr := overrideDate.In(loc).Format("2006-01-02")

		duration, err := time.ParseDuration(override.Duration)
		if err != nil {
			logrus.Errorf("error parsing duration for event: %v", err)
		}
		overrideEnd := overrideDate.Add(duration)
		overrideClassTime := overrideDate.In(loc).Format("15:04") + "-" + overrideEnd.In(loc).Format("15:04")

		rescheduledFromDate := ""
		for _, other := range event.Overrides {
			if other.ID == *override.LinkedOverrideEventID && other.IsCancelled {
				otherRule, err := rrule.StrToRRule(other.OverrideRrule)
				if err == nil && len(otherRule.All()) > 0 {
					rescheduledFromDate = otherRule.All()[0].In(loc).Format("2006-01-02")
				}
				break
			}
		}
		rescheduledInstance := ClassEventInstance{
			EventID:             event.ID,
			ClassTime:           overrideClassTime,
			Date:                overrideDateStr,
			AttendanceRecords:   attendanceByDate[overrideDateStr],
			IsCancelled:         override.IsCancelled,
			RescheduledFromDate: rescheduledFromDate,
			OverrideID:          override.ID,
		}
		if existingIdx, exists := instanceByDate[overrideDateStr]; exists {
			existingInst := eventInstances[existingIdx]
			if existingInst.ClassTime == overrideClassTime {
				// Same time slot on the same date — replace (same session with reschedule info)
				eventInstances[existingIdx] = rescheduledInstance
			} else {
				// Different times on the same date — keep both as separate sessions
				eventInstances = append(eventInstances, rescheduledInstance)
			}
		} else {
			instanceByDate[overrideDateStr] = len(eventInstances)
			eventInstances = append(eventInstances, rescheduledInstance)
		}
	}
	slices.SortFunc(eventInstances, func(a, b ClassEventInstance) int {
		return cmp.Compare(b.Date, a.Date)
	})
	return eventInstances
}

// eventClassTime returns the displayed "HH:MM-HH:MM" class time and canonical
// hour/minute, locked to the DTSTART's wall-clock time to avoid DST shifts.
func eventClassTime(event ProgramClassEvent, rRule *rrule.RRule, occurrences []time.Time, loc *time.Location) (string, int, int) {
	if len(occurrences) == 0 {
		return "", 0, 0
	}
	duration, err := time.ParseDuration(event.Duration)
	if err != nil {
		logrus.Errorf("error parsing duration for event: %v", err)
	}
	dtStart := rRule.GetDTStart()
	canonicalHour := dtStart.Hour()
	canonicalMinute := dtStart.Minute()

	firstOcc := occurrences[0]
	consistentOccurrence := time.Date(
		firstOcc.Year(), firstOcc.Month(), firstOcc.Day(),
		canonicalHour, canonicalMinute, 0, 0, loc,
	)
	startTimeStr := consistentOccurrence.Format("15:04")
	endTimeStr := consistentOccurrence.Add(duration).Format("15:04")
	return startTimeStr + "-" + endTimeStr, canonicalHour, canonicalMinute
}

// dedupeClassEventInstances merges instances from multiple rows. On a date+start-time
// collision the newest row wins (rows arrive oldest-first); sorted by date descending.
func dedupeClassEventInstances(instances []ClassEventInstance) []ClassEventInstance {
	type instanceKey struct{ date, start string }
	indexByKey := make(map[instanceKey]int, len(instances))
	result := make([]ClassEventInstance, 0, len(instances))
	for _, inst := range instances {
		start := inst.ClassTime
		if dash := strings.Index(start, "-"); dash != -1 {
			start = start[:dash]
		}
		key := instanceKey{date: inst.Date, start: start}
		if idx, ok := indexByKey[key]; ok {
			result[idx] = inst
			continue
		}
		indexByKey[key] = len(result)
		result = append(result, inst)
	}
	slices.SortFunc(result, func(a, b ClassEventInstance) int {
		return cmp.Compare(b.Date, a.Date)
	})
	return result
}

func (db *Store) GetCancelledOverrideEvents(qryCtx *QueryContext, eventId int) ([]ProgramClassEventOverride, error) {
	overrides := make([]ProgramClassEventOverride, 0)
	if err := db.WithContext(qryCtx.Ctx).Model(&ProgramClassEventOverride{}).Where("event_id = ? and is_cancelled = true", eventId).Find(&overrides).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_event_overrides")
	}
	return overrides, nil
}

func (db *Store) GetClassEventDatesForRecurrence(classID int, timezone string, month, year string, eventId *int) ([]EventDates, error) {
	// Load all rows so a multi-row class shows every date; honor a specific event_id if given.
	var events []ProgramClassEvent
	query := db.Preload("Overrides").Where("cohort_id = ?", classID)
	if eventId != nil {
		query = query.Where("id = ?", *eventId)
	}
	if err := query.Order("created_at ASC").Find(&events).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_events")
	}
	if len(events) == 0 {
		return nil, newGetRecordsDBError(gorm.ErrRecordNotFound, "program_class_events")
	}

	loc, _ := time.LoadLocation(timezone)
	y, _ := strconv.Atoi(year)
	m, _ := strconv.Atoi(month)
	start := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, loc)
	until := start.AddDate(0, 1, 0)

	out := make([]EventDates, 0)
	for i := range events {
		rule, err := events[i].GetRRuleWithTimezone(timezone)
		if err != nil {
			logrus.Errorf("event has invalid rule, event: %v", events[i])
			continue
		}
		out = append(out, eventDatesForRow(events[i], rule, start, until, loc)...)
	}

	return dedupeEventDates(out), nil
}

func eventDatesForRow(event ProgramClassEvent, rule *rrule.RRule, start, until time.Time, loc *time.Location) []EventDates {
	// A fully cancelled series contributes no scheduled dates.
	if event.IsCancelled {
		return nil
	}
	occs := rule.Between(start, until, true)

	duration, err := time.ParseDuration(event.Duration)
	if err != nil {
		logrus.Errorf("error parsing duration for event: %v", err)
		duration = time.Hour
	}

	var canonicalHour, canonicalMinute int
	if len(occs) > 0 {
		dtstart := rule.GetDTStart()
		canonicalHour = dtstart.Hour()
		canonicalMinute = dtstart.Minute()
	}

	out := make([]EventDates, 0, len(occs))
	for _, occ := range occs {
		isCancelled, _, _, _, _ := checkEventCancelledAndRescheduled(occ, event.Overrides, loc.String())
		if isCancelled {
			continue
		}
		occInLoc := occ.In(loc)
		// lock the hour/minute to the canonical values to avoid DST shift :(
		consistentStart := time.Date(
			occInLoc.Year(),
			occInLoc.Month(),
			occInLoc.Day(),
			canonicalHour,
			canonicalMinute,
			0,
			0,
			loc,
		)
		startStr := consistentStart.Format("15:04")
		endStr := consistentStart.Add(duration).Format("15:04")
		out = append(out, EventDates{
			EventID:   event.ID,
			Date:      consistentStart.Format("2006-01-02"),
			ClassTime: startStr + "-" + endStr,
		})
	}

	//checking the overrides for rescheduled dates (non-cancelled overrides are reschedule targets)
	for _, override := range event.Overrides {
		if override.IsCancelled {
			continue
		}
		rRule, err := rrule.StrToRRule(override.OverrideRrule)
		if err != nil || len(rRule.All()) == 0 {
			continue
		}
		overrideDate := rRule.All()[0].In(loc)
		if overrideDate.Before(start) || !overrideDate.Before(until) {
			continue
		}
		dur, err := time.ParseDuration(override.Duration)
		if err != nil {
			logrus.Errorf("error parsing override duration for event: %v", err)
			dur = duration
		}
		classTime := overrideDate.Format("15:04") + "-" + overrideDate.Add(dur).Format("15:04")
		out = append(out, EventDates{
			EventID:   event.ID,
			Date:      overrideDate.Format("2006-01-02"),
			ClassTime: classTime,
		})
	}

	return out
}

// dedupeEventDates drops entries sharing a date and class time; newest row wins on a tie.
func dedupeEventDates(dates []EventDates) []EventDates {
	type dateKey struct{ date, classTime string }
	indexByKey := make(map[dateKey]int, len(dates))
	result := make([]EventDates, 0, len(dates))
	for _, d := range dates {
		key := dateKey{date: d.Date, classTime: d.ClassTime}
		if idx, ok := indexByKey[key]; ok {
			result[idx] = d
			continue
		}
		indexByKey[key] = len(result)
		result = append(result, d)
	}
	return result
}

func (db *Store) GetProgramClassEventOverrides(qryCtx *QueryContext, eventIDs ...uint) ([]ProgramClassEventOverride, error) {
	overrides := make([]ProgramClassEventOverride, 0)
	if err := db.WithContext(qryCtx.Ctx).Model(&ProgramClassEventOverride{}).Preload("RoomRef").Preload("Instructor").Where("event_id IN (?)", eventIDs).Order("id desc").Find(&overrides).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_event_overrides")
	}
	return overrides, nil
}

func (db *Store) UpdateClassEventRRuleUntilDate(tx *gorm.DB, classIDs []int, completionTime time.Time) error {
	if len(classIDs) == 0 {
		return newDBError(errors.New("no class IDs provided"), "no class IDs provided")
	}

	var events []struct {
		ID             uint   `json:"id"`
		RecurrenceRule string `json:"recurrence_rule"`
	}

	if err := tx.Model(&ProgramClassEvent{}).Select("id, recurrence_rule").Where("cohort_id IN (?)", classIDs).Find(&events).Error; err != nil {
		return newGetRecordsDBError(err, "program_class_events")
	}

	if len(events) == 0 {
		return newDBError(errors.New("no events found for the provided class IDs"), "no events found for the provided class IDs")
	}

	untilDateStr := FormatDateForUntil(completionTime)
	updates := make(map[uint]string, len(events))
	eventIDs := make([]uint, len(events))
	for i, event := range events {
		eventIDs[i] = event.ID
		updates[event.ID] = ReplaceOrAddUntilDate(event.RecurrenceRule, untilDateStr)
	}

	caseParts := make([]string, 0, len(updates))
	args := make([]any, 0, len(updates)*2)

	for eventID, newRRule := range updates {
		caseParts = append(caseParts, "WHEN id = ? THEN ?")
		args = append(args, eventID, newRRule)
	}

	caseSQL := "CASE " + strings.Join(caseParts, " ") + " END"
	if err := tx.Model(&ProgramClassEvent{}).Where("id IN (?)", eventIDs).Update("recurrence_rule", gorm.Expr(caseSQL, args...)).Error; err != nil {
		return newUpdateDBError(err, "program_class_events")
	}
	return nil
}

func getRoomName(roomRef *Room) string {
	if roomRef != nil {
		return roomRef.Name
	}
	return "TBD"
}
