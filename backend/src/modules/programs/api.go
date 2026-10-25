package programs

import (
	"context"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs/api"

	"gorm.io/gorm"
)

// programsAPI answers core's questions (api.Programs). The queries moved from
// core's database package unchanged (EN-194). Its errors are the database's
// own, for core to wrap in its database errors as it did before the move.
type programsAPI struct{ store *Store }

// NewAPI is Programs' answers to core, reading db.
func NewAPI(db *gorm.DB) api.Programs {
	return programsAPI{&Store{db}}
}

func (a programsAPI) EnrolledUserIDs(ctx context.Context, classID uint) ([]uint, error) {
	ids, err := a.store.GetEnrolledResidentIDsForClass(ctx, int(classID))
	return ids, dbCause(err)
}

func (a programsAPI) EligibleUserIDs(ctx context.Context, classID uint) ([]uint, error) {
	ids, err := a.store.GetEligibleResidentIDsForClass(ctx, int(classID))
	return ids, dbCause(err)
}

func (a programsAPI) TransferConflicts(ctx context.Context, userID, toFacilityID uint) ([]api.ResidentTransferProgramConflicts, error) {
	return a.store.GetTransferProgramConflicts(ctx, userID, int(toFacilityID))
}

func (a programsAPI) EndEnrollmentsForTransfer(ctx context.Context, userID, fromFacilityID uint) error {
	return a.store.WithContext(ctx).Exec(`UPDATE program_class_enrollments AS pce SET enrollment_status = ?
		FROM program_class_cohorts pc
		WHERE pce.cohort_id = pc.id
			AND pce.user_id = ?
			AND pc.facility_id = ?
			AND pce.enrollment_status = 'Enrolled'`,
		"Incomplete: Transferred", userID, fromFacilityID).Error
}

// WithdrawEnrollments updates through the model, so the enrollment's
// BeforeUpdate hook sets enrollment_ended_at.
func (a programsAPI) WithdrawEnrollments(ctx context.Context, userID uint) error {
	return a.store.WithContext(ctx).Model(&ProgramClassEnrollment{}).
		Where("user_id = ? AND enrollment_status = ?", userID, Enrolled).
		Updates(map[string]any{
			"enrollment_status": EnrollmentIncompleteWithdrawn,
			"change_reason":     "Account deactivated",
		}).Error
}

func (a programsAPI) ResidentPrograms(ctx context.Context, userID uint) ([]api.ResidentProgramClassInfo, error) {
	// The usage report has always listed every enrollment, latest class start
	// first.
	args := QueryContext{Ctx: ctx, All: true, OrderBy: "start_dt", Order: "DESC"}
	rows, err := a.store.GetUserProgramInfo(&args, int(userID))
	if err != nil {
		return nil, dbCause(err)
	}
	out := make([]api.ResidentProgramClassInfo, len(rows))
	for i := range rows {
		rows[i].CalculateAttendancePercentage()
		out[i] = toAPIResidentProgram(rows[i])
	}
	return out, nil
}

func toAPIResidentProgram(p ResidentProgramClassInfo) api.ResidentProgramClassInfo {
	return api.ResidentProgramClassInfo{
		ProgramName:          p.ProgramName,
		ClassName:            p.ClassName,
		Status:               string(p.Status),
		EnrollmentStatus:     string(p.EnrollmentStatus),
		StartDate:            p.StartDate,
		EndDate:              p.EndDate,
		AttendancePercentage: p.AttendancePercentage,
		CohortID:             p.CohortID,
		ProgramID:            p.ProgramID,
		EnrollmentID:         p.EnrollmentID,
		PresentAttendance:    p.PresentAttendance,
		AbsentAttendance:     p.AbsentAttendance,
		CreditTypes:          p.CreditTypes,
		UpdatedAt:            p.UpdatedAt,
		ChangeReason:         p.ChangeReason,
		Schedule:             p.Schedule,
		IsCanvas:             p.IsCanvas,
		Source:               p.Source,
	}
}

func (a programsAPI) EngagementOverview(ctx context.Context, start, end *time.Time, facilityID *uint) (api.ProgramEngagementOverview, error) {
	overview, err := a.store.GetProgramEngagementOverview(&QueryContext{Ctx: ctx}, start, end, facilityID)
	return overview, dbCause(err)
}

func (a programsAPI) SecondProgramEnrollment(ctx context.Context, facilityID *uint) ([]api.SecondProgramEnrollmentRow, error) {
	rows, err := a.store.GetSecondProgramEnrollmentRates(&QueryContext{Ctx: ctx}, facilityID)
	return rows, dbCause(err)
}

func (a programsAPI) CompletionMatrix(ctx context.Context, facilityID *uint) ([]api.ProgramCompletionMatrixCell, error) {
	cells, err := a.store.GetProgramCompletionMatrix(&QueryContext{Ctx: ctx}, facilityID)
	return cells, dbCause(err)
}

func (a programsAPI) LoadDistribution(ctx context.Context, facilityID *uint) (api.ProgramLoadDistribution, error) {
	dist, err := a.store.GetProgramLoadDistribution(&QueryContext{Ctx: ctx}, facilityID)
	return dist, dbCause(err)
}

func (a programsAPI) EnrollmentByProgramType(ctx context.Context, facilityID *uint) ([]api.ProgramTypeEnrollment, error) {
	rows, err := a.store.GetEnrollmentByProgramType(&QueryContext{Ctx: ctx}, facilityID)
	return rows, dbCause(err)
}

func (a programsAPI) ClassMetrics(ctx context.Context, facilityID *uint) (api.ClassDashboardMetrics, error) {
	metrics, err := a.store.GetClassDashboardMetrics(&QueryContext{Ctx: ctx}, facilityID)
	return metrics, dbCause(err)
}

func (a programsAPI) FacilityHealth(ctx context.Context, facilityID *uint, days int, tz string) ([]api.FacilityHealthSummary, error) {
	summaries, err := NewClassesService(a.store).GetFacilityHealthSummaries(&QueryContext{Ctx: ctx, Timezone: tz}, facilityID, days)
	return summaries, dbCause(err)
}
