package programs

import (
	"sort"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/users"
	"gorm.io/gorm"
)

const minEnrolleesForRanking = 5

func (db *Store) piResidentScope(args *QueryContext, facilityID *uint) *gorm.DB {
	tx := db.WithContext(args.Ctx).Model(&User{}).
		Where("role = ? AND deactivated_at IS NULL", users.Student)
	if facilityID != nil {
		tx = tx.Where("facility_id = ?", *facilityID)
	}
	return tx
}

// piEnrollmentResidentScope scopes program_class_enrollments to rows belonging
// to active, resident (Student) users, optionally within one facility. Shared
// base for the active-residents and ever-enrolled-residents counts, which
// differ only in their enrollment-status filter.
func (db *Store) piEnrollmentResidentScope(args *QueryContext, facilityID *uint) *gorm.DB {
	tx := db.WithContext(args.Ctx).Table("program_class_enrollments pce").
		Joins("JOIN users u ON u.id = pce.user_id").
		Where("u.role = ? AND u.deactivated_at IS NULL", users.Student)
	if facilityID != nil {
		tx = tx.Where("u.facility_id = ?", *facilityID)
	}
	return tx
}

func (db *Store) GetProgramEngagementOverview(args *QueryContext, start, end *time.Time, facilityID *uint) (ProgramEngagementOverview, error) {
	var overview ProgramEngagementOverview

	if err := db.piResidentScope(args, facilityID).Count(&overview.TotalResidents).Error; err != nil {
		return overview, newGetRecordsDBError(err, "users")
	}

	// "Active" means engaged with a program at any point during the selected
	// window (enrollment overlaps [start, end)), not just currently-Enrolled
	// status -- a resident who completed a program mid-range was still active
	// during that range.
	activeTx := db.piEnrollmentResidentScope(args, facilityID).
		Where("pce.enrolled_at IS NOT NULL")
	if end != nil {
		activeTx = activeTx.Where("pce.enrolled_at < ?", *end)
	}
	if start != nil {
		activeTx = activeTx.Where("pce.enrollment_ended_at IS NULL OR pce.enrollment_ended_at >= ?", *start)
	}
	if err := activeTx.Distinct("pce.user_id").Count(&overview.ActiveResidents).Error; err != nil {
		return overview, newGetRecordsDBError(err, "program_class_enrollments")
	}

	var everEnrolledCount int64
	everTx := db.piEnrollmentResidentScope(args, facilityID)
	if err := everTx.Distinct("pce.user_id").Count(&everEnrolledCount).Error; err != nil {
		return overview, newGetRecordsDBError(err, "program_class_enrollments")
	}
	overview.NeverEngagedResidents = overview.TotalResidents - everEnrolledCount
	if overview.NeverEngagedResidents < 0 {
		overview.NeverEngagedResidents = 0
	}

	// Ever-enrolled residents who aren't active in this window: completed,
	// dropped, or otherwise engaged outside the selected range.
	overview.PreviouslyEngagedResidents = everEnrolledCount - overview.ActiveResidents
	if overview.PreviouslyEngagedResidents < 0 {
		overview.PreviouslyEngagedResidents = 0
	}

	type progRow struct {
		ProgramID   uint
		ProgramName string
		Status      ProgramEnrollmentStatus
		EnrolledAt  *time.Time
	}
	rows := make([]progRow, 0)
	rowsTx := db.WithContext(args.Ctx).Table("program_class_enrollments pce").
		Select("p.id as program_id, p.name as program_name, pce.enrollment_status as status, pce.enrolled_at").
		Joins("JOIN program_class_cohorts pc ON pc.id = pce.cohort_id").
		Joins("JOIN programs p ON p.id = pc.program_id").
		Where("pce.enrolled_at IS NOT NULL")
	if facilityID != nil {
		rowsTx = rowsTx.Where("pc.facility_id = ?", *facilityID)
	}
	if start != nil && end != nil {
		rowsTx = rowsTx.Where("pce.enrolled_at >= ? AND pce.enrolled_at < ?", *start, *end)
	}
	if err := rowsTx.Scan(&rows).Error; err != nil {
		return overview, newGetRecordsDBError(err, "program_class_enrollments")
	}

	type progAgg struct {
		name      string
		enrolled  int64
		completed int64
	}
	aggByID := make(map[uint]*progAgg)
	order := make([]uint, 0)
	for _, row := range rows {
		agg, ok := aggByID[row.ProgramID]
		if !ok {
			agg = &progAgg{name: row.ProgramName}
			aggByID[row.ProgramID] = agg
			order = append(order, row.ProgramID)
		}
		agg.enrolled++
		if row.Status == EnrollmentCompleted {
			agg.completed++
		}
	}

	ranked := make([]ProgramCompletionRank, 0)
	for _, id := range order {
		agg := aggByID[id]
		if agg.enrolled < minEnrolleesForRanking {
			continue
		}
		ranked = append(ranked, ProgramCompletionRank{
			ProgramName:    agg.name,
			Enrolled:       agg.enrolled,
			Completed:      agg.completed,
			CompletionRate: float64(agg.completed) / float64(agg.enrolled) * 100,
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].CompletionRate > ranked[j].CompletionRate
	})
	overview.TopPrograms = ranked

	return overview, nil
}

func (db *Store) GetSecondProgramEnrollmentRates(args *QueryContext, facilityID *uint) ([]SecondProgramEnrollmentRow, error) {
	type enrollmentRow struct {
		UserID            uint
		FacilityID        uint
		FacilityName      string
		ProgramID         uint
		Status            ProgramEnrollmentStatus
		EnrolledAt        *time.Time
		EnrollmentEndedAt *time.Time
	}
	rows := make([]enrollmentRow, 0)
	tx := db.WithContext(args.Ctx).Table("program_class_enrollments pce").
		Select("pce.user_id, pc.facility_id, f.name as facility_name, pc.program_id, pce.enrollment_status as status, pce.enrolled_at, pce.enrollment_ended_at").
		Joins("JOIN program_class_cohorts pc ON pc.id = pce.cohort_id").
		Joins("JOIN facilities f ON f.id = pc.facility_id")
	if facilityID != nil {
		tx = tx.Where("pc.facility_id = ?", *facilityID)
	}
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_enrollments")
	}

	programTypeByID, err := db.programTypeLabelsByProgramID(args)
	if err != nil {
		return nil, err
	}

	type userEnrollment struct {
		facilityID   uint
		facilityName string
		programID    uint
		status       ProgramEnrollmentStatus
		enrolledAt   *time.Time
		endedAt      *time.Time
	}
	byUser := make(map[uint][]userEnrollment)
	for _, row := range rows {
		byUser[row.UserID] = append(byUser[row.UserID], userEnrollment{
			facilityID: row.FacilityID, facilityName: row.FacilityName, programID: row.ProgramID,
			status: row.Status, enrolledAt: row.EnrolledAt, endedAt: row.EnrollmentEndedAt,
		})
	}

	type key struct {
		facilityName string
		programType  string
	}
	completedFirst := make(map[key]int64)
	enrolledSecond := make(map[key]int64)

	for _, enrollments := range byUser {
		// Find this user's earliest completion by EnrollmentEndedAt.
		var first *userEnrollment
		for i := range enrollments {
			e := &enrollments[i]
			if e.status != EnrollmentCompleted || e.endedAt == nil {
				continue
			}
			if first == nil || e.endedAt.Before(*first.endedAt) {
				first = e
			}
		}
		if first == nil {
			continue
		}
		k := key{facilityName: first.facilityName, programType: programTypeLabelFor(programTypeByID, first.programID)}
		completedFirst[k]++

		for _, e := range enrollments {
			if e.programID == first.programID {
				continue // re-enrollment in the same program isn't a second *program*
			}
			if e.enrolledAt != nil && e.enrolledAt.After(*first.endedAt) {
				enrolledSecond[k]++
				break
			}
		}
	}

	result := make([]SecondProgramEnrollmentRow, 0, len(completedFirst))
	for k, completed := range completedFirst {
		second := enrolledSecond[k]
		result = append(result, SecondProgramEnrollmentRow{
			FacilityName:   k.facilityName,
			ProgramType:    k.programType,
			CompletedFirst: completed,
			EnrolledSecond: second,
			Rate:           float64(second) / float64(completed) * 100,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Rate > result[j].Rate
	})
	return result, nil
}

// programTypeLabel returns a deterministic single label for a program that
// may have multiple attached ProgramTypes: alphabetically first. Most
// programs have exactly one type; this is a pragmatic tie-break, not a bug.
func programTypeLabel(types []ProgramType) string {
	if len(types) == 0 {
		return "Other"
	}
	labels := make([]string, 0, len(types))
	for _, t := range types {
		labels = append(labels, t.ProgramType.HumanReadable())
	}
	sort.Strings(labels)
	return labels[0]
}

// programTypeLabelsByProgramID returns each program's deterministic type
// label (see programTypeLabel), keyed by program ID.
func (db *Store) programTypeLabelsByProgramID(args *QueryContext) (map[uint]string, error) {
	var programs []Program
	if err := db.WithContext(args.Ctx).Preload("ProgramTypes").Find(&programs).Error; err != nil {
		return nil, newGetRecordsDBError(err, "programs")
	}
	labels := make(map[uint]string, len(programs))
	for _, p := range programs {
		labels[p.ID] = programTypeLabel(p.ProgramTypes)
	}
	return labels, nil
}

// programTypeLabelFor looks up a program's type label, falling back to
// "Other" when the program isn't in the map -- e.g. a soft-deleted program
// (DeleteProgram leaves its enrollments/cohorts in place) that
// programTypeLabelsByProgramID's Find no longer returns.
func programTypeLabelFor(labels map[uint]string, programID uint) string {
	if label, ok := labels[programID]; ok && label != "" {
		return label
	}
	return "Other"
}

const minEnrolledPerMatrixCell = 3

// GetProgramCompletionMatrix returns, for every (program, cohort facility)
// pair, the lifetime enrollment/completion counts and completion rate,
// flagging cells below minEnrolledPerMatrixCell as statistically
// insufficient and reporting each sufficient cell's delta from its own
// facility's overall completion rate. Facility scoping uses the enrollment's
// cohort facility (pc.facility_id), matching GetSecondProgramEnrollmentRates,
// not the resident's home facility.
func (db *Store) GetProgramCompletionMatrix(args *QueryContext, facilityID *uint) ([]ProgramCompletionMatrixCell, error) {
	type cellRow struct {
		ProgramName  string
		FacilityID   uint
		FacilityName string
		Status       ProgramEnrollmentStatus
	}
	rows := make([]cellRow, 0)
	tx := db.WithContext(args.Ctx).Table("program_class_enrollments pce").
		Select("p.name as program_name, pc.facility_id, f.name as facility_name, pce.enrollment_status as status").
		Joins("JOIN program_class_cohorts pc ON pc.id = pce.cohort_id").
		Joins("JOIN programs p ON p.id = pc.program_id").
		Joins("JOIN facilities f ON f.id = pc.facility_id")
	if facilityID != nil {
		tx = tx.Where("pc.facility_id = ?", *facilityID)
	}
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_enrollments")
	}

	type cellKey struct {
		program  string
		facility uint
	}
	type cellAgg struct {
		facilityName string
		enrolled     int64
		completed    int64
	}
	cells := make(map[cellKey]*cellAgg)
	facilityTotals := make(map[uint]*cellAgg) // enrolled/completed totals per facility, for the average
	order := make([]cellKey, 0)

	for _, row := range rows {
		k := cellKey{program: row.ProgramName, facility: row.FacilityID}
		agg, ok := cells[k]
		if !ok {
			agg = &cellAgg{facilityName: row.FacilityName}
			cells[k] = agg
			order = append(order, k)
		}
		agg.enrolled++
		if row.Status == EnrollmentCompleted {
			agg.completed++
		}

		fAgg, ok := facilityTotals[row.FacilityID]
		if !ok {
			fAgg = &cellAgg{}
			facilityTotals[row.FacilityID] = fAgg
		}
		fAgg.enrolled++
		if row.Status == EnrollmentCompleted {
			fAgg.completed++
		}
	}

	result := make([]ProgramCompletionMatrixCell, 0, len(order))
	for _, k := range order {
		agg := cells[k]
		insufficient := agg.enrolled < minEnrolledPerMatrixCell
		rate := 0.0
		delta := 0.0
		if !insufficient {
			rate = float64(agg.completed) / float64(agg.enrolled) * 100
			fTotal := facilityTotals[k.facility]
			facilityAvg := float64(fTotal.completed) / float64(fTotal.enrolled) * 100
			delta = rate - facilityAvg
		}
		result = append(result, ProgramCompletionMatrixCell{
			ProgramName:              k.program,
			FacilityID:               k.facility,
			FacilityName:             agg.facilityName,
			Enrolled:                 agg.enrolled,
			Completed:                agg.completed,
			CompletionRate:           rate,
			Insufficient:             insufficient,
			DeltaFromFacilityAverage: delta,
		})
	}
	return result, nil
}

func loadBucketLabel(count int64) string {
	switch {
	case count <= 0:
		return "0"
	case count == 1:
		return "1"
	case count == 2:
		return "2"
	case count == 3:
		return "3"
	default:
		return "4+"
	}
}

// GetProgramLoadDistribution buckets each resident by their current active
// enrollment count (0/1/2/3/4+), a current-state snapshot with no date
// range. Facility scoping is deliberately mixed: the resident population
// (who counts as a "0", and which facility a resident's bucket is attributed
// to) uses the resident's home facility (u.facility_id), while the active
// enrollment count per resident is joined through the enrollment's cohort
// facility (pc.facility_id). This matches GetProgramEngagementOverview's
// resident-population convention and GetSecondProgramEnrollmentRates'
// cohort-facility convention for enrollment counting, applied together here.
func (db *Store) GetProgramLoadDistribution(args *QueryContext, facilityID *uint) (ProgramLoadDistribution, error) {
	var dist ProgramLoadDistribution

	type resident struct {
		ID           uint
		FacilityID   uint
		FacilityName string
	}
	residents := make([]resident, 0)
	rtx := db.WithContext(args.Ctx).Table("users u").
		Select("u.id, u.facility_id, f.name as facility_name").
		Joins("JOIN facilities f ON f.id = u.facility_id").
		Where("u.role = ? AND u.deactivated_at IS NULL", users.Student)
	if facilityID != nil {
		rtx = rtx.Where("u.facility_id = ?", *facilityID)
	}
	if err := rtx.Scan(&residents).Error; err != nil {
		return dist, newGetRecordsDBError(err, "users")
	}

	type activeCount struct {
		UserID uint
		Cnt    int64
	}
	counts := make([]activeCount, 0)
	ctx := db.WithContext(args.Ctx).Table("program_class_enrollments pce").
		Select("pce.user_id, count(*) as cnt").
		Joins("JOIN program_class_cohorts pc ON pc.id = pce.cohort_id").
		Where("pce.enrollment_status = ? AND pce.enrollment_ended_at IS NULL", Enrolled)
	if facilityID != nil {
		ctx = ctx.Where("pc.facility_id = ?", *facilityID)
	}
	if err := ctx.Group("pce.user_id").Scan(&counts).Error; err != nil {
		return dist, newGetRecordsDBError(err, "program_class_enrollments")
	}
	countByUser := make(map[uint]int64, len(counts))
	for _, c := range counts {
		countByUser[c.UserID] = c.Cnt
	}

	statewide := map[string]int64{"0": 0, "1": 0, "2": 0, "3": 0, "4+": 0}
	type facilityAgg struct {
		name                        string
		zero, one, two, three, four int64
	}
	byFacility := make(map[uint]*facilityAgg)
	facilityOrder := make([]uint, 0)

	for _, r := range residents {
		bucket := loadBucketLabel(countByUser[r.ID])
		statewide[bucket]++

		agg, ok := byFacility[r.FacilityID]
		if !ok {
			agg = &facilityAgg{name: r.FacilityName}
			byFacility[r.FacilityID] = agg
			facilityOrder = append(facilityOrder, r.FacilityID)
		}
		switch bucket {
		case "0":
			agg.zero++
		case "1":
			agg.one++
		case "2":
			agg.two++
		case "3":
			agg.three++
		default:
			agg.four++
		}
	}

	dist.Statewide = []ProgramLoadBucket{
		{Bucket: "0", Count: statewide["0"]},
		{Bucket: "1", Count: statewide["1"]},
		{Bucket: "2", Count: statewide["2"]},
		{Bucket: "3", Count: statewide["3"]},
		{Bucket: "4+", Count: statewide["4+"]},
	}
	dist.ByFacility = make([]ProgramLoadFacilityRow, 0, len(facilityOrder))
	for _, id := range facilityOrder {
		agg := byFacility[id]
		dist.ByFacility = append(dist.ByFacility, ProgramLoadFacilityRow{
			FacilityID: id, FacilityName: agg.name,
			Zero: agg.zero, One: agg.one, Two: agg.two, Three: agg.three, FourPlus: agg.four,
			Total: agg.zero + agg.one + agg.two + agg.three + agg.four,
		})
	}
	return dist, nil
}

const maxIndividualProgramTypes = 4

// GetEnrollmentByProgramType returns lifetime enrollment/completion counts
// per program type label, capped to the maxIndividualProgramTypes
// highest-enrollment types with the remainder rolled into a single "Other"
// bucket (see programTypeLabel). Facility scoping uses the enrollment's
// cohort facility (pc.facility_id), matching GetSecondProgramEnrollmentRates
// and GetProgramCompletionMatrix.
func (db *Store) GetEnrollmentByProgramType(args *QueryContext, facilityID *uint) ([]ProgramTypeEnrollment, error) {
	type enrollmentRow struct {
		ProgramID uint
		Status    ProgramEnrollmentStatus
	}
	rows := make([]enrollmentRow, 0)
	tx := db.WithContext(args.Ctx).Table("program_class_enrollments pce").
		Select("pc.program_id, pce.enrollment_status as status").
		Joins("JOIN program_class_cohorts pc ON pc.id = pce.cohort_id")
	if facilityID != nil {
		tx = tx.Where("pc.facility_id = ?", *facilityID)
	}
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, newGetRecordsDBError(err, "program_class_enrollments")
	}

	programTypeByID, err := db.programTypeLabelsByProgramID(args)
	if err != nil {
		return nil, err
	}

	type typeAgg struct {
		enrolled  int64
		completed int64
	}
	byType := make(map[string]*typeAgg)
	for _, row := range rows {
		label := programTypeLabelFor(programTypeByID, row.ProgramID)
		agg, ok := byType[label]
		if !ok {
			agg = &typeAgg{}
			byType[label] = agg
		}
		agg.enrolled++
		if row.Status == EnrollmentCompleted {
			agg.completed++
		}
	}

	all := make([]ProgramTypeEnrollment, 0, len(byType))
	for label, agg := range byType {
		all = append(all, ProgramTypeEnrollment{
			ProgramType: label,
			Enrolled:    agg.enrolled,
			Completed:   agg.completed,
			Rate:        float64(agg.completed) / float64(agg.enrolled) * 100,
		})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Enrolled != all[j].Enrolled {
			return all[i].Enrolled > all[j].Enrolled
		}
		return all[i].ProgramType < all[j].ProgramType
	})

	if len(all) <= maxIndividualProgramTypes {
		return all, nil
	}
	result := make([]ProgramTypeEnrollment, 0, maxIndividualProgramTypes+1)
	result = append(result, all[:maxIndividualProgramTypes]...)
	other := ProgramTypeEnrollment{ProgramType: "Other"}
	for _, rest := range all[maxIndividualProgramTypes:] {
		other.Enrolled += rest.Enrolled
		other.Completed += rest.Completed
	}
	other.Rate = float64(other.Completed) / float64(other.Enrolled) * 100
	result = append(result, other)
	return result, nil
}
