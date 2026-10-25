package programs

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-contracts/users"
)

func (srv *server) handleGetAdminCalendar(w http.ResponseWriter, r *http.Request, log sLog) error {
	dtRng, err := getDateRange(r)
	if err != nil {
		return newInvalidQueryParamServiceError(err, "start_dt")
	}
	args := facilityScopedQueryContext(r)

	id := r.URL.Query().Get("cohort_id")
	var classID int
	if id != "" {
		classID, err = strconv.Atoi(id)
		if err != nil {
			return newInvalidIdServiceError(err, "class_id")
		}
	}

	events, err := srv.store.GetFacilityCalendar(&args, dtRng, classID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	// Merge live provider calendar events when not filtered to a single class.
	// These are read from the cache rather than fetched here, so the calendar
	// answers with our own events immediately; while a refresh runs the response
	// carries a placeholder per provider instead of blocking on it.
	claims := claimsFrom(r)
	if classID == 0 && claims.HasFeature(features.ProviderPlatforms) {
		canvasEvents, loading := srv.getCanvasProviderEvents(dtRng, args.FacilityID)
		events = append(events, canvasEvents...)
		if loading {
			events = append(events, srv.loadingEventPlaceholders()...)
		}
	}
	return writeJsonResponse(w, http.StatusOK, events)
}

func (srv *server) handleGetStudentCalendar(w http.ResponseWriter, r *http.Request, log sLog) error {
	dtRng, err := getDateRange(r)
	if err != nil {
		return newInvalidQueryParamServiceError(err, "start_dt")
	}
	args := getQueryContext(r)
	events, err := srv.store.GetFacilityCalendar(&args, dtRng, 0)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, events)
}

func (srv *server) handleGetTodaysSchedule(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := getQueryContext(r)
	facility := r.URL.Query().Get("facility")
	var facilityId *uint
	switch facility {
	case "all":
		facilityId = nil
	default:
		facilityId = &args.FacilityID
	}

	service := NewClassesService(srv.store)
	items, err := service.GetTodaysSchedule(&args, facilityId)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	args.Total = int64(len(items))
	if !args.All {
		start := args.CalcOffset()
		if start < len(items) {
			end := start + args.PerPage
			if end > len(items) {
				end = len(items)
			}
			items = items[start:end]
		} else {
			items = []TodaysScheduleItem{}
		}
	}

	return writePaginatedResponse(w, http.StatusOK, items, args.IntoMeta())
}

func (srv *server) handleEventOverrides(w http.ResponseWriter, r *http.Request, log sLog) error {
	eventId, err := strconv.Atoi(r.PathValue("event_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "event_id")
	}
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	cannotCreateOverride, err := srv.cannotUpdateEvent(classID)
	if err != nil {
		return err
	}
	if cannotCreateOverride {
		return newBadRequestServiceError(errors.New("cannot create an event override for a completed or cancelled class"), "cannot create event override")
	}
	var overrides []*ProgramClassEventOverride
	if err := json.NewDecoder(r.Body).Decode(&overrides); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	for i, j := 0, len(overrides); i < j; i++ {
		overrides[i].EventID = uint(eventId)
		overrides[i].CohortID = uint(classID)
	}
	// The room is only worth re-checking when it actually changes. The schedule's
	// single-session editor always sends the CURRENT room_id alongside whatever field is
	// being edited (useChangeEventField.ts: `room_id: event.room_id ?? null, ...fieldPatch`),
	// so changing only the INSTRUCTOR used to run a room-only conflict check
	// (CheckRRuleConflicts ignores InstructorID entirely) and report a room clash for an
	// edit that never touched the room.
	// Deliberately NOT fatal: this lookup is an optimisation, and the handler never needed
	// the event row before. If it cannot be read we fall back to the old behaviour and run
	// the check -- erring toward checking too much, never toward skipping a real conflict.
	var currentRoomID *uint
	if currentEvent, err := srv.store.GetEventById(eventId); err == nil {
		currentRoomID = currentEvent.RoomID
	}
	roomChanged := func(override *ProgramClassEventOverride) bool {
		if override.IsCancelled || override.RoomID == nil {
			return false
		}
		if currentRoomID == nil {
			return true
		}
		return *override.RoomID != *currentRoomID
	}

	var class *ProgramClassCohort
	for _, override := range overrides {
		if roomChanged(override) {
			var err error
			class, err = srv.store.GetCohortByID(classID)
			if err != nil {
				return newDatabaseServiceError(err)
			}
			break
		}
	}
	for _, override := range overrides {
		if !roomChanged(override) {
			continue
		}
		if _, err := srv.store.GetRoomByIDForFacility(*override.RoomID, class.FacilityID); err != nil {
			return newDatabaseServiceError(err)
		}
		eventIDUint := uint(eventId)
		classIDUint := uint(classID)
		conflicts, err := srv.store.CheckRRuleConflicts(&ConflictCheckRequest{
			FacilityID:     class.FacilityID,
			RoomID:         *override.RoomID,
			RecurrenceRule: override.OverrideRrule,
			Duration:       override.Duration,
			ExcludeEventID: &eventIDUint,
			ExcludeClassID: &classIDUint,
		})
		if err != nil {
			return newDatabaseServiceError(err)
		}
		if len(conflicts) > 0 {
			return writeConflictResponse(w, conflicts)
		}
	}
	ctx := getQueryContext(r)
	if err := srv.WithUserContext(r).CreateOverrideEvents(&ctx, overrides); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Override(s) created successfully")
}

type patchEventOverrideRequest struct {
	Date         string `json:"date"`
	StartTime    string `json:"start_time"`
	IsCancelled  bool   `json:"is_cancelled"`
	Reason       string `json:"reason"`
	RoomID       *uint  `json:"room_id"`
	InstructorID *uint  `json:"instructor_id"`
	NewDate      string `json:"new_date"`
	NewStartTime string `json:"new_start_time"`
	NewEndTime   string `json:"new_end_time"`
}

func (srv *server) handlePatchEventOverride(w http.ResponseWriter, r *http.Request, log sLog) error {
	eventId, err := strconv.Atoi(r.PathValue("event_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "event_id")
	}
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	cannotCreateOverride, err := srv.cannotUpdateEvent(classID)
	if err != nil {
		return err
	}
	if cannotCreateOverride {
		return newBadRequestServiceError(errors.New("cannot create an event override for a completed or cancelled class"), "cannot create event override")
	}
	var req patchEventOverrideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if req.Date == "" {
		return newBadRequestServiceError(errors.New("date is required"), "date is required")
	}
	if len(req.Reason) > 255 {
		return newBadRequestServiceError(errors.New("reason exceeds maximum length of 255 characters"), "reason exceeds maximum length of 255 characters")
	}
	if req.InstructorID != nil {
		class, err := srv.store.GetCohortByID(classID)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		claims := claimsFrom(r)
		allowedRoles := []users.Role{users.FacilityAdmin, users.DepartmentAdmin}
		if claims.Role == users.FacilityAdmin {
			allowedRoles = []users.Role{users.FacilityAdmin}
		}
		name, err := srv.store.GetInstructorNameByID(*req.InstructorID, class.FacilityID, allowedRoles...)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		if name == "" {
			return newForbiddenServiceError(errors.New("instructor not authorized for this facility"), "instructor not authorized for this facility")
		}
	}
	ctx := getQueryContext(r)
	event, err := srv.store.GetEventById(eventId)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	eventRule, err := event.GetRRuleWithTimezone(ctx.Timezone)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	// Use GetDTStart() (same as createEventInstances) to get the canonical time.
	// OrigOptions.Dtstart.In(tz) can give a different hour due to DST on the
	// DTSTART date vs the target date. GetDTStart() is what the instance generator uses.
	eventStart := eventRule.GetDTStart()
	canonicalHour := eventStart.Hour()
	canonicalMinute := eventStart.Minute()

	buildRRule := func(dateStr string, optionalTime string) (string, error) {
		dateOnly, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return "", err
		}
		hour, minute := canonicalHour, canonicalMinute
		if optionalTime != "" {
			parts := strings.Split(optionalTime, ":")
			if len(parts) == 2 {
				hour, _ = strconv.Atoi(parts[0])
				minute, _ = strconv.Atoi(parts[1])
			}
		}
		cleanDate := dateOnly.Format("20060102")
		return fmt.Sprintf("DTSTART;TZID=%s:%sT%02d%02d00\nRRULE:FREQ=DAILY;COUNT=1",
			ctx.Timezone, cleanDate, hour, minute), nil
	}

	originalRRule, err := buildRRule(req.Date, req.StartTime)
	if err != nil {
		return newBadRequestServiceError(err, "invalid date format")
	}

	if req.NewDate != "" {
		newRRule, err := buildRRule(req.NewDate, req.NewStartTime)
		if err != nil {
			return newBadRequestServiceError(err, "invalid new_date format")
		}
		newDuration := event.Duration
		if req.NewStartTime != "" && req.NewEndTime != "" {
			startParts := strings.Split(req.NewStartTime, ":")
			endParts := strings.Split(req.NewEndTime, ":")
			if len(startParts) == 2 && len(endParts) == 2 {
				sh, _ := strconv.Atoi(startParts[0])
				sm, _ := strconv.Atoi(startParts[1])
				eh, _ := strconv.Atoi(endParts[0])
				em, _ := strconv.Atoi(endParts[1])
				dur := time.Duration(eh-sh)*time.Hour + time.Duration(em-sm)*time.Minute
				if dur > 0 {
					newDuration = dur.String()
				}
			}
		}
		newRoomID := event.RoomID
		if req.RoomID != nil {
			newRoomID = req.RoomID
		}
		newInstructorID := event.InstructorID
		if req.InstructorID != nil {
			newInstructorID = req.InstructorID
		}
		rescheduleReason := "rescheduled"
		if req.Reason != "" {
			rescheduleReason = req.Reason
		}
		overrides := []*ProgramClassEventOverride{
			{
				EventID:       uint(eventId),
				CohortID:      uint(classID),
				Duration:      event.Duration,
				OverrideRrule: originalRRule,
				IsCancelled:   true,
				Reason:        StringPtr(rescheduleReason),
			},
			{
				EventID:       uint(eventId),
				CohortID:      uint(classID),
				Duration:      newDuration,
				OverrideRrule: newRRule,
				IsCancelled:   false,
				RoomID:        newRoomID,
				InstructorID:  newInstructorID,
			},
		}
		if err := srv.WithUserContext(r).CreateOverrideEvents(&ctx, overrides); err != nil {
			return newDatabaseServiceError(err)
		}
		return writeJsonResponse(w, http.StatusOK, "Event rescheduled successfully")
	}

	overrideDuration := event.Duration
	overrideRRule := originalRRule
	if req.NewStartTime != "" {
		overrideRRule, err = buildRRule(req.Date, req.NewStartTime)
		if err != nil {
			return newBadRequestServiceError(err, "invalid start time")
		}
		if req.NewEndTime != "" {
			startParts := strings.Split(req.NewStartTime, ":")
			endParts := strings.Split(req.NewEndTime, ":")
			if len(startParts) == 2 && len(endParts) == 2 {
				sh, _ := strconv.Atoi(startParts[0])
				sm, _ := strconv.Atoi(startParts[1])
				eh, _ := strconv.Atoi(endParts[0])
				em, _ := strconv.Atoi(endParts[1])
				dur := time.Duration(eh-sh)*time.Hour + time.Duration(em-sm)*time.Minute
				if dur > 0 {
					overrideDuration = dur.String()
				}
			}
		}
	}
	override := &ProgramClassEventOverride{
		EventID:       uint(eventId),
		CohortID:      uint(classID),
		Duration:      overrideDuration,
		OverrideRrule: overrideRRule,
		IsCancelled:   req.IsCancelled,
		Reason:        NilIfBlank(req.Reason),
		RoomID:        req.RoomID,
		InstructorID:  req.InstructorID,
	}
	if err := srv.WithUserContext(r).CreateOverrideEvents(&ctx, []*ProgramClassEventOverride{override}); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Override created successfully")
}

func (srv *server) handleDeleteEventOverride(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("event_override_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "event override ID")
	}
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	cannotDelete, err := srv.cannotUpdateEvent(classID)
	if err != nil {
		return err
	}
	if cannotDelete {
		return newBadRequestServiceError(errors.New("cannot delete an event for a completed or cancelled class"), "cannot delete event")
	}
	log.add("class_id", classID)
	log.add("event_override_id", id)
	args := getQueryContext(r)
	undoAppliedFuture := r.URL.Query().Get("undo_applied_future") == "true"
	err = srv.WithUserContext(r).DeleteOverrideEvent(&args, id, classID, undoAppliedFuture)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusNoContent, "Event override(s) deleted successfully")
}

func (srv *server) handleUncancelSeries(w http.ResponseWriter, r *http.Request, log sLog) error {
	eventID, err := strconv.Atoi(r.PathValue("event_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "event_id")
	}
	var req struct {
		RestoreDate string `json:"restore_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if req.RestoreDate == "" {
		return newBadRequestServiceError(errors.New("restore date is required"), "restore date is required")
	}
	ctx := getQueryContext(r)
	if err := srv.store.UncancelEventSeries(&ctx, uint(eventID), req.RestoreDate); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Series uncancelled successfully")
}

func (srv *server) handleUncancelOverride(w http.ResponseWriter, r *http.Request, log sLog) error {
	overrideID, err := strconv.Atoi(r.PathValue("event_override_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "event override ID")
	}
	ctx := getQueryContext(r)
	if err := srv.store.UncancelOverride(&ctx, uint(overrideID)); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Override uncancelled successfully")
}

func (srv *server) handleCreateEvent(w http.ResponseWriter, r *http.Request, log sLog) error {
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class_id")
	}
	cannotCreate, err := srv.cannotUpdateEvent(classID)
	if err != nil {
		return err
	}
	if cannotCreate {
		return newBadRequestServiceError(errors.New("cannot create new event for a completed or cancelled class"), "cannot create event")
	}
	event := &ProgramClassEvent{}
	if err := json.NewDecoder(r.Body).Decode(event); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	event.CohortID = uint(classID)
	if event.RoomID != nil || event.InstructorID != nil {
		class, err := srv.store.GetCohortByID(classID)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		conflictReq := &ConflictCheckRequest{
			FacilityID:     class.FacilityID,
			RecurrenceRule: event.RecurrenceRule,
			Duration:       event.Duration,
		}
		if event.RoomID != nil {
			if _, err := srv.store.GetRoomByIDForFacility(*event.RoomID, class.FacilityID); err != nil {
				return newDatabaseServiceError(err)
			}
			conflictReq.RoomID = *event.RoomID
		}
		if event.InstructorID != nil {
			if name, err := srv.store.GetInstructorNameByID(*event.InstructorID, class.FacilityID); err != nil || name == "" {
				return newBadRequestServiceError(err, "invalid instructor for this facility")
			}
			conflictReq.InstructorID = *event.InstructorID
		}
		conflicts, err := srv.store.CheckConflicts(conflictReq)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		if len(conflicts) > 0 {
			return writeConflictResponse(w, conflicts)
		}
	}
	_, err = srv.WithUserContext(r).CreateNewEvent(classID, event)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusCreated, "Event created successfully")
}

func (srv *server) handleRescheduleEventSeries(w http.ResponseWriter, r *http.Request, log sLog) error {
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class_id")
	}
	cannotUpdate, err := srv.cannotUpdateEvent(classID)
	if err != nil {
		return err
	}
	if cannotUpdate {
		return newBadRequestServiceError(errors.New("cannot reschedule event series for a completed or cancelled class"), "cannot update event")
	}
	var eventSeriesRequest struct {
		EventSeries       ProgramClassEvent `json:"event_series"`
		ClosedEventSeries ProgramClassEvent `json:"closed_event_series"`
	}
	if err := json.NewDecoder(r.Body).Decode(&eventSeriesRequest); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	// A cancelled series frees the room, so skip the conflict check for cancellations.
	if eventSeriesRequest.EventSeries.RoomID != nil {
		class, err := srv.store.GetCohortByID(classID)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		if _, err := srv.store.GetRoomByIDForFacility(*eventSeriesRequest.EventSeries.RoomID, class.FacilityID); err != nil {
			return newDatabaseServiceError(err)
		}
		if !eventSeriesRequest.EventSeries.IsCancelled {
			var excludeEventID *uint
			if eventSeriesRequest.ClosedEventSeries.ID > 0 {
				closedID := eventSeriesRequest.ClosedEventSeries.ID
				excludeEventID = &closedID
			} else if eventSeriesRequest.EventSeries.ID > 0 {
				excludeEventID = &eventSeriesRequest.EventSeries.ID
			}
			conflicts, err := srv.store.CheckRRuleConflicts(&ConflictCheckRequest{
				FacilityID:     class.FacilityID,
				RoomID:         *eventSeriesRequest.EventSeries.RoomID,
				RecurrenceRule: eventSeriesRequest.EventSeries.RecurrenceRule,
				Duration:       eventSeriesRequest.EventSeries.Duration,
				ExcludeEventID: excludeEventID,
			})
			if err != nil {
				return newDatabaseServiceError(err)
			}
			if len(conflicts) > 0 {
				return writeConflictResponse(w, conflicts)
			}
		}
	}
	args := getQueryContext(r)
	eventSeriesRequest.EventSeries.CohortID = uint(classID)
	eventSeriesRequest.ClosedEventSeries.CohortID = uint(classID)
	err = srv.WithUserContext(r).CreateRescheduleEventSeries(&args, uint(classID), eventSeriesRequest.EventSeries, eventSeriesRequest.ClosedEventSeries)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusCreated, "Event rescheduled successfully")
}

func (srv *server) cannotUpdateEvent(classID int) (bool, error) {
	class, err := srv.store.GetCohortByID(classID)
	if err != nil {
		return false, newDatabaseServiceError(err)
	}
	return class.Status == Completed || class.Status == Cancelled, nil
}

func (srv *server) handleGetProgramClassEvents(w http.ResponseWriter, r *http.Request, log sLog) error {
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class_id")
	}
	if uint(classID) >= CanvasClassIDOffset {
		args := getQueryContext(r)
		return writePaginatedResponse(w, http.StatusOK,
			[]ProgramClassEvent{},
			newPaginationInfo(1, args.PerPage, 0))
	}
	claims := claimsFrom(r)
	var userId *int
	if !claims.IsAdmin() {
		tmp := int(claims.UserID)
		userId = &tmp
	} else {
		userIdStr := r.URL.Query().Get("user_id")
		if userIdStr != "" {
			if parsedUserId, err := strconv.Atoi(userIdStr); err == nil {
				userId = &parsedUserId
			} else {
				log.errorf("Error parsing user id: %v", err)
			}
		}
	}

	log.add("class id", classID)
	month := r.URL.Query().Get("month")
	year := r.URL.Query().Get("year")
	justDates := r.URL.Query().Get("dates")
	if justDates == "true" {
		timezone := getQueryContext(r).Timezone
		// Check for optional event_id parameter to get dates for a specific event
		eventIdStr := r.URL.Query().Get("event_id")
		var eventId *int
		if eventIdStr != "" {
			if parsedEventId, err := strconv.Atoi(eventIdStr); err == nil {
				eventId = &parsedEventId
			} else {
				log.errorf("Error parsing event_id: %v", err)
			}
		}
		dates, err := srv.store.GetClassEventDatesForRecurrence(classID, timezone, month, year, eventId)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		return writeJsonResponse(w, http.StatusOK, dates)
	}

	qryCtx := getQueryContext(r)
	allInstances := r.URL.Query().Get("all") == "true"
	instances, err := srv.store.GetClassEventInstancesWithAttendanceForRecurrence(classID, &qryCtx, month, year, userId, allInstances)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	return writePaginatedResponse(w, http.StatusOK, instances, qryCtx.IntoMeta())
}
