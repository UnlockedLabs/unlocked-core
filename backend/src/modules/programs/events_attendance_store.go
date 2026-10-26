package programs

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (db *Store) GetAttendees(queryParams *QueryContext, params url.Values, classId int) ([]ProgramClassEventAttendance, error) {
	date := params.Get("date")
	userId := params.Get("user_id")
	eventId := params.Get("event_id")
	tx := db.Model(&ProgramClassEventAttendance{})
	if date != "" {
		tx = tx.Where("date = ?", date)
	}
	if userId != "" {
		tx = tx.Where("user_id = ?", userId)
	}
	if eventId != "" {
		tx = tx.Where("event_id = ?", eventId)
	}
	attendance := make([]ProgramClassEventAttendance, 0, queryParams.PerPage)
	if err := tx.Count(&queryParams.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "class_event_attendance")
	}
	err := tx.Order(queryParams.OrderClause("program_class_event_attendance.created_at DESC")).Limit(queryParams.PerPage).Offset(queryParams.CalcOffset()).Find(&attendance).Error
	if err != nil {
		return nil, newGetRecordsDBError(err, "class_event_attendance")
	}
	return attendance, nil
}

func (db *Store) LogUserAttendance(attendanceParams []ProgramClassEventAttendance, ctx context.Context, adminID *uint, className string) error {
	var updateUserID uint
	if ctx := db.Statement.Context; ctx != nil {
		if c, ok := identity.FromContext(ctx); ok {
			updateUserID = c.UserID
		}
	}
	tx := db.Begin()
	if tx.Error != nil {
		return newDBError(tx.Error, "unable to start DB transaction")
	}

	for _, att := range attendanceParams {
		existingRow := updateUserID != 0 && att.ID != 0
		if existingRow {
			att.UpdateUserID = &updateUserID
		}

		// include deleted_at so re-marking a previously soft-deleted record revives it
		// (excluded.deleted_at is NULL on insert) instead of updating a hidden, soft-deleted row
		if err := tx.
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "event_id"}, {Name: "user_id"}, {Name: "date"}},
				DoUpdates: clause.AssignmentColumns([]string{"attendance_status", "note", "reason_category", "check_in_at", "check_out_at", "minutes_attended", "scheduled_minutes", "update_user_id", "deleted_at"}),
			}).
			Create(&att).Error; err != nil {
			tx.Rollback()
			return newCreateDBError(err, "upserting attendance record")
		}

		sessionDateParsed, err := time.ParseInLocation("2006-01-02", att.Date, time.Local)
		if err != nil {
			tx.Rollback()
			return newDBError(err, "invalid session date format")
		}

		history := NewUserAccountHistory(att.UserID, AttendanceRecorded, adminID, nil, nil)
		history.AttendanceStatus = att.AttendanceStatus
		history.ClassName = &className
		history.SessionDate = &sessionDateParsed

		if err := tx.Create(history).Error; err != nil {
			tx.Rollback()
			return newCreateDBError(err, "user_account_history")
		}
	}

	if err := tx.Commit().Error; err != nil {
		return newDBError(err, "unable to commit DB transaction")
	}
	return nil
}

func (db *Store) DeleteAttendance(eventId, userId int, date string, ctx context.Context, adminID *uint, className string) (int64, error) {
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	tx := db.Begin().WithContext(ctx)
	if tx.Error != nil {
		return 0, newDBError(tx.Error, "unable to start DB transaction")
	}

	result := tx.Where("event_id = ? AND user_id = ? AND date = ?", eventId, userId, date).Delete(&ProgramClassEventAttendance{})
	if result.Error != nil {
		tx.Rollback()
		return 0, newDeleteDBError(result.Error, "class_event_attendance")
	}

	if result.RowsAffected > 0 {
		sessionDateParsed, err := time.ParseInLocation("2006-01-02", date, time.Local)
		if err != nil {
			tx.Rollback()
			return 0, newDBError(err, "invalid session date format")
		}

		history := NewUserAccountHistory(uint(userId), AttendanceRecorded, adminID, nil, nil)
		history.AttendanceStatus = Attendance("deleted")
		history.ClassName = &className
		history.SessionDate = &sessionDateParsed

		if err := tx.Create(history).Error; err != nil {
			tx.Rollback()
			return 0, newCreateDBError(err, "user_account_history")
		}
	}

	if err := tx.Commit().Error; err != nil {
		return 0, newDBError(err, "unable to commit DB transaction")
	}

	return result.RowsAffected, nil
}

func (db *Store) GetEnrolledUserIDsForClass(classID uint, userIDs []uint) ([]uint, error) {
	var enrolledIDs []uint
	tx := db.Model(&ProgramClassEnrollment{}).
		Where("cohort_id = ? AND enrollment_status = ?", classID, Enrolled)
	if len(userIDs) > 0 {
		tx = tx.Where("user_id IN ?", userIDs)
	}
	if err := tx.Pluck("user_id", &enrolledIDs).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_enrollments")
	}
	return enrolledIDs, nil
}

func (db *Store) GetEnrollmentsWithAttendanceForEvent(qryCtx *QueryContext, classID, eventID int, date string, includeCurrentlyEnrolled bool) ([]EnrollmentAttendance, error) {
	baseQuery := `
        FROM program_class_enrollments AS e
        JOIN users AS u ON u.id = e.user_id
        LEFT JOIN program_class_event_attendance AS a
            ON a.user_id = e.user_id AND a.event_id = ? AND a.date = ? AND a.deleted_at IS NULL
        WHERE e.cohort_id = ?
        `

	if includeCurrentlyEnrolled {
		baseQuery += `
           AND (
                (DATE(e.enrolled_at) <= ?::date AND (e.enrollment_ended_at IS NULL OR DATE(e.enrollment_ended_at) >= ?::date))
                OR e.enrollment_status = 'Enrolled'
           )
        `
	} else {
		baseQuery += `
           AND DATE(e.enrolled_at) <= ?::date
           AND (e.enrollment_ended_at IS NULL OR DATE(e.enrollment_ended_at) >= ?::date)
        `
	}

	args := []any{eventID, date, classID, date, date}

	if qryCtx.Search != "" {
		query := qryCtx.SearchQuery()
		baseQuery += ` AND (LOWER(u.name_first) LIKE ? OR LOWER(u.name_last) LIKE ? OR LOWER(u.doc_id) LIKE ?)`
		args = append(args, query, query, query)
	}

	countQuery := "SELECT COUNT(*) " + baseQuery
	if err := db.WithContext(qryCtx.Ctx).Raw(countQuery, args...).Scan(&qryCtx.Total).Error; err != nil {
		return nil, err
	}

	selectClause := `
		SELECT
			e.id AS enrollment_id,
			e.cohort_id AS cohort_id,
			e.enrollment_status AS enrollment_status,
			u.id AS user_id,
			u.username AS username,
			u.name_first AS name_first,
			u.name_last AS name_last,
            u.doc_id AS doc_id,
			a.id AS attendance_id,
			a.event_id AS event_id,
			a.date AS date,
			a.attendance_status AS attendance_status,
			a.note AS note,
			a.reason_category AS reason_category,
			a.check_in_at AS check_in_at,
			a.check_out_at AS check_out_at,
			a.minutes_attended AS minutes_attended,
			a.scheduled_minutes AS scheduled_minutes`
	finalQuery := selectClause + baseQuery
	if slices.Contains([]string{"name_first", "name_last"}, qryCtx.OrderBy) {
		finalQuery += " ORDER BY " + adjustUserOrderBy(qryCtx.OrderClause("e.created_at DESC"))
	} else {
		finalQuery += " ORDER BY e.id ASC"
	}

	finalQuery += " LIMIT ? OFFSET ?"
	args = append(args, qryCtx.PerPage, qryCtx.CalcOffset())
	var results []EnrollmentAttendance

	newSession := db.Session(&gorm.Session{NewDB: true})
	if err := newSession.WithContext(qryCtx.Ctx).Raw(finalQuery, args...).Scan(&results).Error; err != nil {
		return nil, err
	}
	return results, nil
}

func (db *Store) GetAttendanceRateForEvent(ctx context.Context, eventID int, classID int, eventDate string) (float64, error) {
	var attendanceRate float64
	sql := `
	select coalesce(
		sum(
			case
				when pcea.attendance_status = 'present' then 1
				when pcea.attendance_status = 'partial' then least(
					coalesce(pcea.minutes_attended, pcea.scheduled_minutes, 0)::numeric /
					nullif(coalesce(pcea.scheduled_minutes, pcea.minutes_attended, 0), 0),
					1
				)
				else 0
			end
		) * 100.0 /
		nullif(
			(select count(*) from program_class_enrollments e
			 where e.cohort_id = ?
			   and e.enrolled_at <= ?
			   and (e.enrollment_ended_at IS NULL OR e.enrollment_ended_at >= ?)),
			0
		), 0
	) as attendance_percentage
	from program_class_event_attendance pcea
	inner join program_class_events pce ON pce.id = pcea.event_id
	where pcea.event_id = ? AND pcea.date = ? AND pcea.deleted_at IS NULL`

	if err := db.WithContext(ctx).Raw(sql, classID, eventDate, eventDate, eventID, eventDate).Scan(&attendanceRate).Error; err != nil {
		return 0, newNotFoundDBError(err, "program_class_event_attendance")
	}
	return attendanceRate, nil
}

func (db *Store) GetAttendanceFlagsForClass(classID int, args *QueryContext) ([]AttendanceFlag, error) {
	flags := make([]AttendanceFlag, 0, args.PerPage)
	attendanceCoreSQL := `from program_class_enrollments e
		inner join program_class_cohorts c on c.id = e.cohort_id
		inner join users u on u.id = e.user_id
		where c.id = ?
		and e.enrollment_status = 'Enrolled'
		and c.status = 'Active'
		and u.deleted_at is null
		and exists (select 1 from program_class_events evt
				inner join program_class_event_attendance att on att.event_id = evt.id and att.deleted_at is null
				where evt.cohort_id = c.id
						and att.attendance_status is not null
		)`
	noAttendanceSQL := `and exists (select 1 from program_class_events evt
			inner join program_class_event_attendance att on att.event_id = evt.id and att.deleted_at is null
			where evt.cohort_id = c.id
					and att.user_id = e.user_id
		)
		and not exists (select 1 from program_class_events evt
			inner join program_class_event_attendance att on att.event_id = evt.id and att.deleted_at is null
			where evt.cohort_id = c.id
					and att.user_id = e.user_id
					and att.attendance_status in ('present','partial')
		)`
	multiAbsencesSQL := `and exists (select 1 from program_class_events evt
			inner join program_class_event_attendance att on att.event_id = evt.id and att.deleted_at is null
			where evt.cohort_id = c.id
					and att.user_id = e.user_id
					and att.attendance_status in ('present','partial')
		)
		and (select count(*) from program_class_events evt
				inner join program_class_event_attendance att on att.event_id = evt.id and att.deleted_at is null
				where evt.cohort_id = c.id
						and att.user_id = e.user_id
						and att.attendance_status = 'absent_unexcused'
		) >= 3`

	statsSQL := `,
		e.user_id,
		(select count(*) from program_class_events evt
			inner join program_class_event_attendance att on att.event_id = evt.id and att.deleted_at is null
			where evt.cohort_id = c.id and att.user_id = e.user_id
		) as total_sessions,
		(select count(*) from program_class_events evt
			inner join program_class_event_attendance att on att.event_id = evt.id and att.deleted_at is null
			where evt.cohort_id = c.id and att.user_id = e.user_id
				and att.attendance_status in ('present','partial')
		) as attended_sessions`

	countQuery := fmt.Sprintf(`select count(*) from (
			select u.id %s %s
			union all
			select u.id %s %s
		) as attendance_flags`, attendanceCoreSQL, noAttendanceSQL, attendanceCoreSQL, multiAbsencesSQL)

	if err := db.WithContext(args.Ctx).Raw(countQuery, classID, classID).Scan(&args.Total).Error; err != nil {
		return nil, newNotFoundDBError(err, "program_class_event_attendance")
	}

	attendanceQuery := fmt.Sprintf(`select name_first, name_last, doc_id, flag_type, user_id, total_sessions, attended_sessions from (
			select u.name_first, u.name_last,
			u.doc_id, 'no_attendance' as flag_type %s %s %s
			union all
			select u.name_first, u.name_last,
			u.doc_id, 'multiple_absences' as flag_type %s %s %s
		) AS attendance_flags
		ORDER BY name_last, name_first
		LIMIT ? OFFSET ?`, statsSQL, attendanceCoreSQL, noAttendanceSQL, statsSQL, attendanceCoreSQL, multiAbsencesSQL)
	if err := db.WithContext(args.Ctx).Raw(attendanceQuery, classID, classID, args.PerPage, args.CalcOffset()).Scan(&flags).Error; err != nil {
		return nil, newNotFoundDBError(err, "program_class_event_attendance")
	}

	for i := range flags {
		flags[i].MissedSessions = flags[i].TotalSessions - flags[i].AttendedSessions
		if flags[i].TotalSessions > 0 {
			flags[i].AttendanceRate = int(float64(flags[i].AttendedSessions) / float64(flags[i].TotalSessions) * 100)
		}
	}

	if len(flags) > 0 {
		if err := db.computeConsecutiveAbsences(classID, flags); err != nil {
			return nil, err
		}
	}

	return flags, nil
}

func (db *Store) computeConsecutiveAbsences(classID int, flags []AttendanceFlag) error {
	userIDs := make([]uint, len(flags))
	for i, f := range flags {
		userIDs[i] = f.UserID
	}

	type record struct {
		UserID           uint   `gorm:"column:user_id"`
		Date             string `gorm:"column:date"`
		AttendanceStatus string `gorm:"column:attendance_status"`
	}
	var records []record
	if err := db.Raw(`
		SELECT att.user_id, att.date, att.attendance_status
		FROM program_class_event_attendance att
		INNER JOIN program_class_events evt ON evt.id = att.event_id
		WHERE evt.cohort_id = ? AND att.user_id IN ? AND att.deleted_at IS NULL
		ORDER BY att.user_id, att.date DESC`,
		classID, userIDs,
	).Scan(&records).Error; err != nil {
		return newNotFoundDBError(err, "consecutive absences")
	}

	byUser := make(map[uint]int)
	var currentUser uint
	var counting bool
	for _, r := range records {
		if r.UserID != currentUser {
			currentUser = r.UserID
			counting = true
			byUser[currentUser] = 0
		}
		if !counting {
			continue
		}
		switch r.AttendanceStatus {
		case "present", "partial":
			counting = false
		case "absent_excused", "absent_unexcused":
			byUser[currentUser]++
		}
	}

	for i := range flags {
		flags[i].ConsecutiveAbsences = byUser[flags[i].UserID]
	}
	return nil
}

func (db *Store) GetMissingAttendance(classID int, args *QueryContext) (int, error) {
	var events []ProgramClassEvent
	err := db.WithContext(args.Ctx).
		Preload("Class").
		Where("cohort_id = ?", classID).
		Find(&events).Error
	if err != nil {
		return 0, newNotFoundDBError(err, "program_class_events")
	}
	allEventDates := make([]EventInstance, 0, 20)
	if len(events) == 1 {
		allEventDates = generateEventInstances(events[0], events[0].Cohort.StartDt, time.Now().AddDate(0, 0, 1)) // adds one day to today so today is included
	} else {
		for _, event := range events {
			startDate := event.Cohort.StartDt
			endDate := time.Now().AddDate(0, 0, 1)                                  // adds one day to today so today is included
			if event.Cohort.EndDt != nil && event.Cohort.EndDt.Before(time.Now()) { //if it has an end date that is before today, then use that
				endDate = *event.Cohort.EndDt
			}
			eventInstances := generateEventInstances(event, startDate, endDate)
			allEventDates = append(allEventDates, eventInstances...)
		}
	}
	totalEvents := len(allEventDates)

	var students []ProgramClassEnrollment
	err = db.WithContext(args.Ctx).
		Where("cohort_id = ?", classID).
		Find(&students).Error
	if err != nil {
		return 0, newGetRecordsDBError(err, "program_class_enrollments")
	}

	type AttendanceDateCount struct {
		EventID int    `json:"event_id"`
		Date    string `json:"date"`
		Count   int64  `json:"count"`
	}

	var attendanceCounts []AttendanceDateCount
	eventIDs := make([]uint, len(allEventDates))
	dates := make([]string, len(allEventDates))

	for i, eventDate := range allEventDates {
		eventIDs[i] = eventDate.EventID
		dates[i] = eventDate.StartTime.Format("2006-01-02")
	}

	err = db.WithContext(args.Ctx).
		Model(&ProgramClassEventAttendance{}).
		Select("event_id, date, COUNT(*) as count").
		Where("event_id IN ? AND date IN ?", eventIDs, dates).
		Group("event_id, date").
		Scan(&attendanceCounts).Error
	if err != nil {
		return 0, newGetRecordsDBError(err, "program_class_event_attendance")
	}

	attendanceMap := make(map[string]int64)
	for _, attendance := range attendanceCounts {
		key := fmt.Sprintf("%d_%s", attendance.EventID, attendance.Date)
		attendanceMap[key] = attendance.Count
	}

	completedAttendanceDates := 0
	for _, eventDate := range allEventDates {
		// count enrolled students on this specific date
		enrolledCountOnDate := 0
		for _, student := range students {
			if student.EnrolledAt == nil {
				continue
			}
			if !student.EnrolledAt.After(eventDate.StartTime) && // enrolled before or on this date
				(student.EnrollmentEndedAt == nil || student.EnrollmentEndedAt.After(eventDate.StartTime.AddDate(0, 0, -1))) { // not ended before this date
				enrolledCountOnDate++
			}
		}

		key := fmt.Sprintf("%d_%s", eventDate.EventID, eventDate.StartTime.Format("2006-01-02"))
		attendanceCount, exists := attendanceMap[key]
		if !exists {
			attendanceCount = 0
		}

		if int(attendanceCount) == enrolledCountOnDate {
			completedAttendanceDates++
		}
	}
	missingAttendanceCount := totalEvents - completedAttendanceDates
	return missingAttendanceCount, nil
}

func (db *Store) GetActiveClassesForMissingAttendance(args *QueryContext, facilityID *uint) ([]MissingAttendanceClass, error) {
	var missClasses []MissingAttendanceClass
	classQuery := db.WithContext(args.Ctx).
		Table("program_class_cohorts c").
		Select("c.id, cl.name as name, f.name as facility_name, c.facility_id").
		Joins("JOIN program_classes cl ON cl.id = c.class_id").
		Joins("JOIN facilities f ON f.id = c.facility_id").
		Where("c.status = ?", Active).
		Where("c.archived_at IS NULL")
	if facilityID != nil {
		classQuery = classQuery.Where("c.facility_id = ?", *facilityID)
	}
	if err := classQuery.Find(&missClasses).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program class cohort")
	}
	return missClasses, nil
}

func (db *Store) GetActiveClassesWithEvents(args *QueryContext, facilityID *uint) ([]ProgramClassCohort, error) {
	var classes []ProgramClassCohort
	// The join supplies class_name, which is the only name a cohort has. Both tables
	// carry status/archived_at/facility_id, so EVERY predicate below is table-qualified:
	// unqualified, Postgres errors "ambiguous" while SQLite silently picks one.
	classQuery := db.WithContext(args.Ctx).
		Model(&ProgramClassCohort{}).
		Select("program_class_cohorts.*, pc.name AS class_name").
		Joins("JOIN program_classes pc ON pc.id = program_class_cohorts.class_id").
		Preload("Events.Overrides").
		Preload("Events.RoomRef").
		Preload("Events.Instructor").
		Preload("Facility").
		Where("program_class_cohorts.status = ?", Active).
		Where("program_class_cohorts.archived_at IS NULL")
	if facilityID != nil {
		classQuery = classQuery.Where("program_class_cohorts.facility_id = ?", *facilityID)
	}
	if err := classQuery.Find(&classes).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program class cohort")
	}
	return classes, nil
}

func (db *Store) GetClassEventsWithOverrides(args *QueryContext, classIDs []uint) ([]ProgramClassEvent, error) {
	var events []ProgramClassEvent
	if err := db.WithContext(args.Ctx).
		Model(&ProgramClassEvent{}).
		Preload("Overrides").
		Preload("RoomRef").
		Where("cohort_id IN ?", classIDs).
		Find(&events).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_events")
	}
	return events, nil
}

type AttendanceCount struct {
	EventID uint   `json:"event_id"`
	Date    string `json:"date"`
	Count   int64  `json:"count"`
}

func (db *Store) GetAttendanceCountsForEvents(args *QueryContext, eventIDs []uint, dates []string) ([]AttendanceCount, error) {
	var attendanceCounts []AttendanceCount
	if err := db.WithContext(args.Ctx).
		Model(&ProgramClassEventAttendance{}).
		Select("event_id, date, COUNT(*) as count").
		Where("event_id IN ? AND date IN ?", eventIDs, dates).
		Group("event_id, date").
		Scan(&attendanceCounts).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_event_attendance")
	}
	return attendanceCounts, nil
}

type ClassEnrollmentCount struct {
	CohortID uint  `json:"cohort_id"`
	Count    int64 `json:"count"`
}

// GetActiveEnrollmentCountsForClasses returns the number of currently-enrolled
// residents per class, keyed by class_id. Classes with no active enrollments
// are absent from the map (callers treat a missing key as zero).
func (db *Store) GetActiveEnrollmentCountsForClasses(args *QueryContext, classIDs []uint) (map[uint]int, error) {
	var rows []ClassEnrollmentCount
	if err := db.WithContext(args.Ctx).
		Model(&ProgramClassEnrollment{}).
		Select("cohort_id, COUNT(*) as count").
		Where("cohort_id IN ? AND enrollment_status = ?", classIDs, Enrolled).
		Group("cohort_id").
		Scan(&rows).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_enrollments")
	}
	counts := make(map[uint]int, len(rows))
	for _, row := range rows {
		counts[row.CohortID] = int(row.Count)
	}
	return counts, nil
}

func (db *Store) GetActiveEnrollmentsForClasses(args *QueryContext, classIDs []uint) ([]ProgramClassEnrollment, error) {
	var enrollments []ProgramClassEnrollment
	if err := db.WithContext(args.Ctx).
		Model(&ProgramClassEnrollment{}).
		Select("cohort_id, enrolled_at, enrollment_ended_at").
		Where("cohort_id IN ?", classIDs).
		Where("enrollment_status = ?", Enrolled).
		Find(&enrollments).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_enrollments")
	}
	return enrollments, nil
}

func (db *Store) CreateAttendanceAuditTrail(ctx context.Context, att *ProgramClassEventAttendance, adminID *uint, className string) error {

	sessionDateParsed, err := time.ParseInLocation("2006-01-02", att.Date, time.Local)
	if err != nil {
		return newDBError(err, "invalid session date format")
	}

	history := NewUserAccountHistory(att.UserID, AttendanceRecorded, adminID, nil, nil)
	history.AttendanceStatus = att.AttendanceStatus
	history.ClassName = &className
	history.SessionDate = &sessionDateParsed

	if err := db.WithContext(ctx).Create(history).Error; err != nil {
		return newCreateDBError(err, "user_account_history")
	}

	return nil
}

func (db *Store) GetCumulativeAttendanceRateForClass(ctx context.Context, classID int) (*float64, error) {
	var attendanceRate *float64
	today := time.Now().Format("2006-01-02")
	sql := `
	WITH attendance_credits AS (
		SELECT
			pcea.id,
			CASE
				WHEN pcea.attendance_status = 'present' THEN 1.0
				WHEN pcea.attendance_status = 'partial' THEN LEAST(
					COALESCE(pcea.minutes_attended, pcea.scheduled_minutes, 0)::numeric /
					NULLIF(COALESCE(pcea.scheduled_minutes, pcea.minutes_attended, 0), 0),
					1
				)
				ELSE 0
			END as credit
		FROM program_class_event_attendance pcea
		INNER JOIN program_class_events pce ON pce.id = pcea.event_id
		WHERE pce.cohort_id = ? AND pcea.date <= ? AND pcea.deleted_at IS NULL
	)
	SELECT
		(SELECT SUM(credit) FROM attendance_credits) * 100.0 /
		NULLIF((SELECT COUNT(*) FROM attendance_credits), 0)
	as attendance_percentage`

	if err := db.WithContext(ctx).Raw(sql, classID, today).Scan(&attendanceRate).Error; err != nil {
		return nil, newNotFoundDBError(err, "program_class_event_attendance")
	}
	return attendanceRate, nil
}

func (db *Store) GetCumulativeAttendanceRatesForClasses(ctx context.Context, classIDs []uint) (map[uint]float64, error) {
	if len(classIDs) == 0 {
		return map[uint]float64{}, nil
	}
	today := time.Now().Format("2006-01-02")
	sql := `
	WITH attendance_credits AS (
		SELECT
			pce.cohort_id,
			CASE
				WHEN pcea.attendance_status = 'present' THEN 1.0
				WHEN pcea.attendance_status = 'partial' THEN LEAST(
					COALESCE(pcea.minutes_attended, pcea.scheduled_minutes, 0)::numeric /
					NULLIF(COALESCE(pcea.scheduled_minutes, pcea.minutes_attended, 0), 0),
					1
				)
				ELSE 0
			END as credit
		FROM program_class_event_attendance pcea
		INNER JOIN program_class_events pce ON pce.id = pcea.event_id
		WHERE pce.cohort_id IN ? AND pcea.date <= ? AND pcea.deleted_at IS NULL
	)
	SELECT cohort_id,
		COALESCE(
			SUM(credit) * 100.0 / NULLIF(COUNT(*), 0),
			0
		) as attendance_percentage
	FROM attendance_credits
	GROUP BY cohort_id`

	type row struct {
		ClassID        uint    `gorm:"column:cohort_id"`
		AttendanceRate float64 `gorm:"column:attendance_percentage"`
	}
	rows := []row{}
	if err := db.WithContext(ctx).Raw(sql, classIDs, today).Scan(&rows).Error; err != nil {
		return nil, newNotFoundDBError(err, "program_class_event_attendance")
	}

	out := make(map[uint]float64, len(classIDs))
	for _, r := range rows {
		out[r.ClassID] = r.AttendanceRate
	}
	return out, nil
}
