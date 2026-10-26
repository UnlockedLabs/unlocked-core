package programs

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/users"
	log "github.com/sirupsen/logrus"
	"github.com/teambition/rrule-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BulkCancelClaims interface {
	GetUserID() uint
	GetFacilityID() uint
	GetTimezone() string
}

func parseDateRange(startDate, endDate string) (time.Time, time.Time, error) {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid start date format: %w", err)
	}
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)

	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid end date format: %w", err)
	}
	end = time.Date(end.Year(), end.Month(), end.Day(), 23, 59, 59, 999999999, time.UTC)

	return start, end, nil
}

func (db *Store) GetCohortByID(id int) (*ProgramClassCohort, error) {
	content := &ProgramClassCohort{}
	// See GetClasses: class_name is the parent CLASS's name, joined in explicitly.
	if err := db.Joins("JOIN program_classes pc ON pc.id = program_class_cohorts.class_id").
		Select("program_class_cohorts.*, pc.name AS class_name").
		Preload("Events").Preload("Events.Overrides").Preload("Events.Overrides.Instructor").Preload("Events.Overrides.RoomRef").Preload("Events.RoomRef").
		Preload("Events.Instructor").Preload("Enrollments", func(tx *gorm.DB) *gorm.DB {
		return tx.Joins("JOIN users ON users.id = program_class_enrollments.user_id AND users.deleted_at IS NULL")
	}).
		Preload("Program").Preload("Facility").First(content, "program_class_cohorts.id = ?", id).Error; err != nil {
		return nil, newNotFoundDBError(err, "program classes")
	}
	var enrollments, completed int

	for _, enrolled := range content.Enrollments {
		switch enrolled.EnrollmentStatus {
		case Enrolled:
			enrollments += 1
		case EnrollmentCompleted:
			completed += 1
		}
	}
	content.Enrolled = int64(enrollments)
	content.Completed = int64(completed)
	return content, nil
}

func (db *Store) GetCohortsForFacility(args *QueryContext) ([]ProgramClassCohort, error) {
	return db.GetClasses(args, &args.FacilityID)
}

func (db *Store) GetClasses(args *QueryContext, facilityID *uint) ([]ProgramClassCohort, error) {
	content := []ProgramClassCohort{}
	// class_name is the parent CLASS's name -- the one the UI shows.
	//
	// ⚠️  EVERY predicate below must be table-qualified. program_classes carries
	//     archived_at, facility_id AND name too, so an unqualified `archived_at IS NULL`
	//     is ambiguous once this join exists -- Postgres errors, SQLite silently picks a
	//     table, so the Go suite passes and dev 500s.
	//
	// The join is many-to-one (class_id is NOT NULL behind a composite FK), so it cannot
	// multiply rows and is safe to apply before the Count.
	tx := db.WithContext(args.Ctx).Model(&ProgramClassCohort{}).
		Joins("JOIN program_classes pc ON pc.id = program_class_cohorts.class_id").
		Where("program_class_cohorts.archived_at IS NULL")
	if facilityID != nil {
		tx = tx.Where("program_class_cohorts.facility_id = ?", *facilityID)
	}
	if args.Search != "" {
		// The class's name only -- the cohort's own name is never shown, and the column
		// is slated for removal (task #12).
		tx = tx.Where("LOWER(pc.name) LIKE ?", args.SearchQuery())
	}
	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program classes")
	}

	tx = tx.Select("program_class_cohorts.*, pc.name AS class_name").
		// Events.Instructor populates the event's `instructor_ref`, which is the ONLY
		// source for the Instructor column on /classes -- the frontend derives it with
		// getInstructorName(cls.events). Without this preload the column renders blank
		// and instructor search silently matches nothing. GetCohortByID already has it,
		// which is why class detail showed an instructor while the list did not.
		Preload("Events").Preload("Events.RoomRef").Preload("Events.Instructor").
		Preload("Enrollments", func(db *gorm.DB) *gorm.DB {
			return db.Joins("JOIN users ON users.id = program_class_enrollments.user_id AND users.deleted_at IS NULL")
		}).
		Preload("Program").Preload("Facility")
	if !args.All {
		tx = tx.Limit(args.PerPage).Offset(args.CalcOffset())
	}
	if err := tx.Find(&content).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program classes")
	}
	for i := range content {
		var enrollments, completed int
		for _, e := range content[i].Enrollments {
			switch e.EnrollmentStatus {
			case Enrolled:
				enrollments++
			case EnrollmentCompleted:
				completed++
			}
		}
		content[i].Enrolled = int64(enrollments)
		content[i].Completed = int64(completed)
	}
	return content, nil
}

func (db *Store) CreateProgramClass(content *ProgramClassCohort, conflictReq *ConflictCheckRequest) (*ProgramClassCohort, []RoomConflict, error) {
	if err := Validate().Struct(content); err != nil {
		return nil, nil, newCreateDBError(err, "create program classes validation error")
	}

	if conflictReq == nil {
		if err := db.Create(&content).Error; err != nil {
			return nil, nil, newCreateDBError(err, "program classes")
		}
		return content, nil, nil
	}

	tx := db.Begin()
	if tx.Error != nil {
		return nil, nil, newDBError(tx.Error, "unable to start transaction")
	}

	conflicts, err := LockAndCheckConflicts(tx, conflictReq)
	if err != nil {
		tx.Rollback()
		return nil, nil, err
	}
	if len(conflicts) > 0 {
		tx.Rollback()
		return nil, conflicts, nil
	}

	if err := tx.Create(&content).Error; err != nil {
		tx.Rollback()
		return nil, nil, newCreateDBError(err, "program classes")
	}

	if err := tx.Commit().Error; err != nil {
		return nil, nil, newDBError(err, "unable to commit transaction")
	}
	return content, nil, nil
}

func (db *Store) UpdateProgramClass(content *ProgramClassCohort, id int, conflictReq *ConflictCheckRequest) (*ProgramClassCohort, []RoomConflict, error) {
	var allChanges []ChangeLogEntry
	existing := &ProgramClassCohort{}
	if err := db.Preload("Events").First(existing, "id = ?", id).Error; err != nil {
		return nil, nil, newNotFoundDBError(err, "program classes")
	}

	trans := db.Begin()
	if trans.Error != nil {
		return nil, nil, newDBError(trans.Error, "unable to start the database transaction")
	}

	if conflictReq != nil {
		conflicts, err := LockAndCheckConflicts(trans, conflictReq)
		if err != nil {
			trans.Rollback()
			return nil, nil, err
		}
		if len(conflicts) > 0 {
			trans.Rollback()
			return nil, conflicts, nil
		}
	}

	ignoredFieldNames := []string{"create_user_id", "update_user_id", "enrollments", "facility", "facilities", "events", "facility_program", "program_id", "facility_id", "start_dt", "end_dt", "program", "enrolled", "completed", "archived_at", "instructor", "instructor_id"}
	classLogEntries := GenerateChangeLogEntries(existing, content, TableNameCohort, existing.ID, DerefUint(content.UpdateUserID), ignoredFieldNames)
	allChanges = append(allChanges, classLogEntries...)

	existingID := existing.ID

	var needsRoomUpdate bool
	var newRoomID *uint
	var eventID uint
	var needsScheduleUpdate bool
	var newRecurrenceRule string
	var newDuration string
	if len(content.Events) > 0 && len(existing.Events) > 0 {
		eventID = existing.Events[0].ID
		if content.Events[0].RoomID != nil {
			existingRoomID := existing.Events[0].RoomID
			if existingRoomID == nil || *content.Events[0].RoomID != *existingRoomID {
				needsRoomUpdate = true
				newRoomID = content.Events[0].RoomID
			}
		}
		if content.Events[0].RecurrenceRule != "" && content.Events[0].RecurrenceRule != existing.Events[0].RecurrenceRule {
			needsScheduleUpdate = true
			newRecurrenceRule = content.Events[0].RecurrenceRule
		}
		if content.Events[0].Duration != "" && content.Events[0].Duration != existing.Events[0].Duration {
			needsScheduleUpdate = true
			newDuration = content.Events[0].Duration
		}
	}

	originalStatus := existing.Status

	updateStruct(existing, content)
	existing.ID = existingID

	if content.UpdateInstructor {
		if err := trans.Model(&ProgramClassEvent{}).
			Where("cohort_id = ?", existing.ID).
			Update("instructor_id", content.InstructorID).Error; err != nil {
			trans.Rollback()
			return nil, nil, newUpdateDBError(err, "program class event instructor")
		}
	}

	if err := trans.Session(&gorm.Session{FullSaveAssociations: false}).Model(&ProgramClassCohort{}).Where("id = ?", existing.ID).Updates(existing).Error; err != nil {
		trans.Rollback()
		return nil, nil, newUpdateDBError(err, "program classes")
	}

	if needsRoomUpdate {
		if err := trans.Model(&ProgramClassEvent{}).Where("id = ?", eventID).Update("room_id", newRoomID).Error; err != nil {
			trans.Rollback()
			return nil, nil, newUpdateDBError(err, "program class event room")
		}
		existing.Events[0].RoomID = newRoomID
		var roomName string
		if newRoomID != nil {
			var room Room
			if err := trans.Select("name").First(&room, *newRoomID).Error; err == nil {
				roomName = room.Name
			}
		}
		allChanges = append(allChanges, *NewChangeLogEntry(TableNameCohort, "event_room_changed", nil, &roomName, existing.ID, DerefUint(content.UpdateUserID)))
	}

	if needsScheduleUpdate {
		if newRecurrenceRule != "" {
			if err := trans.Model(&ProgramClassEvent{}).Where("id = ?", eventID).Update("recurrence_rule", newRecurrenceRule).Error; err != nil {
				trans.Rollback()
				return nil, nil, newUpdateDBError(err, "program class event recurrence rule")
			}
		}
		if newDuration != "" {
			if err := trans.Model(&ProgramClassEvent{}).Where("id = ?", eventID).Update("duration", newDuration).Error; err != nil {
				trans.Rollback()
				return nil, nil, newUpdateDBError(err, "program class event duration")
			}
		}
		oldRule := existing.Events[0].RecurrenceRule
		allChanges = append(allChanges, *NewChangeLogEntry(TableNameCohort, "event_rescheduled_series", &oldRule, &newRecurrenceRule, existing.ID, DerefUint(content.UpdateUserID)))
	}

	newStatus := existing.Status
	if newStatus == Active && originalStatus != Active {
		if err := trans.Model(&ProgramClassEnrollment{}).
			Where("cohort_id = ? AND enrollment_status = ? AND enrolled_at IS NULL", id, Enrolled).
			Updates(map[string]any{
				"enrolled_at":    time.Now().UTC(),
				"update_user_id": content.UpdateUserID,
			}).Error; err != nil {
			trans.Rollback()
			return nil, nil, newUpdateDBError(err, "backfilling enrolled_at on activation")
		}
	}
	if newStatus != originalStatus && (newStatus == Completed || newStatus == Cancelled) {
		completionTime := time.Now().UTC()
		if err := db.UpdateClassEventRRuleUntilDate(trans, []int{id}, completionTime); err != nil {
			trans.Rollback()
			return nil, nil, newUpdateDBError(err, "updating class event rrule until date")
		}

		var enrollmentStatus ProgramEnrollmentStatus
		if newStatus == Cancelled {
			enrollmentStatus = EnrollmentCancelled
		} else {
			enrollmentStatus = EnrollmentCompleted
		}

		if err := trans.
			Model(&ProgramClassEnrollment{}).
			Where("cohort_id = ? AND enrollment_status = ?", id, Enrolled).
			Set("cohort_id", id).
			Update("enrollment_status", enrollmentStatus).
			Error; err != nil {
			trans.Rollback()
			return nil, nil, newUpdateDBError(err, "class enrollment statuses")
		}

		if newStatus == Completed {
			var completedEnrollments []ProgramClassEnrollment
			if err := completionPreloads(trans).
				Where("cohort_id = ? AND enrollment_status = ?", id, EnrollmentCompleted).
				Find(&completedEnrollments).Error; err != nil {
				trans.Rollback()
				return nil, nil, newNotFoundDBError(err, "fetching completed enrollments")
			}

			if len(completedEnrollments) > 0 {
				var admin User
				if err := trans.First(&admin, "id = ?", DerefUint(content.UpdateUserID)).Error; err != nil {
					trans.Rollback()
					return nil, nil, newNotFoundDBError(err, "admin user")
				}

				completions := make([]ClassCompletion, 0, len(completedEnrollments))
				for _, enrollment := range completedEnrollments {
					completions = append(completions, newClassCompletion(enrollment, admin.Email))
				}

				// see the note in GraduateEnrollments -- one certificate per (user, class)
				if err := trans.Clauses(clause.OnConflict{DoNothing: true}).Create(&completions).Error; err != nil {
					trans.Rollback()
					return nil, nil, newCreateDBError(err, "enrollment completions")
				}
			}
		}
	}

	if len(allChanges) > 0 {
		if err := trans.Create(&allChanges).Error; err != nil {
			trans.Rollback()
			return nil, nil, newCreateDBError(err, "change_log_entries")
		}
	}

	if err := trans.Commit().Error; err != nil {
		return nil, nil, newDBError(err, "unable to commit the database transaction")
	}

	return existing, nil, nil
}

func (db *Store) GetTotalEnrollmentsByClassID(id int) (int64, error) {
	var count int64
	if err := db.Model(&ProgramClassEnrollment{}).Where("cohort_id = ? and enrollment_status = 'Enrolled'", id).Count(&count).Error; err != nil {
		return 0, newDBError(err, "program_class_enrollments")
	}
	return count, nil
}

func (db *Store) GetHistoricalEnrollmentForDates(classID int, dates []string) (map[string]int64, error) {
	results := make(map[string]int64)

	for _, date := range dates {
		trimmedDate := strings.TrimSpace(date)
		var count int64
		if err := db.Model(&ProgramClassEnrollment{}).
			Where("cohort_id = ?", classID).
			Where("DATE(enrolled_at) <= ?", trimmedDate).
			Where("enrollment_ended_at IS NULL OR DATE(enrollment_ended_at) > ?", trimmedDate).
			Count(&count).Error; err != nil {
			return nil, newDBError(err, "historical enrollment count for date: "+trimmedDate)
		}
		results[trimmedDate] = count
	}

	return results, nil
}

func (db *Store) GetProgramClassDetailsByID(id int, args *QueryContext) ([]ProgramClassDetail, error) {
	var classDetails []ProgramClassDetail
	query := db.WithContext(args.Ctx).Table("program_class_cohorts ps").
		Select(`ps.*,
		fac.name as facility_name,
		pc.name as class_name,
		count(CASE WHEN pse.enrollment_status = 'Enrolled' THEN 1 END) as enrolled,
		count(CASE WHEN pse.enrollment_status = 'Completed' THEN 1 END) as completed,
		count(CASE WHEN pse.enrollment_status IN (`+completedOutcomeStatusSQLList()+`) THEN 1 END) as historical_enrollments,
		count(CASE WHEN pse.enrollment_status = 'Completed' AND pse.enrollment_ended_at IS NOT NULL THEN 1 END) as rate_eligible_completions,
		count(CASE WHEN pse.enrollment_status IN (`+completedOutcomeStatusSQLList()+`) AND pse.enrollment_ended_at IS NOT NULL THEN 1 END) as rate_eligible_enrollments
		`).
		Joins(`join facilities fac on fac.id = ps.facility_id
			AND fac.deleted_at IS NULL`).
		Joins(`join program_classes pc on pc.id = ps.class_id`).
		Joins(`left outer join program_class_enrollments pse on pse.cohort_id = ps.id`). //TODO Enrollment statuses may change here
		Where("ps.program_id = ?", id)

	if args.Params.Get("facility_id") != "" {
		query = query.Where("ps.facility_id = ?", args.FacilityID)
	} else if !args.CanSwitchFacility {
		query = query.Where("ps.facility_id = ?", args.FacilityID)
	}

	query = query.Group("ps.id,fac.name,pc.name")
	if args.Search != "" {
		// pc.name, not ps.name -- the screen shows the CLASS's name, so searching the
		// cohort's would return nothing for what the user can actually see.
		query = query.Where("LOWER(pc.name) LIKE ? OR LOWER(ps.description) LIKE ?", args.SearchQuery(), args.SearchQuery())
	}
	if err := query.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "programs")
	}
	if err := query.Limit(args.PerPage).Offset(args.CalcOffset()).Order(args.OrderClause("ps.created_at desc")).Find(&classDetails).Error; err != nil {
		return nil, newGetRecordsDBError(err, "programs")
	}

	classIDs := make([]uint, 0, len(classDetails))
	for _, detail := range classDetails { //gathering all for next query
		classIDs = append(classIDs, detail.ID)
	}
	events := []ProgramClassEvent{}
	if err := db.Model(&ProgramClassEvent{}).
		Preload("RoomRef").
		// See GetClasses: without Instructor the program-detail Classes tab renders "—"
		// in its Instructor column, because that column comes from the event, not the cohort.
		Preload("Instructor").
		Preload("Overrides").
		Preload("Overrides.RoomRef").
		Where("cohort_id IN (?)", classIDs).
		Find(&events).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_events")
	}
	eventMap := make(map[uint][]ProgramClassEvent)
	for _, event := range events {
		eventMap[event.CohortID] = append(eventMap[event.CohortID], event)
	}
	for i, j := 0, len(classDetails); i < j; i++ {
		classDetails[i].Events = eventMap[classDetails[i].ID]
	}

	return classDetails, nil
}

type ProgramClassOutcomes struct {
	Month       string `json:"month"`
	Drops       int    `json:"drops"`
	Completions int    `json:"completions"`
}

func (db *Store) GetProgramClassOutcomes(id int, args *QueryContext) ([]ProgramClassOutcomes, error) {
	var outcomes []ProgramClassOutcomes

	facilityID := args.FacilityID
	incompleteStatuses := []ProgramEnrollmentStatus{
		EnrollmentIncompleteDropped,
		EnrollmentIncompleteFailedToComplete,
		EnrollmentIncompleteTransfered,
		EnrollmentIncompleteSegregated}

	// Create a set that includes the last 6 months, excluding the current month, of program outcomes
	const lastSixMonthsSubquery = `(SELECT TO_CHAR(
		DATE_TRUNC('month', NOW()) - INTERVAL '1 month' * gs.i, 'YYYY-MM') AS month
		FROM generate_series(1, 6) AS gs(i))`

	enrollmentsSubquery := `(SELECT *
		FROM program_class_enrollments
		WHERE cohort_id IN (
			SELECT id FROM program_class_cohorts
			WHERE program_id = ? AND facility_id = ?
		))`

	query := db.WithContext(args.Ctx).
		Table(fmt.Sprintf("(%s) AS months", lastSixMonthsSubquery)).
		Select(`
			months.month,
			COALESCE(
          COUNT(CASE WHEN pce.enrollment_status = ? THEN pce.cohort_id END), 0) AS completions,
        	COALESCE(COUNT(CASE WHEN pce.enrollment_status IN (?) THEN pce.cohort_id END),0) AS drops
		`, EnrollmentCompleted, incompleteStatuses).
		Joins(fmt.Sprintf(`
			LEFT JOIN (%s) AS pce
			ON TO_CHAR(DATE_TRUNC('month', pce.updated_at), 'YYYY-MM') = months.month
		`, enrollmentsSubquery), id, facilityID).
		Group("months.month").
		Order(args.OrderBy)
	if err := query.Find(&outcomes).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_enrollments")
	}
	return outcomes, nil
}

func (db *Store) GetProgramClassesHistory(id int, tableName string, args *QueryContext) ([]ProgramClassesHistory, error) {
	history := []ProgramClassesHistory{}
	if err := db.WithContext(args.Ctx).Order(args.OrderClause("created_at desc")).
		Find(&history, "parent_ref_id = ? and table_name = ?", id, tableName).
		Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_classes_history")
	}
	return history, nil
}

func (db *Store) GetClassCreatedAtAndBy(id int, args *QueryContext) (ActivityHistoryResponse, error) {
	var classDetails ActivityHistoryResponse
	if err := db.WithContext(args.Ctx).Table("program_class_cohorts ps").
		Select("ps.created_at, u.username as admin_username").
		Joins("join users u on u.id = ps.create_user_id").
		Where("ps.id = ?", id).
		Scan(&classDetails).Error; err != nil {
		return classDetails, newNotFoundDBError(err, "program class cohort")
	}
	return classDetails, nil
}

func (db *Store) GetFacilityInstructors(facilityID int) ([]Instructor, error) {
	var instructors []Instructor

	if err := db.Table("users u").
		Select("u.id, u.username, u.name_first, u.name_last, u.email").
		Where("u.facility_id = ? AND u.role IN ? AND u.deactivated_at IS NULL", facilityID, []string{"facility_admin", "department_admin"}).
		Order("u.name_first, u.name_last").
		Find(&instructors).Error; err != nil {
		return nil, newGetRecordsDBError(err, "instructors")
	}

	// Prepend "Unassigned" option with ID = 0
	unassigned := Instructor{
		ID:        0,
		Username:  "unassigned",
		NameFirst: "Unassigned",
		NameLast:  "",
		Email:     "",
	}

	// Create a new slice with Unassigned first, then the real instructors
	result := make([]Instructor, 0, len(instructors)+1)
	result = append(result, unassigned)
	result = append(result, instructors...)

	return result, nil
}

func (db *Store) GetInstructorNameByID(instructorID uint, facilityID uint, roles ...users.Role) (string, error) {
	if len(roles) == 0 {
		roles = []users.Role{users.FacilityAdmin, users.DepartmentAdmin}
	}
	var instructorName string
	err := db.Table("users").
		Select("COALESCE(name_first || ' ' || name_last, username)").
		Where("id = ? AND facility_id = ? AND role IN ?",
			instructorID, facilityID, roles).
		Scan(&instructorName).Error
	if err != nil {
		return "", err
	}
	return instructorName, nil
}

func (db *Store) GetClassesByInstructor(instructorID, facilityID int, startDate, endDate string) ([]InstructorClassData, error) {
	var classes []InstructorClassData

	// Handle unassigned classes (instructorID = 0)
	var query *gorm.DB
	if instructorID == 0 {
		query = db.Table("program_class_cohorts pc").
			Select(`pc.id as id,
					cl.name as name,
					0 as session_count,
					COALESCE(COUNT(DISTINCT CASE WHEN pce.enrollment_status = ? THEN pce.id END), 0) as enrolled_count,
					0 as upcoming_sessions,
					0 as cancelled_sessions`, Enrolled).
			Joins("JOIN program_classes cl ON cl.id = pc.class_id").
			Joins("LEFT JOIN program_class_enrollments pce ON pce.cohort_id = pc.id").
			Where("NOT EXISTS (SELECT 1 FROM program_class_events WHERE cohort_id = pc.id AND instructor_id IS NOT NULL) AND pc.facility_id = ?", facilityID).
			Where("pc.status != ?", Cancelled).
			Group("pc.id, cl.name").
			Order("cl.name")
	} else {
		query = db.Table("program_class_cohorts pc").
			Select(`pc.id as id,
					cl.name as name,
					0 as session_count,
					COALESCE(COUNT(DISTINCT CASE WHEN pce.enrollment_status = ? THEN pce.id END), 0) as enrolled_count,
					0 as upcoming_sessions,
					0 as cancelled_sessions`, Enrolled).
			Joins("JOIN program_classes cl ON cl.id = pc.class_id").
			Joins("LEFT JOIN program_class_enrollments pce ON pce.cohort_id = pc.id").
			Where("EXISTS (SELECT 1 FROM program_class_events WHERE cohort_id = pc.id AND instructor_id = ?) AND pc.facility_id = ?", instructorID, facilityID).
			Where("pc.status != ?", Cancelled).
			Group("pc.id, cl.name").
			Order("cl.name")
	}

	if err := query.Find(&classes).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program classes")
	}

	start, end, err := parseDateRange(startDate, endDate)
	if err != nil {
		return nil, newDBError(err, err.Error())
	}

	for i := range classes {
		totalSessions, upcomingSessions, cancelledSessions, sessionDates, err := db.calculateSessionCounts(classes[i].ID, start, end)
		if err != nil {
			return nil, err
		}

		classes[i].SessionCount = totalSessions
		classes[i].UpcomingSessions = upcomingSessions
		classes[i].CancelledSessions = cancelledSessions
		classes[i].SessionDates = sessionDates

		var event struct {
			RecurrenceRule string `gorm:"column:recurrence_rule"`
			Duration       string `gorm:"column:duration"`
			RoomName       string `gorm:"column:room_name"`
		}
		db.Table("program_class_events pce").
			Select("pce.recurrence_rule, pce.duration, COALESCE(r.name, '') as room_name").
			Joins("LEFT JOIN rooms r ON r.id = pce.room_id").
			Where("pce.cohort_id = ?", classes[i].ID).
			Order("pce.created_at ASC").
			Limit(1).
			Scan(&event)

		if event.RecurrenceRule != "" {
			rule, rErr := rrule.StrToRRule(event.RecurrenceRule)
			if rErr == nil {
				classes[i].StartTime = rule.GetDTStart().Format("15:04")
			}
		}
		classes[i].Duration = event.Duration
		classes[i].Room = event.RoomName
	}

	return classes, nil
}

func (db *Store) calculateSessionCounts(classID int, startDate, endDate time.Time) (int, int, int, []string, error) {

	var events []struct {
		ProgramClassEvent
		Overrides []ProgramClassEventOverride `gorm:"foreignKey:EventID;references:ID"`
	}

	if err := db.Preload("Overrides").Where("cohort_id = ?", classID).Find(&events).Error; err != nil {
		return 0, 0, 0, nil, newGetRecordsDBError(err, "class events")
	}

	var totalSessions, upcomingSessions, cancelledSessions int
	var sessionDates []string

	for _, event := range events {
		rule, err := rrule.StrToRRule(event.RecurrenceRule)
		if err != nil {
			continue // Skip invalid rules
		}

		occurrences := rule.Between(startDate, endDate, true)
		totalSessions += len(occurrences)

		cancelledDates := make(map[string]bool)
		cancelledInDateRange := 0
		for _, override := range event.Overrides {
			if override.IsCancelled {
				overrideRule, err := rrule.StrToRRule(override.OverrideRrule)
				if err != nil {
					continue // Skip invalid override rules
				}

				overrideOccurrences := overrideRule.Between(startDate, endDate, true)
				cancelledInDateRange += len(overrideOccurrences)

				for _, occ := range overrideOccurrences {
					cancelledDates[occ.Format("2006-01-02")] = true
				}
			}
		}

		actualUpcoming := 0
		for _, occurrence := range occurrences {
			dateStr := occurrence.Format("2006-01-02")
			if !cancelledDates[dateStr] {
				actualUpcoming++
				sessionDates = append(sessionDates, dateStr)
			}
		}
		upcomingSessions += actualUpcoming

		cancelledSessions += cancelledInDateRange
	}

	return totalSessions, upcomingSessions, cancelledSessions, sessionDates, nil
}

func (db *Store) BulkCancelSessions(req *BulkCancelSessionsRequest, facilityID int, claims BulkCancelClaims) (*BulkCancelSessionsResponse, error) {
	ctx := context.Background()

	if err := Validate().Struct(req); err != nil {
		return nil, newDBError(err, "bulk cancel sessions validation error")
	}

	tx := db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, newUpdateDBError(tx.Error, "begin transaction")
	}
	defer func() {
		if r := recover(); r != nil {
			if err := tx.Rollback().Error; err != nil {
				log.WithError(err).Error("Error rolling back transaction in BulkCancelSessions after panic")
			}
			log.WithField("stack", string(debug.Stack())).Error("Panic in BulkCancelSessions")
			panic(r)
		}
	}()

	start, end, err := parseDateRange(req.StartDate, req.EndDate)
	if err != nil {
		tx.Rollback()
		return nil, newDBError(err, err.Error())
	}

	var baseEvents []ProgramClassEvent
	var query *gorm.DB
	if req.InstructorID == 0 {
		query = tx.Table("program_class_events pce").
			Select("pce.*").
			Joins("INNER JOIN program_class_cohorts pc ON pc.id = pce.cohort_id").
			Where("pce.instructor_id IS NULL AND pc.facility_id = ?", facilityID)
	} else {
		query = tx.Table("program_class_events pce").
			Select("pce.*").
			Joins("INNER JOIN program_class_cohorts pc ON pc.id = pce.cohort_id").
			Where("pce.instructor_id = ? AND pc.facility_id = ?", req.InstructorID, facilityID)
	}

	if err := query.Find(&baseEvents).Error; err != nil {
		tx.Rollback()
		return nil, newGetRecordsDBError(err, "class events")
	}

	var eventInstances []struct {
		Event      ProgramClassEvent
		Occurrence time.Time
	}
	for _, baseEvent := range baseEvents {
		rule, err := baseEvent.GetRRule()
		if err != nil {
			continue
		}
		occurrences := rule.Between(start, end, true)
		firstOccurrence := rule.After(time.Time{}, false)
		canonicalHour, canonicalMinute := getCanonicalHourAndMinute([]time.Time{firstOccurrence}, claims.GetTimezone())
		userLocation, _ := time.LoadLocation(claims.GetTimezone())
		for _, occurrence := range occurrences {
			consistentOccurrence := time.Date(
				occurrence.Year(),
				occurrence.Month(),
				occurrence.Day(),
				canonicalHour,
				canonicalMinute,
				0,
				0,
				userLocation,
			).UTC()
			eventInstances = append(eventInstances, struct {
				Event      ProgramClassEvent
				Occurrence time.Time
			}{
				Event:      baseEvent,
				Occurrence: consistentOccurrence,
			})
		}
	}

	if len(eventInstances) == 0 {
		if err := tx.Rollback().Error; err != nil {
			log.WithError(err).Error("Error rolling back transaction in BulkCancelSessions")
		}
		return &BulkCancelSessionsResponse{
			Success:      false,
			SessionCount: 0,
			ClassCount:   0,
			StudentCount: 0,
			Classes:      []AffectedClass{},
		}, nil
	}

	// Get unique classes affected and count cancelled sessions
	classMap := make(map[int]*AffectedClass)
	for _, instance := range eventInstances {
		if _, exists := classMap[int(instance.Event.CohortID)]; !exists {
			classMap[int(instance.Event.CohortID)] = &AffectedClass{
				ClassID:           int(instance.Event.CohortID),
				ClassName:         "",
				UpcomingSessions:  0,
				CancelledSessions: 0,
				StudentCount:      0,
			}
		}
		classMap[int(instance.Event.CohortID)].CancelledSessions++
	}

	classIDs := make([]uint, 0, len(classMap))
	for classID := range classMap {
		classIDs = append(classIDs, uint(classID))
	}

	countMap, err := db.GetActiveEnrollmentCountsForClasses(&QueryContext{Ctx: ctx}, classIDs)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	type classInfo struct {
		ID   int    `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	var classInfos []classInfo
	if err := tx.Table("program_class_cohorts c").
		Joins("JOIN program_classes pc ON pc.id = c.class_id").
		Select("c.id AS id, pc.name AS name").
		Where("c.id IN ?", classIDs).
		Scan(&classInfos).Error; err != nil {
		tx.Rollback()
		return nil, newGetRecordsDBError(err, "class names")
	}

	nameMap := make(map[int]string)
	for _, info := range classInfos {
		nameMap[info.ID] = info.Name
	}

	for classID := range classMap {
		classMap[classID].StudentCount = countMap[uint(classID)]
		classMap[classID].ClassName = nameMap[classID]
	}

	existingOverridesMap := make(map[uint]map[string]bool)

	eventIDs := make([]uint, 0, len(eventInstances))
	rruleMap := make(map[string]bool)
	for _, instance := range eventInstances {
		eventIDs = append(eventIDs, instance.Event.ID)
		overrideRrule := fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;COUNT=1",
			instance.Occurrence.Format("20060102T150405Z"))
		rruleMap[overrideRrule] = true
	}

	if len(eventIDs) > 0 {
		var existingOverrides []ProgramClassEventOverride
		rruleList := make([]string, 0, len(rruleMap))
		for rrule := range rruleMap {
			rruleList = append(rruleList, rrule)
		}

		if err := tx.Table("program_class_event_overrides").
			Where("event_id IN ? AND override_rrule IN ? AND is_cancelled = ? AND deleted_at IS NULL",
				eventIDs, rruleList, true).
			Find(&existingOverrides).Error; err != nil {
			tx.Rollback()
			return nil, newGetRecordsDBError(err, "existing overrides")
		}

		for _, override := range existingOverrides {
			if existingOverridesMap[override.EventID] == nil {
				existingOverridesMap[override.EventID] = make(map[string]bool)
			}
			existingOverridesMap[override.EventID][override.OverrideRrule] = true
		}
	}
	var newCancellations []struct {
		Event      ProgramClassEvent
		Occurrence time.Time
	}
	var alreadyCancelled []struct {
		Event      ProgramClassEvent
		Occurrence time.Time
	}

	for _, instance := range eventInstances {
		overrideRrule := fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;COUNT=1",
			instance.Occurrence.Format("20060102T150405Z"))

		// Check if this override already exists
		if eventOverrides, exists := existingOverridesMap[instance.Event.ID]; exists {
			if eventOverrides[overrideRrule] {
				// Already cancelled
				alreadyCancelled = append(alreadyCancelled, instance)
				continue
			}
		}
		newCancellations = append(newCancellations, instance)
	}

	classMap = make(map[int]*AffectedClass)
	for _, instance := range newCancellations {
		if _, exists := classMap[int(instance.Event.CohortID)]; !exists {
			classMap[int(instance.Event.CohortID)] = &AffectedClass{
				ClassID:           int(instance.Event.CohortID),
				ClassName:         "",
				UpcomingSessions:  0,
				CancelledSessions: 0,
				StudentCount:      0,
			}
		}
		classMap[int(instance.Event.CohortID)].CancelledSessions++
	}

	for classID := range classMap {
		classMap[classID].StudentCount = countMap[uint(classID)]
		classMap[classID].ClassName = nameMap[classID]
	}

	var overrides []ProgramClassEventOverride
	var auditEntries []ChangeLogEntry
	for _, instance := range newCancellations {
		overrideRrule := fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;COUNT=1",
			instance.Occurrence.Format("20060102T150405Z"))

		overrides = append(overrides, ProgramClassEventOverride{
			EventID:       instance.Event.ID,
			OverrideRrule: overrideRrule,
			Duration:      instance.Event.Duration,
			RoomID:        instance.Event.RoomID,
			IsCancelled:   true,
			Reason:        StringPtr(req.Reason),
		})

		oldStatus := "Scheduled"
		newStatus := "Cancelled"

		auditEntry := NewChangeLogEntry(
			TableNameCohort,
			"status",
			&oldStatus,
			&newStatus,
			instance.Event.CohortID,
			claims.GetUserID(),
		)
		auditEntries = append(auditEntries, *auditEntry)
	}

	// Bulk insert overrides with ON CONFLICT DO NOTHING for idempotency (unique partial index on event_id, override_rrule WHERE deleted_at IS NULL).
	if len(overrides) > 0 {
		const overrideBatchSize = 100
		if err := tx.Clauses(clause.OnConflict{
			Columns:     []clause.Column{{Name: "event_id"}, {Name: "override_rrule"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "deleted_at IS NULL"}}},
			DoNothing:   true,
		}).CreateInBatches(overrides, overrideBatchSize).Error; err != nil {
			tx.Rollback()
			return nil, newCreateDBError(err, "event override")
		}
	}

	if len(auditEntries) > 0 {
		if err := tx.Create(&auditEntries).Error; err != nil {
			tx.Rollback()
			return nil, newCreateDBError(err, "change log entries")
		}
	}

	if err := tx.Commit().Error; err != nil {
		return nil, newUpdateDBError(err, "commit transaction")
	}

	var affectedClasses []AffectedClass
	for _, class := range classMap {
		affectedClasses = append(affectedClasses, *class)
	}

	totalStudents := 0
	for _, class := range affectedClasses {
		totalStudents += class.StudentCount
	}

	alreadyCancelledCount := len(alreadyCancelled)
	var message string
	if alreadyCancelledCount > 0 {
		if len(newCancellations) == 0 {
			message = fmt.Sprintf("All %d sessions in the selected range were already cancelled.", alreadyCancelledCount)
		} else {
			message = fmt.Sprintf("Successfully cancelled %d sessions. %d sessions were already cancelled and were skipped.",
				len(newCancellations), alreadyCancelledCount)
		}
	}

	response := &BulkCancelSessionsResponse{
		Success:               true,
		SessionCount:          len(newCancellations),
		ClassCount:            len(affectedClasses),
		StudentCount:          totalStudents,
		AlreadyCancelledCount: alreadyCancelledCount,
		Message:               message,
		Classes:               affectedClasses,
	}

	return response, nil
}

func (db *Store) DeleteClass(id int) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// Read the parent BEFORE the delete: afterwards GORM's soft-delete scope hides
		// the cohort row and there is nothing left to resolve class_id from.
		var classID uint
		if err := tx.Model(&ProgramClassCohort{}).
			Select("class_id").
			Where("id = ?", id).
			Scan(&classID).Error; err != nil {
			return newGetRecordsDBError(err, "program class parent")
		}

		if err := tx.Where("cohort_id = ?", id).Delete(&ProgramClassEvent{}).Error; err != nil {
			return newDeleteDBError(err, "program_class_events")
		}
		if err := tx.Exec(`DELETE FROM change_log_entries WHERE table_name = ? AND parent_ref_id = ?`, TableNameCohort, id).Error; err != nil {
			return newDeleteDBError(err, "change_log_entries")
		}
		if err := tx.Exec(`DELETE FROM program_classes_history WHERE table_name = ? AND parent_ref_id = ?`, TableNameCohort, id).Error; err != nil {
			return newDeleteDBError(err, "program_classes_history")
		}
		if err := tx.Delete(&ProgramClassCohort{}, "id = ?", id).Error; err != nil {
			return newDeleteDBError(err, "program class")
		}
		return deleteClassIfLastCohort(tx, classID)
	})
}

/*
deleteClassIfLastCohort removes the parent CLASS once its final cohort is gone, so
deleting the only class a user can see does not leave an invisible class-tier row behind.
That orphan is not cosmetic: it still matches the create form's class dropdown and it
still counts toward the duplicate-name guard, so a facility would be offered a "class"
with no runs and be unable to recreate one under the same name.

Three things make this safe rather than destructive:

  - the CLASS is SOFT-deleted, exactly as the cohort is. Its row stays put, so none of
    the FKs pointing at program_classes fire -- and two of them would bite on a hard
    delete: class_completions.class_id is ON DELETE SET NULL (it would blank the
    certificate's class link) while program_class_cohorts and program_class_enrollments
    have no ON DELETE at all, so a hard delete would be RESTRICTed by the soft-deleted
    rows still referencing it.
  - the remaining-cohort count runs through GORM's default scope, so soft-deleted
    siblings correctly do NOT keep the class alive.
  - a class still referenced by a live certificate is LEFT ALONE. The cohort delete
    guard only checks completions for the cohort being deleted; a certificate earned in
    a sibling cohort that was itself deleted earlier still points at the class, and
    hiding it would orphan that certificate's class link.
*/
func deleteClassIfLastCohort(tx *gorm.DB, classID uint) error {
	if classID == 0 {
		return nil
	}
	var remainingCohorts int64
	if err := tx.Model(&ProgramClassCohort{}).
		Where("class_id = ?", classID).
		Count(&remainingCohorts).Error; err != nil {
		return newGetRecordsDBError(err, "remaining cohorts")
	}
	if remainingCohorts > 0 {
		return nil
	}

	var completions int64
	if err := tx.Model(&ClassCompletion{}).
		Where("class_id = ?", classID).
		Count(&completions).Error; err != nil {
		return newGetRecordsDBError(err, "class completions")
	}
	if completions > 0 {
		return nil
	}

	if err := tx.Exec(`DELETE FROM change_log_entries WHERE table_name = ? AND parent_ref_id = ?`, TableNameClass, classID).Error; err != nil {
		return newDeleteDBError(err, "change_log_entries")
	}
	if err := tx.Exec(`DELETE FROM program_classes_history WHERE table_name = ? AND parent_ref_id = ?`, TableNameClass, classID).Error; err != nil {
		return newDeleteDBError(err, "program_classes_history")
	}
	if err := tx.Delete(&ProgramClass{}, "id = ?", classID).Error; err != nil {
		return newDeleteDBError(err, "program class tier")
	}
	return nil
}

// GetClassesForProgram returns the CLASS tier under a program, each with its
// cohorts rolled up. Facility scoping matches the cohort query: an explicit
// facility_id param, or the caller's own facility when they cannot switch.
//
// Scope on args.FacilityID, NOT on the presence of the raw param. getQueryContext
// parses facility_id and leaves FacilityID at 0 when the value is unparseable, so
// keying off Params.Get("facility_id") != "" made "?facility_id=abc" filter on
// facility_id = 0 and silently return an empty list. The `!CanSwitchFacility` arm
// stays so a non-switching caller still fails closed if their claim carries no
// facility.
func (db *Store) GetClassesForProgram(programID int, args *QueryContext) ([]ProgramClass, error) {
	classes := make([]ProgramClass, 0, args.PerPage)

	query := db.WithContext(args.Ctx).Table("program_classes pc").
		Where("pc.deleted_at IS NULL AND pc.program_id = ?", programID)

	if args.FacilityID != 0 || !args.CanSwitchFacility {
		query = query.Where("pc.facility_id = ?", args.FacilityID)
	}
	if args.Search != "" {
		query = query.Where("LOWER(pc.name) LIKE ? OR LOWER(pc.description) LIKE ?",
			args.SearchQuery(), args.SearchQuery())
	}

	if err := db.WithContext(args.Ctx).Table("(?) AS counted", query).
		Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_classes")
	}
	if err := query.Order("pc.name").
		Limit(args.PerPage).Offset(args.CalcOffset()).
		Scan(&classes).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_classes")
	}
	// Rollups are computed fields (gorm:"-"), so GORM will not scan them from aliased
	// SELECT columns -- it drops ignored fields from the schema entirely. Populating them
	// through the shared helper keeps list and detail numerically identical by
	// construction rather than by two queries that happen to agree today.
	ids := make([]uint, 0, len(classes))
	for i := range classes {
		ids = append(ids, classes[i].ID)
	}
	rollups, err := db.classRollupsFor(args, ids)
	if err != nil {
		return nil, err
	}
	for i := range classes {
		if r, ok := rollups[classes[i].ID]; ok {
			classes[i].CohortCount = r.CohortCount
			classes[i].ActiveCohorts = r.ActiveCohorts
			classes[i].ScheduledCohorts = r.ScheduledCohorts
			classes[i].CompletedCohorts = r.CompletedCohorts
			classes[i].Capacity = r.Capacity
			classes[i].Enrolled = r.Enrolled
			classes[i].Completed = r.Completed
		}
	}
	return classes, nil
}

// GetClassByID returns one class with its rollups, its cohorts, and its credit
// types. An empty CreditTypes slice means "inherit from the program" -- callers
// must resolve with ProgramClass.CreditTypesOrInherited, never read it raw.
func (db *Store) GetClassByID(id int, args *QueryContext) (*ProgramClass, error) {
	var class ProgramClass
	if err := db.WithContext(args.Ctx).
		Preload("Cohorts", "deleted_at IS NULL").
		Preload("CreditTypes").
		First(&class, "id = ?", id).Error; err != nil {
		return nil, newNotFoundDBError(err, "program_classes")
	}

	rollups, err := db.classRollupsFor(args, []uint{class.ID})
	if err != nil {
		return nil, err
	}
	if r, ok := rollups[class.ID]; ok {
		class.CohortCount = r.CohortCount
		class.ActiveCohorts = r.ActiveCohorts
		class.ScheduledCohorts = r.ScheduledCohorts
		class.CompletedCohorts = r.CompletedCohorts
		class.Capacity = r.Capacity
		class.Enrolled = r.Enrolled
		class.Completed = r.Completed
	}
	if err := db.resolveInheritedCreditTypes(args, &class); err != nil {
		return nil, err
	}
	return &class, nil
}

// resolveInheritedCreditTypes fills in a class's credit types from its program when the
// class defines no override of its own, so callers do not have to know the
// empty-means-inherit rule to render a class correctly.
func (db *Store) resolveInheritedCreditTypes(args *QueryContext, class *ProgramClass) error {
	if len(class.CreditTypes) > 0 {
		return nil
	}
	var programTypes []ProgramCreditType
	if err := db.WithContext(args.Ctx).
		Where("program_id = ?", class.ProgramID).
		Find(&programTypes).Error; err != nil {
		return newGetRecordsDBError(err, "program_credit_types")
	}
	inherited := make([]ProgramClassCreditType, 0, len(programTypes))
	for _, pt := range programTypes {
		inherited = append(inherited, ProgramClassCreditType{
			ClassID: class.ID, CreditType: pt.CreditType})
	}
	class.CreditTypes = inherited
	return nil
}

type classRollup struct {
	ClassID          uint
	CohortCount      int64
	ActiveCohorts    int64
	ScheduledCohorts int64
	CompletedCohorts int64
	Capacity         int64
	Enrolled         int64
	Completed        int64
}

// classRollupsFor aggregates cohorts up to their class, for any number of classes.
//
// One definition, used by both the list and the detail read. Two divergent definitions
// of "enrolled" is how reporting bugs start, and this restructure exists precisely to
// make these numbers meaningful.
//
// COUNT(DISTINCT user_id) is deliberate: after a merge one resident may hold enrollments
// in two sibling cohorts of the same class, and they are still one person in a class
// headcount. Capacity, by contrast, is a per-cohort quantity and is summed.
func (db *Store) classRollupsFor(args *QueryContext, classIDs []uint) (map[uint]classRollup, error) {
	out := make(map[uint]classRollup, len(classIDs))
	if len(classIDs) == 0 {
		return out, nil
	}
	var rows []classRollup
	err := db.WithContext(args.Ctx).Raw(`
SELECT pc.id AS class_id,
  (SELECT COUNT(*) FROM program_class_cohorts c
     WHERE c.class_id = pc.id AND c.deleted_at IS NULL) AS cohort_count,
  -- Per-status cohort counts. The program page used to group cohorts by status; the
  -- class tier keeps that information on the class row rather than losing it.
  (SELECT COUNT(*) FROM program_class_cohorts c
     WHERE c.class_id = pc.id AND c.deleted_at IS NULL AND c.status = 'Active') AS active_cohorts,
  (SELECT COUNT(*) FROM program_class_cohorts c
     WHERE c.class_id = pc.id AND c.deleted_at IS NULL AND c.status = 'Scheduled') AS scheduled_cohorts,
  (SELECT COUNT(*) FROM program_class_cohorts c
     WHERE c.class_id = pc.id AND c.deleted_at IS NULL AND c.status = 'Completed') AS completed_cohorts,
  (SELECT COALESCE(SUM(c.capacity), 0) FROM program_class_cohorts c
     WHERE c.class_id = pc.id AND c.deleted_at IS NULL) AS capacity,
  (SELECT COUNT(DISTINCT e.user_id) FROM program_class_enrollments e
     WHERE e.class_id = pc.id AND e.deleted_at IS NULL
       AND e.enrollment_status = 'Enrolled') AS enrolled,
  (SELECT COUNT(DISTINCT cc.user_id) FROM class_completions cc
     WHERE cc.class_id = pc.id AND cc.deleted_at IS NULL) AS completed
FROM program_classes pc
WHERE pc.id IN ?`, classIDs).Scan(&rows).Error
	if err != nil {
		return nil, newGetRecordsDBError(err, "program_classes")
	}
	for _, r := range rows {
		out[r.ClassID] = r
	}
	return out, nil
}

// GetClassesForFacility lists the class tier at the caller's facility.
func (db *Store) GetClassesForFacility(args *QueryContext) ([]ProgramClass, error) {
	classes := make([]ProgramClass, 0, args.PerPage)
	tx := db.WithContext(args.Ctx).Model(&ProgramClass{}).
		Where("facility_id = ? AND deleted_at IS NULL", args.FacilityID)
	if args.Search != "" {
		tx = tx.Where("LOWER(name) LIKE ?", args.SearchQuery())
	}
	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_classes")
	}
	if err := tx.Order("name").Limit(args.PerPage).Offset(args.CalcOffset()).
		Find(&classes).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_classes")
	}
	return classes, nil
}

// CreateClass creates a class and, optionally, its class-level credit types.
func (db *Store) CreateClass(class *ProgramClass, creditTypes []CreditType) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(class).Error; err != nil {
			return newCreateDBError(err, "program_classes")
		}
		return replaceClassCreditTypes(tx, class.ID, creditTypes)
	})
}

// UpdateClass updates class-level fields. It deliberately cannot touch status,
// capacity or dates -- those live on the cohort.
func (db *Store) UpdateClass(id int, updates map[string]any, creditTypes []CreditType) (*ProgramClass, error) {
	var class ProgramClass
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&class, "id = ?", id).Error; err != nil {
			return newNotFoundDBError(err, "program_classes")
		}
		if len(updates) > 0 {
			if err := tx.Model(&class).Updates(updates).Error; err != nil {
				return newUpdateDBError(err, "program_classes")
			}
		}
		if creditTypes != nil {
			return replaceClassCreditTypes(tx, class.ID, creditTypes)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &class, nil
}

// replaceClassCreditTypes sets a class's credit-type override wholesale.
//
// Passing an EMPTY slice is meaningful and is not the same as passing nil: it clears
// the override, which returns the class to inheriting its program's credit types.
func replaceClassCreditTypes(tx *gorm.DB, classID uint, creditTypes []CreditType) error {
	if err := tx.Where("class_id = ?", classID).
		Delete(&ProgramClassCreditType{}).Error; err != nil {
		return newDeleteDBError(err, "program_class_credit_types")
	}
	if len(creditTypes) == 0 {
		return nil
	}
	rows := make([]ProgramClassCreditType, 0, len(creditTypes))
	for _, ct := range creditTypes {
		rows = append(rows, ProgramClassCreditType{ClassID: classID, CreditType: ct})
	}
	if err := tx.Create(&rows).Error; err != nil {
		return newCreateDBError(err, "program_class_credit_types")
	}
	return nil
}
