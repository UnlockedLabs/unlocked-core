package programs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// O(N+1) query problem in this method, but it is not a performance critical endpoint
// and the number of enrollments is usually small, so for now we're going to live with it.
func (srv *server) handleAddAttendanceForEvent(w http.ResponseWriter, r *http.Request, log sLog) error {
	classId, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	class, err := srv.store.GetCohortByID(classId)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if class.CannotUpdateClass() {
		return newBadRequestServiceError(err, "cannot perform action on class that is completed cancelled or archived")
	}
	eventID, err := strconv.Atoi(r.PathValue("event_id"))
	if err != nil {
		return newBadRequestServiceError(err, "event ID")
	}
	var attendances []ProgramClassEventAttendance
	err = json.NewDecoder(r.Body).Decode(&attendances)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	const isoLayout = "2006-01-02"
	now := time.Now().In(time.Local)
	endOfToday := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, time.Local)

	args := getQueryContext(r)
	overrides, err := srv.store.GetProgramClassEventOverrides(&args, []uint{uint(eventID)}...)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	uniqueUserIDs := make(map[uint]struct{})
	for i := range attendances {
		if attendances[i].UserID != 0 {
			uniqueUserIDs[attendances[i].UserID] = struct{}{}
		}
	}

	userIDSlice := make([]uint, 0, len(uniqueUserIDs))
	for uid := range uniqueUserIDs {
		userIDSlice = append(userIDSlice, uid)
	}

	enrolledIDs, err := srv.store.GetEnrolledUserIDsForClass(uint(classId), userIDSlice)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	enrolledSet := make(map[uint]struct{}, len(enrolledIDs))
	for _, uid := range enrolledIDs {
		enrolledSet[uid] = struct{}{}
	}
	event, err := srv.store.GetEventById(eventID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	for i := range attendances {
		attendances[i].Note = NilIfBlank(FormatNullableString(attendances[i].Note))
		attendances[i].ReasonCategory = NilIfBlank(FormatNullableString(attendances[i].ReasonCategory))
		if attendances[i].Date == "" {
			attendances[i].Date = time.Now().Format("2006-01-02")
		}

		parsed, err := time.ParseInLocation(isoLayout, attendances[i].Date, time.Local)
		if err != nil {
			return newBadRequestServiceError(err, "unable to parse time")
		}

		if parsed.After(endOfToday) {
			return writeJsonResponse(w, http.StatusUnprocessableEntity, "attempted attendance date in future")
		}
		if isDateCancelled(overrides, attendances[i].Date) {
			return writeJsonResponse(w, http.StatusConflict, "cannot record attendance for cancelled class date")
		}

		if _, ok := enrolledSet[attendances[i].UserID]; !ok {
			// just logging this since we may need to take past classes attendance
			log.warnf("user %d is not enrolled in class %d", attendances[i].UserID, classId)
		}
		attendances[i].EventID = uint(eventID)
		overrideForDate := findOverrideForDate(overrides, attendances[i].Date)
		scheduledMinutes := deriveScheduledMinutes(event, overrideForDate)
		if err := applyTimeTracking(&attendances[i], scheduledMinutes); err != nil {
			return err
		}
	}
	if err := srv.WithUserContext(r).LogUserAttendance(attendances, args.Ctx, &args.UserID, class.ClassName); err != nil {
		return newDatabaseServiceError(err)
	}

	return writeJsonResponse(w, http.StatusOK, "Attendance updated")
}

func (srv *server) handleDeleteAttendee(w http.ResponseWriter, r *http.Request, log sLog) error {
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newBadRequestServiceError(err, "class ID")
	}
	class, err := srv.store.GetCohortByID(classID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	eventID, err := strconv.Atoi(r.PathValue("event_id"))
	if err != nil {
		return newBadRequestServiceError(err, "event ID")
	}
	userID, err := strconv.Atoi(r.PathValue("user_id"))
	if err != nil {
		return newBadRequestServiceError(err, "user ID")
	}
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	args := getQueryContext(r)
	overrides, err := srv.store.GetProgramClassEventOverrides(&args, []uint{uint(eventID)}...)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if isDateCancelled(overrides, date) {
		return writeJsonResponse(w, http.StatusConflict, "cannot delete attendance for cancelled class date")
	}

	var enrollment ProgramClassEnrollment
	enrollmentErr := srv.store.Where("cohort_id = ? AND user_id = ? AND enrollment_status = ?",
		classID, userID, Enrolled).First(&enrollment).Error
	if enrollmentErr != nil {
		return writeJsonResponse(w, http.StatusBadRequest, "user is not enrolled in class")
	}

	rowsAffected, err := srv.WithUserContext(r).DeleteAttendance(eventID, userID, date, args.Ctx, &args.UserID, class.ClassName)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	if rowsAffected == 0 {
		return writeJsonResponse(w, http.StatusBadRequest, "user is not enrolled in class or has no attendance record for this date")
	}

	return writeJsonResponse(w, http.StatusNoContent, any(nil))
}

func (srv *server) handleGetEventAttendance(w http.ResponseWriter, r *http.Request, log sLog) error {
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newBadRequestServiceError(err, "class_id")
	}
	log.add("class id", classID)
	eventID, err := strconv.Atoi(r.PathValue("event_id"))
	if err != nil {
		return newBadRequestServiceError(err, "event_id")
	}
	log.add("event id", eventID)
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	log.add("date", date)

	args := getQueryContext(r)

	class, err := srv.store.GetCohortByID(classID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	includeCurrentlyEnrolled := class.Status != Active

	overrides, err := srv.store.GetProgramClassEventOverrides(&args, []uint{uint(eventID)}...)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if isDateCancelled(overrides, date) { //check overrides for cancelled date
		return writePaginatedResponse(w, http.StatusConflict, []EnrollmentAttendance{}, args.IntoMeta())
	}

	combined, err := srv.store.GetEnrollmentsWithAttendanceForEvent(&args, classID, eventID, date, includeCurrentlyEnrolled)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writePaginatedResponse(w, http.StatusOK, combined, args.IntoMeta())
}

func isDateCancelled(overrides []ProgramClassEventOverride, eventDate string) bool {
	formatPattern := "2006-01-02"
	evtDate, err := time.Parse(formatPattern, eventDate) ///making sure the date is good
	if err != nil {
		return false
	}

	var isRescheduled bool
	for _, override := range overrides {
		rRule, err := rrule.StrToRRule(override.OverrideRrule)
		if err != nil {
			continue
		}
		if len(rRule.All()) == 0 {
			continue
		}
		overrideDate := rRule.All()[0].Format(formatPattern)

		if overrideDate == evtDate.Format(formatPattern) {
			if override.IsCancelled {
				if FormatNullableString(override.Reason) == "rescheduled" {
					isRescheduled = true
					break
				}
				return true // a true cancellation
			}
		}
	}

	if isRescheduled { //check for reschedules on same date
		for _, override := range overrides {
			rRule, err := rrule.StrToRRule(override.OverrideRrule)
			if err != nil {
				continue
			}
			if len(rRule.All()) == 0 {
				continue
			}
			overrideDate := rRule.All()[0].Format(formatPattern)
			if overrideDate == evtDate.Format(formatPattern) && !override.IsCancelled { //is it rescheduled on the same date??
				return false
			}
		}
		return true
	}
	return false
}

func findOverrideForDate(overrides []ProgramClassEventOverride, eventDate string) *ProgramClassEventOverride {
	for i := range overrides {
		rRule, err := rrule.StrToRRule(overrides[i].OverrideRrule)
		if err != nil || len(rRule.All()) == 0 {
			continue
		}
		if rRule.All()[0].Format("2006-01-02") == eventDate {
			return &overrides[i]
		}
	}
	return nil
}

func deriveScheduledMinutes(event *ProgramClassEvent, override *ProgramClassEventOverride) int {
	getMinutes := func(durationStr string) int {
		duration, err := time.ParseDuration(durationStr)
		if err != nil || duration <= 0 {
			return 0
		}
		return int(duration.Minutes())
	}
	if override != nil && override.Duration != "" {
		if mins := getMinutes(override.Duration); mins > 0 {
			return mins
		}
	}
	return getMinutes(event.Duration)
}

func normalizeTimeValue(val *string) string {
	if val == nil {
		return ""
	}
	return strings.TrimSpace(*val)
}

func applyTimeTracking(attendance *ProgramClassEventAttendance, scheduledMinutes int) error {
	checkIn := normalizeTimeValue(attendance.CheckInAt)
	checkOut := normalizeTimeValue(attendance.CheckOutAt)

	const timeLayout = "15:04"
	var minutesAttended *int

	if checkIn == "" && checkOut == "" {
		if attendance.AttendanceStatus == Present || attendance.AttendanceStatus == Partial {
			mins := scheduledMinutes
			minutesAttended = &mins
		}
	} else if checkIn != "" && checkOut == "" {
		// need to allow saving an in-progress attendance with only a check-in time
		attendance.CheckInAt = &checkIn
	} else {
		if checkIn == "" || checkOut == "" {
			return newBadRequestServiceError(fmt.Errorf("invalid time range"), "check-in and check-out times are both required for time-based attendance")
		}
		parsedIn, err := time.Parse(timeLayout, checkIn)
		if err != nil {
			return newBadRequestServiceError(err, "check_in_at must be in HH:MM format")
		}
		parsedOut, err := time.Parse(timeLayout, checkOut)
		if err != nil {
			return newBadRequestServiceError(err, "check_out_at must be in HH:MM format")
		}
		diff := int(parsedOut.Sub(parsedIn).Minutes())
		if diff <= 0 {
			return newBadRequestServiceError(fmt.Errorf("negative duration"), "check_out_at must be after check_in_at")
		}
		minutesAttended = &diff
		if scheduledMinutes <= 0 {
			scheduledMinutes = *minutesAttended
		}
		if *minutesAttended > scheduledMinutes {
			minutes := scheduledMinutes
			minutesAttended = &minutes
		}
		attendance.CheckInAt = &checkIn
		attendance.CheckOutAt = &checkOut
	}

	attendance.MinutesAttended = minutesAttended
	attendance.ScheduledMinutes = &scheduledMinutes

	if minutesAttended == nil {
		if attendance.AttendanceStatus == "" {
			if checkIn != "" {
				attendance.AttendanceStatus = Present
			} else {
				attendance.AttendanceStatus = Absent_Unexcused
			}
		}
		if attendance.AttendanceStatus == Partial {
			return newBadRequestServiceError(fmt.Errorf("missing minutes for partial attendance"), "partial attendance requires check-in and check-out times")
		}
		return nil
	}

	minutes := *minutesAttended

	if attendance.AttendanceStatus == "" {
		switch {
		case minutes == 0:
			attendance.AttendanceStatus = Absent_Unexcused
		case scheduledMinutes > 0 && minutes < scheduledMinutes:
			attendance.AttendanceStatus = Partial
		default:
			attendance.AttendanceStatus = Present
		}
	}

	if attendance.AttendanceStatus == Present && scheduledMinutes > 0 && minutes > 0 && minutes < scheduledMinutes {
		attendance.AttendanceStatus = Partial
	}

	if attendance.AttendanceStatus == Partial && minutes == 0 {
		return newBadRequestServiceError(fmt.Errorf("missing minutes for partial attendance"), "partial attendance requires check-in and check-out times")
	}

	return nil
}

func (srv *server) handleGetAttendanceRateForEvent(w http.ResponseWriter, r *http.Request, log sLog) error {
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	eventID, err := strconv.Atoi(r.PathValue("event_id"))
	if err != nil {
		return newInvalidQueryParamServiceError(err, "event ID")
	}
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	attendanceRate, err := srv.store.GetAttendanceRateForEvent(r.Context(), eventID, classID, date)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	response := map[string]float64{
		"attendance_rate": attendanceRate,
	}
	return writeJsonResponse(w, http.StatusOK, response)
}

func (srv *server) handleGetHistoricalEnrollmentBatch(w http.ResponseWriter, r *http.Request, log sLog) error {
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}

	datesParam := r.URL.Query().Get("dates")
	if datesParam == "" {
		return newBadRequestServiceError(nil, "dates parameter is required")
	}

	dates := strings.Split(datesParam, ",")
	if len(dates) == 0 {
		return newBadRequestServiceError(nil, "at least one date must be provided")
	}

	for _, date := range dates {
		if _, err := time.Parse("2006-01-02", strings.TrimSpace(date)); err != nil {
			return newBadRequestServiceError(err, "invalid date format: "+date)
		}
	}

	results, err := srv.store.GetHistoricalEnrollmentForDates(classID, dates)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	return writeJsonResponse(w, http.StatusOK, results)
}
