package programs

import (
	"context"
)

func (db *Store) GetTransferProgramConflicts(ctx context.Context, id uint, transferFacilityId int) ([]ResidentTransferProgramConflicts, error) {
	var programNames []ResidentTransferProgramConflicts
	query := `with transfer_facility_programs as (
			select name from programs p
			inner join facilities_programs fp on fp.program_id = p.id
					and fp.facility_id = ?
			where p.is_active = true
		)
		select cl.name as class_name, p.name as program_name from program_class_enrollments pce
		inner join users u on u.id = pce.user_id
			and u.id = ?
		inner join program_class_cohorts pc on pc.id = pce.cohort_id
			and pc.facility_id = u.facility_id
		inner join program_classes cl on cl.id = pc.class_id
		inner join programs p on p.id = pc.program_id
		where enrollment_status = 'Enrolled'
			and p.name not in (select name from transfer_facility_programs)`
	if err := db.WithContext(ctx).Raw(query, transferFacilityId, id).Scan(&programNames).Error; err != nil {
		return nil, err
	}
	return programNames, nil
}

// GetEnrolledResidentIDsForClass is the users enrolled in a class right now.
func (db *Store) GetEnrolledResidentIDsForClass(ctx context.Context, classID int) ([]uint, error) {
	var ids []uint
	if err := db.WithContext(ctx).Table("program_class_enrollments").
		Where("cohort_id = ? AND enrollment_status = 'Enrolled'", classID).
		Pluck("user_id", &ids).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_enrollments")
	}
	return ids, nil
}

// GetEligibleResidentIDsForClass is the active residents at a class's facility,
// which offers its program, who have no enrollment in the class.
func (db *Store) GetEligibleResidentIDsForClass(ctx context.Context, classID int) ([]uint, error) {
	var ids []uint
	if err := db.WithContext(ctx).Table("users").
		Joins("LEFT JOIN program_class_enrollments pse ON users.id = pse.user_id AND pse.cohort_id = ?", classID).
		Joins("JOIN facilities_programs fp ON users.facility_id = fp.facility_id").
		Joins("JOIN program_class_cohorts c ON c.program_id = fp.program_id AND c.id = ?", classID).
		Where("pse.user_id IS NULL"). //not enrolled in class
		Where("users.role = 'student'").
		Where("users.facility_id = c.facility_id").
		Where("users.deactivated_at IS NULL").
		Pluck("users.id", &ids).Error; err != nil {
		return nil, newGetRecordsDBError(err, "users")
	}
	return ids, nil
}

func (db *Store) GetChangeLogEntries(args *QueryContext, tableName string, refID int, categories []string) ([]ActivityHistoryResponse, error) {
	history := make([]ActivityHistoryResponse, 0, args.PerPage)

	programCategoryFields := map[string][]string{
		"info":     {"name", "description", "program"},
		"status":   {"status", "is_active", "archived_at"},
		"settings": {"credit_type", "credit_hours", "funding_type", "program_type"},
	}

	classCategoryFields := map[string][]string{
		"info":     {"name", "description", "instructor_name", "class"},
		"status":   {"status", "archived_at"},
		"schedule": {"start_date", "end_date", "start_dt", "end_dt", "meeting_days", "meeting_times", "event_rescheduled_series", "event_rescheduled", "event_cancelled", "event_restored", "event_substitute_instructor", "event_room_changed"},
		"settings": {"capacity", "location", "credit_hours"},
	}

	//added criteria field_name != 'facility_id to skip these records from being a part of the result set, this may be temporary?
	tx := db.WithContext(args.Ctx).
		Table("change_log_entries cle").
		Select(`cle.created_at, cle.user_id,
				users.username AS admin_username,
				'progclass_history' AS action,
				cle.*`).
		Joins("inner join users on cle.user_id = users.id").
		Where("cle.table_name = ? and cle.parent_ref_id = ? and field_name != 'facility_id'", tableName, refID)

	if len(categories) > 0 {
		var fields []string
		var categoryFields map[string][]string

		if tableName == "programs" {
			categoryFields = programCategoryFields
		} else {
			categoryFields = classCategoryFields
		}

		for _, category := range categories {
			if categoryFields[category] != nil {
				fields = append(fields, categoryFields[category]...)
			}
		}
		if len(fields) > 0 {
			tx = tx.Where("cle.field_name IN ?", fields)
		}
	}

	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "change_log_entries")
	}
	if err := tx.Order("cle.created_at desc").
		Offset(args.CalcOffset()).
		Limit(args.PerPage).
		Scan(&history).Error; err != nil {
		return nil, newGetRecordsDBError(err, "change_log_entries")
	}
	return history, nil
}

func (db *Store) GetUserProgramInfo(args *QueryContext, userId int) ([]ResidentProgramClassInfo, error) {
	userEnrollments := make([]ResidentProgramClassInfo, 0, args.PerPage)
	base := db.WithContext(args.Ctx).
		Table("program_class_enrollments AS pce").
		Select(`
            pce.id                   AS enrollment_id,
            pce.enrollment_status   AS enrollment_status,
            p.name                   AS program_name,
            p.id                     AS program_id,
            cl.name                  AS class_name,
            pc.status                AS status,
			pc.start_dt AS start_date,
			pc.end_dt AS end_date,
            pc.id                    AS cohort_id,
			pce.updated_at,
			pce.created_at,
			(SELECT ARRAY_TO_STRING(ARRAY_AGG(DISTINCT pct.credit_type), ', ')
			 FROM program_credit_types pct WHERE pct.program_id = p.id) AS credit_types,

            ROUND(COALESCE(SUM(
              CASE
                WHEN pcea.attendance_status = 'present' THEN 1
                WHEN pcea.attendance_status = 'partial' THEN LEAST(
					COALESCE(pcea.minutes_attended, pcea.scheduled_minutes, 0)::numeric /
					NULLIF(COALESCE(pcea.scheduled_minutes, pcea.minutes_attended, 0), 0),
					1
				)
                ELSE 0
              END
            ), 0))::int AS present_attendance,

            COALESCE(SUM(
              CASE WHEN pcea.attendance_status NOT IN ('present','partial') THEN 1 ELSE 0 END
            ), 0)  AS absent_attendance, pce.change_reason,
			STRING_AGG(DISTINCT e.recurrence_rule, '|') AS recurrence_rule
        `).
		Joins("INNER JOIN program_class_cohorts pc ON pc.id = pce.cohort_id").
		// A cohort has no name; every displayed class name comes from the class tier, so
		// this join is required for cl.name in the SELECT -- not optional enrichment.
		Joins("INNER JOIN program_classes cl ON cl.id = pc.class_id").
		Joins("INNER JOIN programs p ON p.id = pc.program_id").
		Joins("INNER JOIN program_class_events e ON e.cohort_id = pc.id").
		Joins(
			`LEFT JOIN program_class_event_attendance pcea
            ON pcea.event_id = e.id
           AND pcea.user_id = ?
           AND pcea.deleted_at IS NULL`,
			userId,
		).
		Where("pce.user_id = ?", userId).
		Group(`
            pce.id, pce.enrollment_status,
            p.name, p.id,
            cl.name, pc.status, pc.start_dt, pc.end_dt, pc.id, pce.updated_at,
			pce.created_at, pce.change_reason`).Order(args.OrderClause("pce.created_at desc"))

	if !args.All {
		if err := base.Count(&args.Total).Error; err != nil {
			return nil, newDBError(err, "program_class_enrollments")
		}
		if err := base.Limit(args.PerPage).Offset(args.CalcOffset()).Scan(&userEnrollments).Error; err != nil {
			return nil, newDBError(err, "program_class_enrollments apply pagination")
		}
	} else {
		if err := base.Find(&userEnrollments).Error; err != nil {
			return nil, newDBError(err, "program_class_enrollments")
		}

	}
	return userEnrollments, nil
}

func (db *Store) GetUserWeeklyAttendanceRows(ctx context.Context, userID, weeks int) ([]WeeklyAttendanceRow, error) {
	var rows []WeeklyAttendanceRow
	query := `
		SELECT DATE_TRUNC('week', date::date) AS week_start,
			COALESCE(SUM(CASE WHEN attendance_status IN ('present','partial') THEN 1 ELSE 0 END), 0) AS present_count,
			COUNT(*) AS total_count
		FROM program_class_event_attendance
		WHERE user_id = ? AND date::date >= NOW() - INTERVAL '1 week' * ? AND deleted_at IS NULL
		GROUP BY week_start
		ORDER BY week_start ASC`
	if err := db.WithContext(ctx).Raw(query, userID, weeks).Scan(&rows).Error; err != nil {
		return nil, newGetRecordsDBError(err, "weekly_attendance_trend")
	}
	return rows, nil
}

func (db *Store) GetResidentAttendanceCSVData(ctx context.Context, userID uint, facilityID uint, all bool, classID *uint) ([]ResidentAttendanceCSVData, error) {
	var csvData []ResidentAttendanceCSVData

	query := `
		SELECT
			p.name AS program_name,
			cl.name AS class_name,
			pcea.date AS session_date,
			pcea.attendance_status,
			COALESCE(pcea.note, '') AS note
		FROM program_class_event_attendance pcea
		JOIN program_class_events pce ON pce.id = pcea.event_id
		JOIN program_class_cohorts pc ON pc.id = pce.cohort_id
		JOIN program_classes cl ON cl.id = pc.class_id
		JOIN programs p ON p.id = pc.program_id
		JOIN program_class_enrollments e ON e.cohort_id = pc.id AND e.user_id = pcea.user_id
		JOIN users u ON u.id = pcea.user_id
		WHERE pcea.user_id = ?
			AND pcea.deleted_at IS NULL
			AND (e.enrolled_at IS NULL OR CAST(DATE(e.enrolled_at) AS TEXT) <= pcea.date)
			AND (e.enrollment_ended_at IS NULL OR CAST(DATE(e.enrollment_ended_at) AS TEXT) >= pcea.date)
	`

	args := []any{userID}

	if !all {
		query += " AND u.facility_id = ?"
		args = append(args, facilityID)
	}

	if classID != nil {
		query += " AND pc.id = ?"
		args = append(args, *classID)
	}

	query += " ORDER BY p.name ASC, cl.name ASC, pcea.date ASC"

	if err := db.WithContext(ctx).Raw(query, args...).Scan(&csvData).Error; err != nil {
		return nil, newGetRecordsDBError(err, "resident attendance CSV data")
	}

	return csvData, nil
}
