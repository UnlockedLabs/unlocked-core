package programs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/users"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (db *Store) GetProgramByID(id int) (*Program, error) {
	content := &Program{}
	if err := db.Preload("ProgramTypes").Preload("ProgramCreditTypes").Preload("FacilitiesPrograms.Facility").First(content, id).Error; err != nil {
		return nil, newNotFoundDBError(err, "programs")
	}

	for _, fp := range content.FacilitiesPrograms {
		if fp.Facility != nil {
			content.Facilities = append(content.Facilities, *fp.Facility)
		}
	}

	return content, nil
}

func (db *Store) GetProgramNamesByIDs(ids []uint) ([]string, error) {
	var names []string
	if len(ids) == 0 {
		return names, nil
	}
	if err := db.Model(&Program{}).Where("id IN ?", ids).Order("name").Pluck("name", &names).Error; err != nil {
		return nil, newGetRecordsDBError(err, "programs")
	}
	return names, nil
}

func (db *Store) FetchEnrollmentMetrics(programID int, facilityId uint) (*ProgramOverviewResponse, error) {
	var metrics ProgramOverviewResponse

	query := `
		COUNT(CASE WHEN pce.enrollment_status = 'Enrolled' AND pce.enrolled_at IS NOT NULL AND (pce.enrollment_ended_at IS NULL OR pce.enrollment_ended_at > CURRENT_TIMESTAMP) THEN 1 END) AS active_enrollments,
		COUNT(CASE WHEN pce.enrollment_status = 'Completed' THEN 1 END) AS completions,
		COUNT(CASE WHEN pce.enrolled_at IS NOT NULL THEN 1 END) AS total_enrollments,
		COUNT(DISTINCT CASE WHEN pce.enrollment_status = 'Enrolled' AND pce.enrolled_at IS NOT NULL AND (pce.enrollment_ended_at IS NULL OR pce.enrollment_ended_at > CURRENT_TIMESTAMP) THEN pce.user_id END) as active_residents,
		COUNT(CASE WHEN pce.enrollment_status = 'Completed' AND pce.enrollment_ended_at IS NOT NULL AND ` + rateEligibleClassStatusSQL + ` THEN 1 END) * 100.0
			/ NULLIF(COUNT(CASE WHEN pce.enrollment_status IN (` + completedOutcomeStatusSQLList() + `) AND pce.enrollment_ended_at IS NOT NULL AND ` + rateEligibleClassStatusSQL + ` THEN 1 END), 0) AS completion_rate
	`

	tx := db.Table("program_class_enrollments pce").
		Select(query).
		Joins("JOIN program_class_cohorts pc ON pce.cohort_id = pc.id").
		Where("pc.program_id = ?", programID)
	if facilityId != 0 {
		tx = tx.Where("pc.facility_id = ?", facilityId)
	}

	if err := tx.Scan(&metrics).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_enrollments")
	}

	partialAttendanceSQL := buildPartialAttendanceSQL(db.Name(), "pcea")
	attendanceQuery := fmt.Sprintf(`
		SELECT
			SUM(
				CASE
					WHEN pcea.attendance_status = 'present' THEN 1
					WHEN pcea.attendance_status = 'partial' THEN %s
					ELSE 0
				END
			) * 100.0 /
				NULLIF(COUNT(CASE WHEN pcea.attendance_status IS NOT NULL AND pcea.attendance_status != '' THEN 1 END), 0) AS attendance_rate
		FROM program_class_cohorts pc
		LEFT JOIN program_class_enrollments pce ON pce.cohort_id = pc.id
		LEFT JOIN program_class_events pcev ON pcev.cohort_id = pc.id
		LEFT JOIN program_class_event_attendance pcea ON pcea.event_id = pcev.id AND pcea.user_id = pce.user_id AND pcea.deleted_at IS NULL
		WHERE pc.program_id = ?`, partialAttendanceSQL)

	attendanceArgs := []any{programID}
	if facilityId != 0 {
		attendanceQuery += " AND pc.facility_id = ?"
		attendanceArgs = append(attendanceArgs, facilityId)
	}

	var attendanceResult struct {
		AttendanceRate *float64 `json:"attendance_rate"`
	}
	if err := db.Raw(attendanceQuery, attendanceArgs...).Scan(&attendanceResult).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_event_attendance")
	}
	metrics.AttendanceRate = attendanceResult.AttendanceRate
	return &metrics, nil
}

func (db *Store) GetActiveClassFacilityIDs(ctx context.Context, id int) ([]int, error) {
	activeClassFacilityIDs := make([]int, 0)
	if err := db.WithContext(ctx).Model(&ProgramClassCohort{}).Select("DISTINCT facility_id").
		Where("program_id = ? and status in ('Active', 'Scheduled')", id).
		Scan(&activeClassFacilityIDs).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program class cohort")
	}
	return activeClassFacilityIDs, nil
}

func (db *Store) GetProgramActiveClassFacilities(ctx context.Context, id uint) ([]string, error) {
	var facilities []string
	if err := db.WithContext(ctx).Table("program_class_cohorts").
		Joins("JOIN facilities ON facilities.id = program_class_cohorts.facility_id").
		Where("program_class_cohorts.program_id = ? AND program_class_cohorts.status IN (?, ?) AND program_class_cohorts.archived_at IS NULL", id,
			Active, Scheduled).
		Distinct().
		Pluck("facilities.name", &facilities).Error; err != nil {
		return nil, newGetRecordsDBError(err, fmt.Sprintf("program %d active facilities", id))
	}
	return facilities, nil
}

func (db *Store) GetPrograms(args *QueryContext) ([]Program, error) {
	content := make([]Program, 0, args.PerPage)
	tx := db.WithContext(args.Ctx).Model(&Program{}).
		Preload("ProgramTypes").
		Preload("ProgramCreditTypes").
		Preload("FacilitiesPrograms.Facility")
	if len(args.Tags) > 0 {
		tx = tx.Joins("JOIN program_types t ON t.program_id = programs.id").Where("t.id IN (?)", args.Tags)
	}
	tx = tx.Order(args.OrderClause("programs.created_at desc"))

	if args.Search != "" {
		tx = tx.Where("LOWER(name) LIKE ? OR LOWER(description) LIKE ? ", args.SearchQuery(), args.SearchQuery())
	}

	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "programs")
	}
	if err := tx.Limit(args.PerPage).Offset(args.CalcOffset()).Find(&content).Error; err != nil {
		return nil, newGetRecordsDBError(err, "programs")
	}
	for i := range content {
		for _, fp := range content[i].FacilitiesPrograms {
			if fp.Facility != nil {
				content[i].Facilities = append(content[i].Facilities, *fp.Facility)
			}
		}
	}
	return content, nil
}

func (db *Store) CreateProgram(content *Program) error {
	err := Validate().Struct(content)
	if err != nil {
		return newDBError(err, "create programs validation error")
	}
	if err := db.Create(content).Error; err != nil {
		return newCreateDBError(err, "programs")
	}
	return nil
}

// UpdateProgram applies a partial update to a program.
//
// hasProgramCompletion carries the tri-state the wire needs: nil means the caller did not
// send the field and the stored value must survive, non-nil is an explicit set. It cannot
// be read off program.HasProgramCompletion because updateStruct ALWAYS copies
// bools, so an omitted field is indistinguishable from an explicit false by then.
//
// ⚠️  The read is inside the transaction and takes a row lock, and that is load-bearing.
//
//	This function is a read-modify-write: it reads the program, merges the caller's
//	non-zero fields over it, and writes the merged row back. With the read outside the
//	transaction, two overlapping updates each read the old row and the slower writer
//	silently reverses the faster one -- a lost update, for EVERY field, not just the
//	completion flag. Locking the row makes the second update wait for the first.
//
// getProgramByIDForUpdate is GetProgramByID scoped to a transaction and holding a row
// lock, so a read-modify-write cannot interleave with another writer. Preloads run as
// their own SELECTs and are deliberately not locked -- only the programs row needs it.
func getProgramByIDForUpdate(tx *gorm.DB, id int) (*Program, error) {
	content := &Program{}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Preload("ProgramTypes").Preload("ProgramCreditTypes").Preload("FacilitiesPrograms.Facility").
		First(content, id).Error; err != nil {
		return nil, newNotFoundDBError(err, "programs")
	}
	for _, fp := range content.FacilitiesPrograms {
		if fp.Facility != nil {
			content.Facilities = append(content.Facilities, *fp.Facility)
		}
	}
	return content, nil
}

func (db *Store) UpdateProgram(program *Program, facilityIds []int, hasProgramCompletion *bool) (*Program, error) {
	var allChanges []ChangeLogEntry
	//begin the transaction
	trans := db.Begin() //only need to pass context here, it will be used/shared within the transaction
	if trans.Error != nil {
		return nil, newDBError(trans.Error, "unable to start the database transaction")
	}
	updatePrg, err := getProgramByIDForUpdate(trans, int(program.ID))
	if err != nil {
		trans.Rollback()
		return nil, err
	}
	// Resolve the flag against the LOCKED row, and do it before the change log is
	// generated -- otherwise an omitted field is diffed as true -> false and logged as a
	// change that never happens.
	if hasProgramCompletion != nil {
		program.HasProgramCompletion = *hasProgramCompletion
	} else {
		program.HasProgramCompletion = updatePrg.HasProgramCompletion
	}
	ignoredFieldNames := []string{"create_user_id", "update_user_id", "program_types", "credit_types", "facilities", "archived_at"}
	programLogEntries := GenerateChangeLogEntries(updatePrg, program, "programs", updatePrg.ID, DerefUint(program.UpdateUserID), ignoredFieldNames)
	allChanges = append(allChanges, programLogEntries...)
	updateStruct(&updatePrg, &program)
	facLogEntries, err := updateFacilitiesPrograms(trans, updatePrg, facilityIds)
	if err != nil {
		trans.Rollback()
		return nil, err
	}
	allChanges = append(allChanges, facLogEntries...)
	prgTypeLogEntries, err := updateProgramTypes(trans, updatePrg, program.ProgramTypes)
	if err != nil {
		trans.Rollback()
		return nil, err
	}
	allChanges = append(allChanges, prgTypeLogEntries...)
	creditTypeLogEntries, err := updateProgramCreditTypes(trans, updatePrg, program.ProgramCreditTypes)
	if err != nil {
		trans.Rollback()
		return nil, err
	}
	allChanges = append(allChanges, creditTypeLogEntries...)
	// ⚠️  This Select list is hand-maintained and fails OPEN: a field missing from it is
	//     silently NOT persisted -- the update succeeds and the value is dropped. Any new
	//     Program column the edit form can change must be added here.
	if err := trans.Omit("Facilities", "CreatedAt").Select("IsActive", "Name", "Description", "FundingType", "UpdateUserID", "HasProgramCompletion").Updates(&updatePrg).Error; err != nil {
		trans.Rollback()
		return nil, newUpdateDBError(err, "programs")
	}

	if len(allChanges) > 0 {
		if err := trans.Create(&allChanges).Error; err != nil {
			trans.Rollback()
			return nil, newCreateDBError(err, "change_log_entries")
		}
	}
	//end transaction
	if err := trans.Commit().Error; err != nil {
		return nil, newDBError(err, "unable to commit the database transaction")
	}
	return updatePrg, nil
}

func updateFacilitiesPrograms(trans *gorm.DB, updateProgram *Program, facilityIds []int) ([]ChangeLogEntry, error) {
	var (
		currentFacPrgs []FacilitiesPrograms
		logEntries     []ChangeLogEntry
		facilityID     uint
	)
	if err := trans.Where("program_id = ?", updateProgram.ID).Find(&currentFacPrgs).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facilities_programs")
	}

	currentFacPrgsMap := make(map[uint]struct{}, len(currentFacPrgs)) //added light weight map used for lookup
	for _, fp := range currentFacPrgs {
		currentFacPrgsMap[fp.FacilityID] = struct{}{}
	}

	updateFacPrgsMap := make(map[uint]struct{}, len(facilityIds)) //added light weight map used for lookup
	for _, facId := range facilityIds {
		facilityID = uint(facId)
		updateFacPrgsMap[facilityID] = struct{}{}
		if _, ok := currentFacPrgsMap[facilityID]; !ok {
			logEntries = append(logEntries, *NewChangeLogEntry("programs", "facility_id", nil, StringPtr(strconv.Itoa(int(facilityID))), updateProgram.ID, DerefUint(updateProgram.UpdateUserID)))
			if err := trans.Create(&FacilitiesPrograms{
				ProgramID:  updateProgram.ID,
				FacilityID: facilityID,
			}).Error; err != nil {
				return nil, newCreateDBError(err, "facilities_programs")
			}
		}
	}

	for facId := range currentFacPrgsMap {
		if _, ok := updateFacPrgsMap[facId]; !ok { //delete entry
			logEntries = append(logEntries, *NewChangeLogEntry("programs", "facility_id", StringPtr(strconv.Itoa(int(facId))), nil, updateProgram.ID, DerefUint(updateProgram.UpdateUserID)))
			now := time.Now().UTC()
			updates := map[string]any{
				"update_user_id": DerefUint(updateProgram.UpdateUserID),
				"deleted_at":     gorm.DeletedAt{Time: now, Valid: true},
			}
			if err := trans.Model(&FacilitiesPrograms{}).
				Where("program_id = ? and facility_id = ?", updateProgram.ID, facId).
				Updates(updates).Error; err != nil {
				return nil, newDeleteDBError(err, "facilities_programs")
			}
		}
	}

	return logEntries, nil
}

func updateProgramTypes(trans *gorm.DB, updateProgram *Program, programTypes []ProgramType) ([]ChangeLogEntry, error) {
	var (
		currentPrgTypes []ProgramType
		logEntries      []ChangeLogEntry
		progType        ProgType
	)
	if err := trans.Where("program_id = ?", updateProgram.ID).Find(&currentPrgTypes).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_types")
	}

	currentPrgTypesMap := make(map[ProgType]struct{}, len(currentPrgTypes)) //added light weight map used for lookup
	for _, pt := range currentPrgTypes {
		currentPrgTypesMap[pt.ProgramType] = struct{}{}
	}

	updatePrgTypesMap := make(map[ProgType]struct{}, len(programTypes)) //added light weight map used for lookup
	for _, pt := range programTypes {
		progType = pt.ProgramType
		updatePrgTypesMap[progType] = struct{}{}
		if _, ok := currentPrgTypesMap[progType]; !ok { //type doesn't exist create it
			logEntries = append(logEntries, *NewChangeLogEntry("programs", "program_type", nil, StringPtr(string(progType)), updateProgram.ID, DerefUint(updateProgram.UpdateUserID)))
			if err := trans.Create(&ProgramType{
				ProgramID:   updateProgram.ID,
				ProgramType: progType,
			}).Error; err != nil {
				return nil, newCreateDBError(err, "program_types")
			}
		}
	}

	for prgType := range currentPrgTypesMap {
		if _, ok := updatePrgTypesMap[prgType]; !ok {
			logEntries = append(logEntries, *NewChangeLogEntry("programs", "program_type", StringPtr(string(prgType)), nil, updateProgram.ID, DerefUint(updateProgram.UpdateUserID)))
			if err := trans.Where("program_id = ? and program_type = ?", updateProgram.ID, prgType).Delete(&ProgramType{}).Error; err != nil {
				return nil, newDeleteDBError(err, "program_types")
			}
		}
	}
	return logEntries, nil
}

func updateProgramCreditTypes(trans *gorm.DB, updateProgram *Program, programCreditTypes []ProgramCreditType) ([]ChangeLogEntry, error) {
	var (
		currentPrgCreditTypes []ProgramCreditType
		logEntries            []ChangeLogEntry
		progCreditType        CreditType
	)
	if err := trans.Where("program_id = ?", updateProgram.ID).Find(&currentPrgCreditTypes).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_credit_types")
	}

	currentPrgCreditTypesMap := make(map[CreditType]struct{}, len(currentPrgCreditTypes)) //added a light weight map used for lookup
	for _, ct := range currentPrgCreditTypes {
		currentPrgCreditTypesMap[ct.CreditType] = struct{}{}
	}

	updatePrgCreditTypesMap := make(map[CreditType]struct{}, len(programCreditTypes)) //added a light weight map used for lookup
	for _, pt := range programCreditTypes {
		progCreditType = pt.CreditType
		updatePrgCreditTypesMap[progCreditType] = struct{}{}
		if _, ok := currentPrgCreditTypesMap[progCreditType]; !ok { //type doesn't exist create it
			logEntries = append(logEntries, *NewChangeLogEntry("programs", "credit_type", nil, StringPtr(string(progCreditType)), updateProgram.ID, DerefUint(updateProgram.UpdateUserID)))
			if err := trans.Create(&ProgramCreditType{
				ProgramID:  updateProgram.ID,
				CreditType: progCreditType,
			}).Error; err != nil {
				return nil, newCreateDBError(err, "program_credit_types")
			}
		}
	}

	for creditType := range currentPrgCreditTypesMap {
		if _, ok := updatePrgCreditTypesMap[creditType]; !ok { //type was removed delete it
			logEntries = append(logEntries, *NewChangeLogEntry("programs", "credit_type", StringPtr(string(creditType)), nil, updateProgram.ID, DerefUint(updateProgram.UpdateUserID)))
			if err := trans.Where("program_id = ? and credit_type = ?", updateProgram.ID, creditType).Delete(&ProgramCreditType{}).Error; err != nil {
				return nil, newDeleteDBError(err, "program_credit_types")
			}
		}
	}
	return logEntries, nil
}

func (db *Store) UpdateProgramStatus(programUpdate map[string]any, id uint) ([]string, bool, error) {
	var (
		facilities []string
		logEntry   *ChangeLogEntry
	)
	updatePrg, err := db.GetProgramByID(int(id))
	if err != nil {
		return nil, false, err
	}
	if programUpdate["archived_at"] != nil {
		err := db.Model(&ProgramClassCohort{}).
			Joins("JOIN facilities ON facilities.id = program_class_cohorts.facility_id").
			Where("program_class_cohorts.program_id = ? AND program_class_cohorts.status IN ? AND program_class_cohorts.archived_at IS NULL", id,
				[]ClassStatus{Active, Scheduled}).
			Distinct().
			Pluck("facilities.name", &facilities).Error

		if err != nil {
			return nil, false, newGetRecordsDBError(err, "program_classes / facilities")
		}
		if len(facilities) > 0 {
			return facilities, false, nil
		}
		logEntry = NewChangeLogEntry("programs", "archived_at", nil, StringPtr(programUpdate["archived_at"].(string)), updatePrg.ID, programUpdate["update_user_id"].(uint))
	} else { //is possible that the old value is the same as the new value here when reactivating
		oldValue := strconv.FormatBool(updatePrg.IsActive)
		newValue := strconv.FormatBool(programUpdate["is_active"].(bool))
		logEntry = NewChangeLogEntry("programs", "is_active", StringPtr(oldValue), StringPtr(newValue), updatePrg.ID, programUpdate["update_user_id"].(uint))
	}
	trans := db.Begin() //create transaction
	if trans.Error != nil {
		return nil, false, newDBError(trans.Error, "unable to start the database transaction")
	}
	if err := trans.Model(&Program{}).
		Where("id = ?", id).
		Updates(programUpdate).Error; err != nil {
		return nil, false, newUpdateDBError(err, "program status")
	}
	if err := trans.Create(&logEntry).Error; err != nil {
		trans.Rollback()
		return nil, false, newCreateDBError(err, "change_log_entries")
	}
	if err := trans.Commit().Error; err != nil { //commit transaction
		return nil, false, newDBError(err, "unable to commit the database transaction")
	}
	return facilities, true, nil
}

func (db *Store) DeleteProgram(id int) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("cohort_id IN (SELECT id FROM program_class_cohorts WHERE program_id = ?)", id).
			Delete(&ProgramClassEvent{}).Error; err != nil {
			return newDeleteDBError(err, "program_class_events")
		}
		if err := tx.Exec(`DELETE FROM change_log_entries
			WHERE (table_name = ? AND parent_ref_id = ?)
			   OR (table_name = ?
			       AND parent_ref_id IN (SELECT id FROM program_class_cohorts WHERE program_id = ?))
			   OR (table_name = ?
			       AND parent_ref_id IN (SELECT id FROM program_classes WHERE program_id = ?))`,
			TableNamePrograms, id,
			TableNameCohort, id,
			TableNameClass, id).Error; err != nil {
			return newDeleteDBError(err, "change_log_entries")
		}
		if err := tx.Exec(`DELETE FROM program_classes_history
			WHERE (table_name = ?
			       AND parent_ref_id IN (SELECT id FROM program_class_cohorts WHERE program_id = ?))
			   OR (table_name = ?
			       AND parent_ref_id IN (SELECT id FROM program_classes WHERE program_id = ?))
			   OR (table_name = ? AND parent_ref_id = ?)`,
			TableNameCohort, id,
			TableNameClass, id,
			TableNamePrograms, id).Error; err != nil {
			return newDeleteDBError(err, "program_classes_history")
		}
		if err := tx.Delete(&Program{}, id).Error; err != nil {
			return newDeleteDBError(err, "programs")
		}
		return nil
	})
}

func (db *Store) GetProgramsFacilitiesStats(args *QueryContext, timeFilter int) (ProgramsFacilitiesStats, error) {
	var programsFacilitiesStats ProgramsFacilitiesStats

	var totals struct {
		TotalPrograms    int64 `json:"total_programs"`
		TotalEnrollments int64 `json:"total_enrollments"`
	}

	if err := db.WithContext(args.Ctx).Model(&Program{}).
		Select("COUNT(*) AS total_programs").
		Where("is_active = true AND archived_at IS NULL").
		Scan(&totals.TotalPrograms).Error; err != nil {
		return programsFacilitiesStats, newGetRecordsDBError(err, "total active programs")
	}

	if err := db.WithContext(args.Ctx).Model(&ProgramClassEnrollment{}).
		Select("COUNT(CASE WHEN enrolled_at IS NOT NULL THEN 1 END) AS total_enrollments").
		Scan(&totals.TotalEnrollments).Error; err != nil {
		return programsFacilitiesStats, newGetRecordsDBError(err, "total enrollments")
	}

	var rateStats struct {
		AvgActiveProgramsPerFacility float64 `json:"avg_active_programs_per_facility"`
		AttendanceRate               float64 `json:"attendance_rate"`
		CompletionRate               float64 `json:"completion_rate"`
	}

	completionTimeFilter := ""
	attendanceTimeFilter := ""
	if timeFilter > 0 {
		cutoffDate := time.Now().AddDate(0, 0, -timeFilter).Format("2006-01-02")
		completionTimeFilter = fmt.Sprintf("AND (pce.enrollment_ended_at >= '%s' OR pcev.created_at >= '%s')", cutoffDate, cutoffDate)
		attendanceTimeFilter = completionTimeFilter
	}

	partialAttendanceSQL := buildPartialAttendanceSQL(db.Name(), "pcea")
	var avgResult struct {
		TotalAssociations int64 `json:"total_associations"`
		TotalFacilities   int64 `json:"total_facilities"`
	}

	if err := db.WithContext(args.Ctx).Raw(`
		SELECT
			COUNT(DISTINCT fp.id) as total_associations,
			COUNT(DISTINCT f.id) as total_facilities
		FROM facilities f
		LEFT JOIN facilities_programs fp ON fp.facility_id = f.id
		LEFT JOIN programs p ON p.id = fp.program_id AND p.is_active = true AND p.archived_at IS NULL
		LEFT JOIN program_class_cohorts pc ON pc.program_id = p.id AND pc.facility_id = f.id AND pc.status = 'Active'
		LEFT JOIN program_class_enrollments pce ON pce.cohort_id = pc.id
		WHERE pc.id IS NOT NULL AND pce.id IS NOT NULL AND pce.enrolled_at IS NOT NULL AND (pce.enrollment_ended_at IS NULL OR pce.enrollment_ended_at > CURRENT_TIMESTAMP)
	`).Scan(&avgResult).Error; err != nil {
		return programsFacilitiesStats, newGetRecordsDBError(err, "avg active programs per facility")
	}

	if avgResult.TotalFacilities > 0 {
		rateStats.AvgActiveProgramsPerFacility = float64(avgResult.TotalAssociations) / float64(avgResult.TotalFacilities)
	} else {
		rateStats.AvgActiveProgramsPerFacility = 0
	}

	query := fmt.Sprintf(`
		SELECT
			COALESCE(COUNT(CASE WHEN pce.enrollment_status = 'Completed' AND pce.enrolled_at IS NOT NULL %s THEN 1 END) * 100.0 /
				NULLIF(COUNT(CASE WHEN pce.enrolled_at IS NOT NULL THEN 1 END), 0), 0) AS completion_rate,
			COALESCE(SUM(
				CASE
					WHEN pcea.attendance_status = 'present' %s THEN 1
					WHEN pcea.attendance_status = 'partial' %s THEN %s
					ELSE 0
					END
				) * 100.0 /
					NULLIF(COUNT(CASE WHEN pcea.attendance_status IS NOT NULL AND pcea.attendance_status != '' %s THEN 1 END), 0), 0) AS attendance_rate
		FROM program_class_enrollments pce
		LEFT JOIN program_class_cohorts pc ON pc.id = pce.cohort_id
		LEFT JOIN program_class_events pcev ON pcev.cohort_id = pc.id
		LEFT JOIN program_class_event_attendance pcea ON pcea.event_id = pcev.id AND pcea.user_id = pce.user_id AND pcea.deleted_at IS NULL
		WHERE pce.enrolled_at IS NOT NULL %s
	`, completionTimeFilter, attendanceTimeFilter, attendanceTimeFilter, partialAttendanceSQL, attendanceTimeFilter, completionTimeFilter)

	var completionAttendanceRates struct {
		AttendanceRate float64 `json:"attendance_rate"`
		CompletionRate float64 `json:"completion_rate"`
	}

	if err := db.WithContext(args.Ctx).Raw(query).Scan(&completionAttendanceRates).Error; err != nil {
		return programsFacilitiesStats, newGetRecordsDBError(err, "completion and attendance rates")
	}

	rateStats.AttendanceRate = completionAttendanceRates.AttendanceRate
	rateStats.CompletionRate = completionAttendanceRates.CompletionRate

	programsFacilitiesStats.TotalPrograms = &totals.TotalPrograms
	programsFacilitiesStats.TotalEnrollments = &totals.TotalEnrollments
	programsFacilitiesStats.AvgActiveProgramsPerFacility = &rateStats.AvgActiveProgramsPerFacility
	programsFacilitiesStats.AttendanceRate = &rateStats.AttendanceRate
	programsFacilitiesStats.CompletionRate = &rateStats.CompletionRate

	return programsFacilitiesStats, nil
}

func (db *Store) GetProgramsFacilityStats(args *QueryContext, timeFilter int) (ProgramsFacilitiesStats, error) {
	var programsFacilityStats ProgramsFacilitiesStats

	var totals struct {
		TotalPrograms    int64 `json:"total_programs"`
		TotalEnrollments int64 `json:"total_enrollments"`
	}

	if err := db.WithContext(args.Ctx).Raw(`
		SELECT COUNT(DISTINCT p.id) AS total_programs
		FROM programs p
		JOIN facilities_programs fp ON fp.program_id = p.id
		JOIN program_class_cohorts pc ON pc.program_id = p.id AND pc.facility_id = fp.facility_id AND pc.status = 'Active'
		JOIN program_class_enrollments pce ON pce.cohort_id = pc.id
		WHERE fp.facility_id = ? AND p.is_active = true AND p.archived_at IS NULL
	`, args.FacilityID).Scan(&totals.TotalPrograms).Error; err != nil {
		return programsFacilityStats, newGetRecordsDBError(err, "total programs for facility")
	}

	if err := db.WithContext(args.Ctx).
		Model(&ProgramClassEnrollment{}).
		Select("COUNT(CASE WHEN program_class_enrollments.enrolled_at IS NOT NULL THEN 1 END) AS total_enrollments").
		Joins("LEFT JOIN program_class_cohorts pc ON pc.id = program_class_enrollments.cohort_id").
		Where("pc.facility_id = ?", args.FacilityID).
		Scan(&totals.TotalEnrollments).Error; err != nil {
		return programsFacilityStats, newGetRecordsDBError(err, "total enrollments for facility")
	}

	var rateStats struct {
		AvgActiveProgramsPerFacility float64 `json:"avg_active_programs_per_facility"`
		AttendanceRate               float64 `json:"attendance_rate"`
		CompletionRate               float64 `json:"completion_rate"`
	}

	completionTimeFilter := ""
	attendanceTimeFilter := ""
	if timeFilter > 0 {
		cutoffDate := time.Now().AddDate(0, 0, -timeFilter).Format("2006-01-02")
		completionTimeFilter = fmt.Sprintf("AND (pce.enrollment_ended_at >= '%s' OR pcev.created_at >= '%s')", cutoffDate, cutoffDate)
		attendanceTimeFilter = completionTimeFilter
	}

	partialAttendanceSQL := buildPartialAttendanceSQL(db.Name(), "pcea")
	query := fmt.Sprintf(`
		SELECT
			COALESCE(COUNT(CASE WHEN pce.enrollment_status = 'Completed' AND pce.enrolled_at IS NOT NULL %s THEN 1 END) * 100.0 /
				NULLIF(COUNT(CASE WHEN pce.enrolled_at IS NOT NULL THEN 1 END), 0), 0) AS completion_rate,
			COALESCE(SUM(
				CASE
					WHEN pcea.attendance_status = 'present' %s THEN 1
					WHEN pcea.attendance_status = 'partial' %s THEN %s
					ELSE 0
					END
				) * 100.0 /
					NULLIF(COUNT(CASE WHEN pcea.attendance_status IS NOT NULL AND pcea.attendance_status != '' %s THEN 1 END), 0), 0) AS attendance_rate
		FROM program_class_enrollments pce
		LEFT JOIN program_class_cohorts pc ON pc.id = pce.cohort_id
		LEFT JOIN program_class_events pcev ON pcev.cohort_id = pc.id
		LEFT JOIN program_class_event_attendance pcea ON pcea.event_id = pcev.id AND pcea.user_id = pce.user_id AND pcea.deleted_at IS NULL
		WHERE pc.facility_id = ? AND pce.enrolled_at IS NOT NULL %s
	`, completionTimeFilter, attendanceTimeFilter, attendanceTimeFilter, partialAttendanceSQL, attendanceTimeFilter, completionTimeFilter)

	if err := db.WithContext(args.Ctx).Raw(query, args.FacilityID).Scan(&rateStats).Error; err != nil {
		return programsFacilityStats, newGetRecordsDBError(err, "facility completion and attendance rates")
	}

	rateStats.AvgActiveProgramsPerFacility = float64(totals.TotalPrograms)

	programsFacilityStats.TotalPrograms = &totals.TotalPrograms
	programsFacilityStats.TotalEnrollments = &totals.TotalEnrollments
	programsFacilityStats.AvgActiveProgramsPerFacility = &rateStats.AvgActiveProgramsPerFacility
	programsFacilityStats.AttendanceRate = &rateStats.AttendanceRate
	programsFacilityStats.CompletionRate = &rateStats.CompletionRate

	return programsFacilityStats, nil
}

func ParseOperatorAndValue(input string) (string, float64) {
	ops := []string{">=", "<=", "!=", "=", ">", "<"}
	for _, op := range ops {
		if strings.HasPrefix(input, op) {
			numString := strings.TrimSpace(input[len(op):])
			num, err := strconv.ParseFloat(numString, 64)
			if err == nil {
				return op, num
			}
			break
		}
	}
	return "", 0
}

// applyNumericFilter mirrors applyRateFilter's fail-closed behavior: an
// unparseable operator/value (e.g. a malformed `filter_mr.total_classes`
// query param) leaves tx untouched instead of building a WHERE clause with a
// missing operator, which Postgres rejects as a syntax error.
func applyNumericFilter(tx *gorm.DB, column string, val string) *gorm.DB {
	op, num := ParseOperatorAndValue(val)
	if op == "" {
		return tx
	}
	return tx.Where(fmt.Sprintf("%s %s ?", column, op), num)
}

func applyRateFilter(tx *gorm.DB, rateExpr string, val string) *gorm.DB {
	const floatTolerance = 0.005
	op, num := ParseOperatorAndValue(val)
	switch op {
	case "!=":
		return tx.Where("ABS("+rateExpr+" - ?) > ?", num, floatTolerance)
	case "=":
		return tx.Where(rateExpr+" BETWEEN ? AND ?", num-floatTolerance, num+floatTolerance)
	case ">=":
		return tx.Where(rateExpr+" >= ?", num-floatTolerance)
	case "<=":
		return tx.Where(rateExpr+" <= ?", num+floatTolerance)
	case ">":
		return tx.Where(rateExpr+" > ?", num)
	case "<":
		return tx.Where(rateExpr+" < ?", num)
	}
	return tx
}

func (db *Store) GetProgramsOverviewTable(args *QueryContext, timeFilter int, includeArchived bool, adminRole users.Role, filters map[string]string) ([]ProgramsOverviewTable, error) {
	var programsTable []ProgramsOverviewTable

	baseQuery := db.WithContext(args.Ctx).Model(&Program{})

	facilityScoped := adminRole == users.FacilityAdmin
	var selectFields string
	if facilityScoped {
		selectFields = `
			programs.id AS program_id,
			programs.name AS program_name,
			programs.description AS description,
			programs.archived_at AS archived_at,
			mr.total_enrollments AS total_enrollments,
			mr.total_active_enrollments AS total_active_enrollments,
			mr.total_classes AS total_classes,
			mr.total_active_classes AS total_active_classes,
			mr.total_cancelled_classes AS total_cancelled_classes,
			mr.total_capacity AS total_capacity,
			mr.total_filled_seats AS total_filled_seats,
			time_filtered_rates.completion_rate AS completion_rate,
			time_filtered_rates.rate_eligible_completions AS rate_eligible_completions,
			time_filtered_rates.rate_eligible_enrollments AS rate_eligible_enrollments,
			time_filtered_rates.attendance_rate AS attendance_rate,
			pt.program_types AS program_types,
			pct.credit_types AS credit_types,
			programs.funding_type AS funding_type,
			programs.is_active AS status
		`
	} else {
		selectFields = `
			programs.id AS program_id,
			programs.name AS program_name,
			programs.description AS description,
			programs.archived_at AS archived_at,
			mr.total_active_facilities AS total_active_facilities,
			mr.total_enrollments AS total_enrollments,
			mr.total_active_enrollments AS total_active_enrollments,
			mr.total_classes AS total_classes,
			mr.total_active_classes AS total_active_classes,
			mr.total_cancelled_classes AS total_cancelled_classes,
			mr.total_capacity AS total_capacity,
			mr.total_filled_seats AS total_filled_seats,
			time_filtered_rates.completion_rate AS completion_rate,
			time_filtered_rates.rate_eligible_completions AS rate_eligible_completions,
			time_filtered_rates.rate_eligible_enrollments AS rate_eligible_enrollments,
			time_filtered_rates.attendance_rate AS attendance_rate,
			pt.program_types AS program_types,
			pct.credit_types AS credit_types,
			programs.funding_type AS funding_type,
			programs.is_active AS status
		`
	}

	var currentMetricsSubquery string
	if facilityScoped {
		currentMetricsSubquery = fmt.Sprintf(`
			LEFT JOIN (
				SELECT
					p.id as program_id,
					COUNT(DISTINCT pce.id) AS total_enrollments,
					COUNT(DISTINCT CASE WHEN pce.enrollment_status = 'Enrolled' AND pce.enrolled_at IS NOT NULL AND (pce.enrollment_ended_at IS NULL OR pce.enrollment_ended_at > CURRENT_TIMESTAMP) THEN pce.id END) AS total_active_enrollments,
					COUNT(DISTINCT CASE WHEN pce.enrollment_status = 'Enrolled' AND pce.enrolled_at IS NOT NULL AND (pce.enrollment_ended_at IS NULL OR pce.enrollment_ended_at > CURRENT_TIMESTAMP) AND pc.status = 'Active' THEN pce.id END) AS total_filled_seats,
					COUNT(DISTINCT CASE WHEN pc.status != 'Cancelled' THEN pc.id END) AS total_classes,
					COUNT(DISTINCT CASE WHEN pc.status = 'Active' THEN pc.id END) AS total_active_classes,
					COUNT(DISTINCT CASE WHEN pc.status = 'Cancelled' THEN pc.id END) AS total_cancelled_classes,
					class_stats.total_capacity
				FROM programs p
				JOIN facilities_programs fp ON fp.program_id = p.id
				LEFT JOIN program_class_cohorts pc ON pc.program_id = p.id AND pc.facility_id = %d
				LEFT JOIN program_class_enrollments pce ON pce.cohort_id = pc.id
				LEFT JOIN (
					SELECT pc2.program_id, COALESCE(SUM(CASE WHEN pc2.status = 'Active' THEN pc2.capacity ELSE 0 END), 0) AS total_capacity
					FROM program_class_cohorts pc2
					GROUP BY pc2.program_id
				) AS class_stats ON class_stats.program_id = p.id
				WHERE fp.facility_id = %d
				GROUP BY p.id, class_stats.total_capacity
			) AS mr ON mr.program_id = programs.id
		`, args.FacilityID, args.FacilityID)
	} else {
		currentMetricsSubquery = `
			LEFT JOIN (
				SELECT
					p.id as program_id,
					COUNT(DISTINCT CASE WHEN p.is_active = true AND p.archived_at IS NULL THEN fp.facility_id END) AS total_active_facilities,
					COUNT(DISTINCT pce.id) AS total_enrollments,
					COUNT(DISTINCT CASE WHEN pce.enrollment_status = 'Enrolled' AND pce.enrolled_at IS NOT NULL AND (pce.enrollment_ended_at IS NULL OR pce.enrollment_ended_at > CURRENT_TIMESTAMP) THEN pce.id END) AS total_active_enrollments,
					COUNT(DISTINCT CASE WHEN pce.enrollment_status = 'Enrolled' AND pce.enrolled_at IS NOT NULL AND (pce.enrollment_ended_at IS NULL OR pce.enrollment_ended_at > CURRENT_TIMESTAMP) AND pc.status = 'Active' THEN pce.id END) AS total_filled_seats,
					COUNT(DISTINCT CASE WHEN pc.status != 'Cancelled' THEN pc.id END) AS total_classes,
					COUNT(DISTINCT CASE WHEN pc.status = 'Active' THEN pc.id END) AS total_active_classes,
					COUNT(DISTINCT CASE WHEN pc.status = 'Cancelled' THEN pc.id END) AS total_cancelled_classes,
					class_stats.total_capacity
				FROM programs p
				LEFT JOIN facilities_programs fp ON fp.program_id = p.id
				LEFT JOIN program_class_cohorts pc ON pc.program_id = p.id
				LEFT JOIN program_class_enrollments pce ON pce.cohort_id = pc.id
				LEFT JOIN (
					SELECT pc2.program_id, COALESCE(SUM(CASE WHEN pc2.status = 'Active' THEN pc2.capacity ELSE 0 END), 0) AS total_capacity
					FROM program_class_cohorts pc2
					GROUP BY pc2.program_id
				) AS class_stats ON class_stats.program_id = p.id
				GROUP BY p.id, class_stats.total_capacity
			) AS mr ON mr.program_id = programs.id
		`
	}

	timeFilterCondition := ""
	if timeFilter > 0 {
		cutoffDate := time.Now().AddDate(0, 0, -timeFilter)
		timeFilterCondition = fmt.Sprintf(`
			AND (pce.enrollment_ended_at >= '%s' OR pcev.created_at >= '%s')
		`, cutoffDate.Format("2006-01-02"), cutoffDate.Format("2006-01-02"))
	}

	var facilityFilterForRates string
	if facilityScoped {
		facilityFilterForRates = fmt.Sprintf("AND pc.facility_id = %d", args.FacilityID)
	}

	partialAttendanceSQL := buildPartialAttendanceSQL(db.Name(), "pcea")

	// DISTINCT on pce.id: the pcev/pcea joins below fan out one enrollment into many
	// rows (one per event/attendance record), so a plain COUNT double-counts enrollments
	// with more recorded attendance and skews the rate.
	// Both halves are also projected as their own columns so a client aggregating
	// across programs can weight by the real denominator instead of guessing one.
	rateCompletionsSQL := `COUNT(DISTINCT CASE WHEN pce.enrollment_status = 'Completed' AND pce.enrollment_ended_at IS NOT NULL AND ` + rateEligibleClassStatusSQL + ` ` + timeFilterCondition + ` THEN pce.id END)`
	rateEligibleSQL := `COUNT(DISTINCT CASE WHEN pce.enrollment_status IN (` + completedOutcomeStatusSQLList() + `) AND pce.enrollment_ended_at IS NOT NULL AND ` + rateEligibleClassStatusSQL + ` THEN pce.id END)`

	timeFilteredRatesSubquery := `
		LEFT JOIN (
			SELECT
				p.id as program_id,
				` + rateCompletionsSQL + ` * 100.0 / NULLIF(` + rateEligibleSQL + `, 0) AS completion_rate,
				` + rateCompletionsSQL + ` AS rate_eligible_completions,
				` + rateEligibleSQL + ` AS rate_eligible_enrollments,
				SUM(
					CASE
						WHEN pcea.attendance_status = 'present' ` + timeFilterCondition + ` THEN 1
						WHEN pcea.attendance_status = 'partial' ` + timeFilterCondition + ` THEN ` + partialAttendanceSQL + `
						ELSE 0
					END
				) * 100.0 /
					NULLIF(COUNT(CASE WHEN pcea.attendance_status IS NOT NULL AND pcea.attendance_status != '' ` + timeFilterCondition + ` THEN 1 END), 0) AS attendance_rate
			FROM programs p
			LEFT JOIN program_class_cohorts pc ON pc.program_id = p.id ` + facilityFilterForRates + `
			LEFT JOIN program_class_enrollments pce ON pce.cohort_id = pc.id
			LEFT JOIN program_class_events pcev ON pcev.cohort_id = pc.id
			LEFT JOIN program_class_event_attendance pcea ON pcea.event_id = pcev.id AND pcea.user_id = pce.user_id AND pcea.deleted_at IS NULL
			WHERE 1=1 ` + timeFilterCondition + `
			GROUP BY p.id
		) AS time_filtered_rates ON time_filtered_rates.program_id = programs.id
	`

	tx := baseQuery.Select(selectFields).
		Joins(currentMetricsSubquery).
		Joins(timeFilteredRatesSubquery).
		Joins(`LEFT JOIN (
			SELECT program_id, STRING_AGG(DISTINCT program_type::text, ',') AS program_types
			FROM program_types
			GROUP BY program_id
		) AS pt ON pt.program_id = programs.id`).
		Joins(`LEFT JOIN (
			SELECT program_id, STRING_AGG(DISTINCT credit_type::text, ',') AS credit_types
			FROM program_credit_types
			GROUP BY program_id
		) AS pct ON pct.program_id = programs.id`)

	if facilityScoped {
		tx = tx.Joins("JOIN facilities_programs fp ON fp.program_id = programs.id").Where("fp.facility_id = ?", args.FacilityID)
	}

	if !includeArchived {
		tx = tx.Where("programs.archived_at IS NULL")
	}

	if len(args.Tags) > 0 {
		tx = tx.Where("programs.id IN (SELECT program_id FROM program_types WHERE program_type IN (?))", args.Tags)
	}

	for col, val := range filters {
		switch col {
		case "programs.name":
			tx = tx.Where("programs.name ILIKE ?", "%"+val+"%")
		case "mr.total_enrollments":
			tx = applyNumericFilter(tx, "mr.total_enrollments", val)
		case "mr.total_active_enrollments":
			tx = applyNumericFilter(tx, "mr.total_active_enrollments", val)
		case "mr.total_classes":
			tx = applyNumericFilter(tx, "mr.total_classes", val)
		case "mr.total_active_classes":
			tx = applyNumericFilter(tx, "mr.total_active_classes", val)
		case "capacity_utilization":
			tx = applyRateFilter(tx, "(mr.total_filled_seats * 100.0 / NULLIF(mr.total_capacity, 0))", val)
		case "mr.total_active_facilities":
			if !facilityScoped {
				tx = applyNumericFilter(tx, "mr.total_active_facilities", val)
			}
		case "completion_rate":
			tx = applyRateFilter(tx, "time_filtered_rates.completion_rate", val)
		case "attendance_rate":
			tx = applyRateFilter(tx, "time_filtered_rates.attendance_rate", val)
		case "pt.program_types":
			tx = tx.Where("pt.program_types ~* ?", val)
		case "pct.credit_types":
			tx = tx.Where("pct.credit_types ~* ?", val)
		case "programs.funding_type":
			tx = tx.Where("programs.funding_type IN (?)", strings.Split(val, "|"))
		case "programs.is_active":
			statuses := strings.Split(val, "|")
			conditions := make([]string, 0, len(statuses))
			for _, status := range statuses {
				switch status {
				case "Available":
					conditions = append(conditions, "programs.is_active = true AND programs.archived_at IS NULL")
				case "Archived":
					conditions = append(conditions, "programs.archived_at IS NOT NULL")
				case "Inactive":
					conditions = append(conditions, "programs.is_active = false AND programs.archived_at IS NULL")
				}
			}
			if len(conditions) > 0 {
				whereStatement := "(" + strings.Join(conditions, " OR ") + ")"
				tx = tx.Where(whereStatement)
			}
		case "facility_id":
			tx = tx.Joins("JOIN facilities_programs fp ON fp.program_id = programs.id").Where("fp.facility_id = ?", val)
		}
	}

	if args.Search != "" {
		tx = tx.Where("LOWER(programs.name) LIKE ? OR LOWER(programs.description) LIKE ?", args.SearchQuery(), args.SearchQuery())
	}

	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "programs table count")
	}

	if args.OrderBy != "" && args.Order != "" {
		tx = tx.Order(args.OrderBy + " " + args.Order)
	} else {
		tx = tx.Order(args.OrderClause("programs.created_at desc"))
	}

	if !args.All {
		tx = tx.Limit(args.PerPage).Offset(args.CalcOffset())
	}
	if err := tx.Scan(&programsTable).Error; err != nil {
		return programsTable, newGetRecordsDBError(err, "programs table")
	}

	return programsTable, nil
}

func (db *Store) GetProgramCreatedAtAndBy(id int, args *QueryContext) (ActivityHistoryResponse, error) {
	var programDetails ActivityHistoryResponse
	if err := db.WithContext(args.Ctx).Table("programs p").
		Select("p.created_at, u.username as admin_username").
		Joins("join users u on u.id = p.create_user_id").
		Where("p.id = ?", id).
		Scan(&programDetails).Error; err != nil {
		return programDetails, newNotFoundDBError(err, "programs")
	}
	return programDetails, nil
}

func (db *Store) GetProgramsCSVData(args *QueryContext) ([]ProgramCSVData, error) {
	var programCSVData []ProgramCSVData
	statuses := CompletedOutcomeStatuses

	partialAttendanceSQL := buildPartialAttendanceSQL(db.Name(), "pca")
	tx := db.WithContext(args.Ctx).Table("programs p").
		Joins("JOIN program_class_cohorts pc ON pc.program_id = p.id").
		Joins("JOIN program_classes cl ON cl.id = pc.class_id").
		Joins("JOIN program_class_enrollments pe ON pe.cohort_id = pc.id").
		Joins("JOIN users u on u.id = pe.user_id").
		Joins("JOIN facilities f ON f.id = u.facility_id").
		Joins("LEFT JOIN class_completions c on c.cohort_id = pc.id and c.user_id = u.id").
		Joins("LEFT JOIN program_class_events pce on pce.cohort_id = pc.id").
		Joins("LEFT JOIN users iu ON iu.id = pce.instructor_id").
		Joins("LEFT JOIN program_class_event_attendance pca ON pca.event_id = pce.id AND pca.user_id = u.id AND pca.deleted_at IS NULL").
		Select(`
				f.name AS facility_name,
				p.name AS program_name,
				cl.name AS class_name,
				COALESCE(iu.name_first || ' ' || iu.name_last, '') AS instructor_name,
				u.id as unlock_ed_id,
				u.doc_id AS resident_id,
				u.name_last AS name_last,
				u.name_first AS name_first,
				pe.created_at as enrollment_date,
				COALESCE(
				CASE
					WHEN c.id IS NOT NULL THEN c.created_at
					WHEN pe.enrollment_status IN (?) THEN pe.updated_at
					END,
					pc.end_dt
	 		) as end_date,
				pe.enrollment_status AS end_status,
				CASE
					WHEN COUNT(CASE WHEN pca.attendance_status IS NOT NULL AND pca.attendance_status != '' THEN 1 END) = 0 THEN 0
					ELSE
						COALESCE(
							SUM(
								CASE
									WHEN pca.attendance_status = 'present' THEN 1
									WHEN pca.attendance_status = 'partial' THEN `+partialAttendanceSQL+`
									ELSE 0
								END
							) * 100.0 /
							NULLIF(COUNT(CASE WHEN pca.attendance_status IS NOT NULL AND pca.attendance_status != '' THEN 1 END), 0),
							0
						)
				END AS attendance_percentage`, statuses).
		Where(`
			(
				c.id IS NOT NULL
				OR pe.enrollment_status IN (?)
			)
		`, statuses).
		Group("u.id, u.doc_id, f.name, p.name, cl.name, iu.name_first, iu.name_last, pe.created_at, pe.enrollment_status, pe.updated_at, c.id, c.created_at, pc.end_dt").
		Order("f.name ASC, p.name ASC, cl.name ASC, end_date DESC")

	if !args.All {
		tx = tx.Where("f.id = ?", args.FacilityID)
	}

	if err := tx.Scan(&programCSVData).Error; err != nil {
		return nil, newGetRecordsDBError(err, "programs CSV data")
	}
	return programCSVData, nil
}

func buildPartialAttendanceSQL(dialect, alias string) string {
	minutesCast := "COALESCE(%s.minutes_attended, %s.scheduled_minutes, 0)::numeric"
	if dialect == "sqlite" {
		minutesCast = "CAST(COALESCE(%s.minutes_attended, %s.scheduled_minutes, 0) AS REAL)"
	}
	minutesCast = fmt.Sprintf(minutesCast, alias, alias)
	ratioExpr := fmt.Sprintf("%s / NULLIF(COALESCE(%s.scheduled_minutes, %s.minutes_attended, 0), 0)", minutesCast, alias, alias)
	partialAttendanceSQL := fmt.Sprintf("CASE WHEN %s < 1 THEN %s ELSE 1 END", ratioExpr, ratioExpr)
	return partialAttendanceSQL
}

// completedOutcomeStatusSQLList renders CompletedOutcomeStatuses as a quoted,
// comma-separated SQL list for inlining into a raw `IN (...)` clause.
func completedOutcomeStatusSQLList() string {
	quoted := make([]string, len(CompletedOutcomeStatuses))
	for i, status := range CompletedOutcomeStatuses {
		quoted[i] = "'" + string(status) + "'"
	}
	return strings.Join(quoted, ", ")
}

// rateEligibleClassStatusSQL excludes classes that haven't started (Scheduled)
// or never ran (Cancelled) from completion/attendance rate calculations, so a
// class with no real data doesn't produce a misleading rate. Shared by every
// completion-rate query so the eligibility rule can't drift between them.
const rateEligibleClassStatusSQL = "pc.status NOT IN ('Scheduled', 'Cancelled')"
