package programs

import (
	"fmt"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs/api"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-contracts/users"
	"gorm.io/gorm"
)

// The rows of core's tables that Programs reads and writes, and the Programs
// types that lived in core's shared model files. Their JSON is what core sent
// before the move (EN-194), so responses don't change.

// DatabaseFields are the columns most tables share. The create and update user
// come from the identity.Claims in the query's context.
type DatabaseFields struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	CreateUserID *uint `json:"create_user_id"`
	UpdateUserID *uint `json:"update_user_id"`
}

func (m *DatabaseFields) BeforeCreate(tx *gorm.DB) error {
	if c, ok := identity.FromContext(tx.Statement.Context); ok {
		m.CreateUserID = &c.UserID
	}
	return nil
}

func (m *DatabaseFields) BeforeUpdate(tx *gorm.DB) error {
	if c, ok := identity.FromContext(tx.Statement.Context); ok {
		m.UpdateUserID = &c.UserID
		tx.Statement.SetColumn("update_user_id", c.UserID)
	}
	return nil
}

// User is a row of core's users table.
type User struct {
	DatabaseFields
	Username      string     `gorm:"size:255;not null;unique" json:"username"`
	NameFirst     string     `gorm:"size:255;not null" json:"name_first"`
	Email         string     `gorm:"size:255;not null;unique" json:"email"`
	NameLast      string     `gorm:"size:255;not null" json:"name_last"`
	Role          users.Role `gorm:"size:64;default:student" json:"role"`
	KratosID      *string    `gorm:"size:255" json:"kratos_id"`
	FacilityID    uint       `json:"facility_id"`
	DocID         *string    `json:"doc_id" gorm:"column:doc_id;size:32"`
	DeactivatedAt *time.Time `json:"deactivated_at,omitempty"`

	Facility *Facility `json:"facility,omitempty" gorm:"foreignKey:FacilityID"`
}

func (User) TableName() string { return "users" }

// Facility is a row of core's facilities table.
type Facility struct {
	DatabaseFields
	Name     string `gorm:"size:255;not null" json:"name"`
	Timezone string `gorm:"size:255;not null" json:"timezone"`

	// Programs is always null, as it was in core.
	Programs []Program `json:"programs" gorm:"-"`
	// LoginActivity is core's, which Programs never loads, so it is always
	// null.
	LoginActivity []struct{} `json:"login_activity" gorm:"-"`
	Rooms         []Room     `json:"rooms,omitempty" gorm:"foreignKey:FacilityID;references:ID"`
}

func (Facility) TableName() string { return "facilities" }

// UserAccountHistory is a row of core's user_account_history table. Programs
// adds one when it records attendance.
type UserAccountHistory struct {
	UserID                  uint                  `json:"user_id" gorm:"primaryKey"`
	AdminID                 *uint                 `json:"admin_id"`
	Action                  ActivityHistoryAction `json:"action" gorm:"size:255;primaryKey"`
	ProgramClassesHistoryID *uint                 `json:"program_classes_history_id"`
	FacilityID              *uint                 `json:"facility_id"`
	CreatedAt               time.Time             `json:"created_at" gorm:"primaryKey"`
	AttendanceStatus        Attendance            `json:"attendance_status" gorm:"size:27"`
	ClassName               *string               `json:"class_name" gorm:"size:255"`
	SessionDate             *time.Time            `json:"session_date" gorm:"type:date"`
}

func (UserAccountHistory) TableName() string { return "user_account_history" }

// ActivityHistoryAction is a user_account_history action.
type ActivityHistoryAction string

const (
	ProgClassHistory   ActivityHistoryAction = "progclass_history"
	AttendanceRecorded ActivityHistoryAction = "attendance_recorded"
)

func NewUserAccountHistory(userID uint, action ActivityHistoryAction, adminID *uint, programClassesHistoryID *uint, facilityID *uint) *UserAccountHistory {
	return &UserAccountHistory{
		UserID:                  userID,
		AdminID:                 adminID,
		Action:                  action,
		ProgramClassesHistoryID: programClassesHistoryID,
		FacilityID:              facilityID,
		CreatedAt:               time.Now(),
	}
}

// ActivityHistoryResponse is one entry of a program's or class's history.
type ActivityHistoryResponse struct {
	Action                  ActivityHistoryAction `json:"action"`
	CreatedAt               *time.Time            `json:"created_at"`
	FieldName               *string               `json:"field_name"`
	NewValue                *string               `json:"new_value"`
	OldValue                *string               `json:"old_value"`
	UserID                  *uint                 `json:"user_id"`
	UserUsername            *string               `json:"user_username"`
	AdminUsername           *string               `json:"admin_username"`
	FacilityName            *string               `json:"facility_name"`
	ProgramClassesHistoryID *uint                 `json:"program_classes_history_id"`
	AttendanceStatus        *string               `json:"attendance_status"`
	ClassName               *string               `json:"class_name"`
	SessionDate             *time.Time            `json:"session_date"`
}

// The types Programs answers core with are the api's, so the store returns
// them as they are.
type (
	ResidentTransferProgramConflicts = api.ResidentTransferProgramConflicts
	ProgramEngagementOverview        = api.ProgramEngagementOverview
	ProgramCompletionRank            = api.ProgramCompletionRank
	SecondProgramEnrollmentRow       = api.SecondProgramEnrollmentRow
	ProgramCompletionMatrixCell      = api.ProgramCompletionMatrixCell
	ProgramLoadBucket                = api.ProgramLoadBucket
	ProgramLoadFacilityRow           = api.ProgramLoadFacilityRow
	ProgramLoadDistribution          = api.ProgramLoadDistribution
	ProgramTypeEnrollment            = api.ProgramTypeEnrollment
	ClassDashboardMetrics            = api.ClassDashboardMetrics
	FacilityHealthSummary            = api.FacilityHealthSummary
)

// Classes is zero for class deletes; populated for programs to count classes whose own children block.
// Events and History are informational and never block: scheduled sessions are
// always created with a class, and change-log/history rows are derivative audit
// artifacts. Both are cleaned up alongside the parent on delete.
// NonDeletableStatus is set on class-only checks when the class is in a status
// other than Scheduled (Active/Completed/Cancelled/Paused), which blocks delete.
type DeleteBlockingChildren struct {
	Classes            int64  `json:"classes"`
	Enrollments        int64  `json:"enrollments"`
	Events             int64  `json:"events"`
	Completions        int64  `json:"completions"`
	AttendanceFlags    int64  `json:"attendance_flags"`
	History            int64  `json:"history"`
	NonDeletableStatus string `json:"non_deletable_status,omitempty"`
}

func (b DeleteBlockingChildren) HasAny() bool {
	return b.Classes > 0 ||
		b.Enrollments > 0 ||
		b.Completions > 0 ||
		b.AttendanceFlags > 0 ||
		b.NonDeletableStatus != ""
}

type Room struct {
	DatabaseFields
	FacilityID uint   `json:"facility_id" gorm:"not null"`
	Name       string `json:"name" gorm:"size:255;not null" validate:"required,max=255"`

	Facility *Facility `json:"facility,omitempty" gorm:"foreignKey:FacilityID;references:ID"`
}

func (Room) TableName() string {
	return "rooms"
}

type RoomBooking struct {
	RoomID     uint
	EventID    uint
	ClassID    uint
	StartTime  time.Time
	EndTime    time.Time
	IsOverride bool
}

type ConflictCheckRequest struct {
	FacilityID     uint
	RoomID         uint
	InstructorID   uint
	RecurrenceRule string
	Duration       string
	ExcludeEventID *uint
	ExcludeClassID *uint
}

const (
	ConflictTypeRoom       = "room"
	ConflictTypeInstructor = "instructor"
)

type RoomConflict struct {
	ConflictingEventID uint      `json:"conflicting_event_id"`
	ConflictingClassID uint      `json:"conflicting_class_id"`
	ClassName          string    `json:"class_name"`
	StartTime          time.Time `json:"start_time"`
	EndTime            time.Time `json:"end_time"`
	ConflictType       string    `json:"conflict_type"`
}

type ResidentProgramClassInfo struct {
	ProgramName          string                  `json:"program_name"`
	ClassName            string                  `json:"class_name"`
	Status               ClassStatus             `json:"status"`
	EnrollmentStatus     ProgramEnrollmentStatus `json:"enrollment_status"`
	StartDate            string                  `json:"start_date"`
	EndDate              string                  `json:"end_date"`
	AttendancePercentage string                  `json:"attendance_percentage"`
	CohortID             uint                    `json:"cohort_id"`
	ProgramID            uint                    `json:"program_id"`
	EnrollmentID         uint                    `json:"enrollment_id"`
	PresentAttendance    int                     `json:"present_attendance"`
	AbsentAttendance     int                     `json:"absent_attendance"`
	CreditTypes          string                  `json:"credit_types"`
	UpdatedAt            string                  `json:"updated_at"`
	ChangeReason         string                  `json:"change_reason"`
	Schedule             string                  `json:"schedule"`
	RecurrenceRule       string                  `json:"-"`
	IsCanvas             bool                    `json:"is_canvas"`
	Source               string                  `json:"source,omitempty"`
}

// calculates the attendance percentage, sets the feild AttendancePercentage and returns it's value
func (rpc *ResidentProgramClassInfo) CalculateAttendancePercentage() string {
	present := rpc.PresentAttendance
	absent := rpc.AbsentAttendance
	total := present + absent

	if total == 0 {
		rpc.AttendancePercentage = "--"
	} else {
		pct := float64(present) / float64(total) * 100
		rpc.AttendancePercentage = fmt.Sprintf("%.0f%%", pct)
	}
	return rpc.AttendancePercentage
}

type WeeklyAttendanceTrend struct {
	Week string  `json:"week"`
	Rate float64 `json:"rate"`
}

type WeeklyAttendanceRow struct {
	WeekStart    time.Time
	PresentCount float64
	TotalCount   float64
}

type ResidentAttendanceCSVData struct {
	ProgramName      string `json:"program_name" gorm:"column:program_name"`
	ClassName        string `json:"class_name" gorm:"column:class_name"`
	SessionDate      string `json:"session_date" gorm:"column:session_date"`
	AttendanceStatus string `json:"attendance_status" gorm:"column:attendance_status"`
	Note             string `json:"note" gorm:"column:note"`
}

func ResidentAttendanceToCSVFormat(data []ResidentAttendanceCSVData) ([][]string, error) {
	csvData := [][]string{{"Program Name", "Class Name", "Session Date", "Attendance Status", "Note"}}

	for _, record := range data {
		statusDisplay := ""
		switch record.AttendanceStatus {
		case "present":
			statusDisplay = "Present"
		case "absent_excused":
			statusDisplay = "Absent Excused"
		case "absent_unexcused":
			statusDisplay = "Absent Unexcused"
		default:
			statusDisplay = "No Record"
		}

		row := []string{
			record.ProgramName,
			record.ClassName,
			record.SessionDate,
			statusDisplay,
			record.Note,
		}
		csvData = append(csvData, row)
	}
	return csvData, nil
}
