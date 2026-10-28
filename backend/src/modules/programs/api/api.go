// Package api is the only part of programs that core and other feature modules
// may import: the questions Programs answers for them.
//
// The JSON names match what core already sends, so a response built from
// these types doesn't change.
package api

import (
	"context"
	"time"
)

// Programs answers core's questions about classes, enrollments and the
// Programs numbers on the dashboard.
type Programs interface {
	// EnrolledUserIDs is the users enrolled in a class right now.
	EnrolledUserIDs(ctx context.Context, classID uint) ([]uint, error)
	// EligibleUserIDs is the residents who could join a class: active
	// residents at the class's facility, which offers its program, who have no
	// enrollment in the class.
	EligibleUserIDs(ctx context.Context, classID uint) ([]uint, error)

	// TransferConflicts is the classes a resident is enrolled in at their
	// facility whose program toFacilityID doesn't offer.
	TransferConflicts(ctx context.Context, userID, toFacilityID uint) ([]ResidentTransferProgramConflicts, error)
	// EndEnrollmentsForTransfer marks the resident's current enrollments at
	// fromFacilityID as ended by a transfer. Get the api with
	// registry.Programs(tx) to make it part of the transfer's transaction.
	EndEnrollmentsForTransfer(ctx context.Context, userID, fromFacilityID uint) error
	// WithdrawEnrollments marks the user's current enrollments as withdrawn,
	// because their account was deactivated. Get the api with
	// registry.Programs(tx) to make it part of the deactivation's transaction.
	WithdrawEnrollments(ctx context.Context, userID uint) error

	// ResidentPrograms is every enrollment a resident has had, with its
	// attendance percentage, latest class start first, for the usage report.
	ResidentPrograms(ctx context.Context, userID uint) ([]ResidentProgramClassInfo, error)

	// FacilityPrograms is the Programs data each of facilityIDs holds, for
	// core's facility list and facility delete check. A facility that holds
	// none is left out.
	FacilityPrograms(ctx context.Context, facilityIDs []uint) (map[uint]FacilityPrograms, error)
	// ClassHistoryCreatedAt is when each of the program_classes_history rows
	// ids was written, for a resident's account history. A row with no time is
	// left out.
	ClassHistoryCreatedAt(ctx context.Context, ids []uint) (map[uint]time.Time, error)

	// The Programs numbers on the dashboard, for facilityID (every facility
	// when nil). EngagementOverview counts the activity between start and end,
	// or all of it when they are nil.
	EngagementOverview(ctx context.Context, start, end *time.Time, facilityID *uint) (ProgramEngagementOverview, error)
	SecondProgramEnrollment(ctx context.Context, facilityID *uint) ([]SecondProgramEnrollmentRow, error)
	CompletionMatrix(ctx context.Context, facilityID *uint) ([]ProgramCompletionMatrixCell, error)
	LoadDistribution(ctx context.Context, facilityID *uint) (ProgramLoadDistribution, error)
	EnrollmentByProgramType(ctx context.Context, facilityID *uint) ([]ProgramTypeEnrollment, error)
	ClassMetrics(ctx context.Context, facilityID *uint) (ClassDashboardMetrics, error)
	// FacilityHealth counts the missing attendance over the last days (3 when
	// days isn't positive), with class times read in the time zone tz.
	FacilityHealth(ctx context.Context, facilityID *uint, days int, tz string) ([]FacilityHealthSummary, error)
}

// ResidentTransferProgramConflicts is a class a resident would leave behind by
// transferring.
type ResidentTransferProgramConflicts struct {
	ProgramName string `json:"program_name"`
	ClassName   string `json:"class_name"`
}

// FacilityPrograms counts the Programs rows recorded at a facility.
type FacilityPrograms struct {
	// ActivePrograms and ActiveClasses are the facility list's numbers.
	ActivePrograms int64
	ActiveClasses  int64
	// Classes, Programs (the programs the facility offers) and Rooms block
	// deleting the facility.
	Classes  int64
	Programs int64
	Rooms    int64
}

// ResidentProgramClassInfo is one of a resident's enrollments.
type ResidentProgramClassInfo struct {
	ProgramName          string `json:"program_name"`
	ClassName            string `json:"class_name"`
	Status               string `json:"status"`
	EnrollmentStatus     string `json:"enrollment_status"`
	StartDate            string `json:"start_date"`
	EndDate              string `json:"end_date"`
	AttendancePercentage string `json:"attendance_percentage"`
	CohortID             uint   `json:"cohort_id"`
	ProgramID            uint   `json:"program_id"`
	EnrollmentID         uint   `json:"enrollment_id"`
	PresentAttendance    int    `json:"present_attendance"`
	AbsentAttendance     int    `json:"absent_attendance"`
	CreditTypes          string `json:"credit_types"`
	UpdatedAt            string `json:"updated_at"`
	ChangeReason         string `json:"change_reason"`
	Schedule             string `json:"schedule"`
	IsCanvas             bool   `json:"is_canvas"`
	Source               string `json:"source,omitempty"`
}

// ProgramEngagementOverview is how many residents take part in programs.
type ProgramEngagementOverview struct {
	ActiveResidents            int64                   `json:"active_residents"`
	PreviouslyEngagedResidents int64                   `json:"previously_engaged_residents"`
	NeverEngagedResidents      int64                   `json:"never_engaged_residents"`
	TotalResidents             int64                   `json:"total_residents"`
	TopPrograms                []ProgramCompletionRank `json:"top_programs"`
}

// ProgramCompletionRank is one program's completions, for the top programs.
type ProgramCompletionRank struct {
	ProgramName    string  `json:"program_name"`
	Enrolled       int64   `json:"enrolled"`
	Completed      int64   `json:"completed"`
	CompletionRate float64 `json:"completion_rate"`
}

// SecondProgramEnrollmentRow is the rate at which residents who completed a
// program enrolled in another, for one facility and program type.
type SecondProgramEnrollmentRow struct {
	FacilityName   string  `json:"facility_name"`
	ProgramType    string  `json:"program_type"`
	CompletedFirst int64   `json:"completed_first"`
	EnrolledSecond int64   `json:"enrolled_second"`
	Rate           float64 `json:"rate"`
}

// ProgramCompletionMatrixCell is one program's completions at one facility.
type ProgramCompletionMatrixCell struct {
	ProgramName              string  `json:"program_name"`
	FacilityID               uint    `json:"facility_id"`
	FacilityName             string  `json:"facility_name"`
	Enrolled                 int64   `json:"enrolled"`
	Completed                int64   `json:"completed"`
	CompletionRate           float64 `json:"completion_rate"`
	Insufficient             bool    `json:"insufficient"`
	DeltaFromFacilityAverage float64 `json:"delta_from_facility_average"`
}

// ProgramLoadDistribution is how many programs active residents are enrolled
// in, statewide and by facility.
type ProgramLoadDistribution struct {
	Statewide  []ProgramLoadBucket      `json:"statewide"`
	ByFacility []ProgramLoadFacilityRow `json:"by_facility"`
}

// ProgramLoadBucket is how many residents are enrolled in Bucket programs.
type ProgramLoadBucket struct {
	Bucket string `json:"bucket"` // "0", "1", "2", "3", "4+"
	Count  int64  `json:"count"`
}

// ProgramLoadFacilityRow is one facility's residents by how many programs
// they are enrolled in.
type ProgramLoadFacilityRow struct {
	FacilityID   uint   `json:"facility_id"`
	FacilityName string `json:"facility_name"`
	Zero         int64  `json:"zero"`
	One          int64  `json:"one"`
	Two          int64  `json:"two"`
	Three        int64  `json:"three"`
	FourPlus     int64  `json:"four_plus"`
	Total        int64  `json:"total"`
}

// ProgramTypeEnrollment is the enrollments and completions for one program
// type.
type ProgramTypeEnrollment struct {
	ProgramType string  `json:"program_type"`
	Enrolled    int64   `json:"enrolled"`
	Completed   int64   `json:"completed"`
	Rate        float64 `json:"rate"`
}

// ClassDashboardMetrics is the class numbers on the dashboard.
type ClassDashboardMetrics struct {
	ActiveClasses      int64 `json:"active_classes"`
	ScheduledClasses   int64 `json:"scheduled_classes"`
	TotalEnrollments   int64 `json:"total_enrollments"`
	TotalSeats         int64 `json:"total_seats"`
	AttendanceConcerns int64 `json:"attendance_concerns"`
}

// FacilityHealthSummary is one facility's programs, classes and attendance.
type FacilityHealthSummary struct {
	FacilityID         uint   `json:"facility_id"`
	FacilityName       string `json:"facility_name"`
	Programs           int64  `json:"programs"`
	ActiveClasses      int64  `json:"active_classes"`
	Enrollment         int64  `json:"enrollment"`
	MissingAttendance  int64  `json:"missing_attendance"`
	AttendanceConcerns int64  `json:"attendance_concerns"`
}
