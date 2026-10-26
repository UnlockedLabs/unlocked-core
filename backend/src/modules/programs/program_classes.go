package programs

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type ClassStatus string

const (
	Scheduled ClassStatus = "Scheduled"
	Active    ClassStatus = "Active"
	Cancelled ClassStatus = "Cancelled"
	Completed ClassStatus = "Completed"
	Paused    ClassStatus = "Paused"
)

type ProgramClassCohort struct {
	DatabaseFields
	ClassID          uint        `json:"program_class_id" gorm:"not null"`
	ClassName        string      `json:"class_name" gorm:"->"`
	ProgramID        uint        `json:"program_id" gorm:"not null"`
	FacilityID       uint        `json:"facility_id" gorm:"not null"`
	Capacity         int64       `json:"capacity" gorm:"not null"`
	InstructorID     *uint       `json:"instructor_id" gorm:"-"`
	UpdateInstructor bool        `json:"-" gorm:"-"`
	Description      string      `json:"description" gorm:"not null" validate:"required,max=255"`
	ArchivedAt       *time.Time  `json:"archived_at"`
	StartDt          time.Time   `gorm:"type:date" json:"start_dt"`
	EndDt            *time.Time  `gorm:"type:date" json:"end_dt"`
	Status           ClassStatus `json:"status" gorm:"type:class_status" validate:"required"`
	CreditHours      *int64      `json:"credit_hours"`
	Enrolled         int64       `json:"enrolled" gorm:"-"`
	Completed        int64       `json:"completed" gorm:"-"`
	IsCanvas         bool        `json:"is_canvas" gorm:"-"`
	Source           string      `json:"source,omitempty" gorm:"-"`
	Loading          bool        `json:"loading,omitempty" gorm:"-"`
	CanvasTimezone   string      `json:"canvas_timezone,omitempty" gorm:"-"`

	Class        *ProgramClass            `json:"program_class" gorm:"foreignKey:ClassID;references:ID"`
	Program      *Program                 `json:"program" gorm:"foreignKey:ProgramID;references:ID"`
	Enrollments  []ProgramClassEnrollment `json:"enrollments" gorm:"foreignKey:CohortID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Facility     *Facility                `json:"facility" gorm:"foreignKey:FacilityID;references:ID"`
	Events       []ProgramClassEvent      `json:"events" gorm:"foreignKey:CohortID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	FacilityProg *FacilitiesPrograms      `json:"facility_program" gorm:"foreignKey:ProgramID;references:ProgramID"`
}

func (ProgramClassCohort) TableName() string { return "program_class_cohorts" }

type ProgramClass struct {
	DatabaseFields
	ProgramID   uint       `json:"program_id" gorm:"not null"`
	FacilityID  uint       `json:"facility_id" gorm:"not null"`
	Name        string     `json:"name" gorm:"size:255" validate:"required,max=255"`
	Description *string    `json:"description"`
	CreditHours *int64     `json:"credit_hours"`
	ArchivedAt  *time.Time `json:"archived_at"`

	// Rollups across cohorts. Computed, never stored -- see program_classes_store.go.
	CohortCount      int64 `json:"cohort_count" gorm:"-"`
	ActiveCohorts    int64 `json:"active_cohorts" gorm:"-"`
	ScheduledCohorts int64 `json:"scheduled_cohorts" gorm:"-"`
	CompletedCohorts int64 `json:"completed_cohorts" gorm:"-"`
	Enrolled         int64 `json:"enrolled" gorm:"-"`
	Capacity         int64 `json:"capacity" gorm:"-"`
	Completed        int64 `json:"completed" gorm:"-"`

	Program     *Program                 `json:"program" gorm:"foreignKey:ProgramID;references:ID"`
	Facility    *Facility                `json:"facility" gorm:"foreignKey:FacilityID;references:ID"`
	Cohorts     []ProgramClassCohort     `json:"cohorts" gorm:"foreignKey:ClassID;references:ID"`
	CreditTypes []ProgramClassCreditType `json:"credit_types" gorm:"foreignKey:ClassID;references:ID"`
}

func (ProgramClass) TableName() string { return "program_classes" }

type ProgramClassCreditType struct {
	ClassID    uint       `json:"program_class_id" gorm:"primaryKey;not null"`
	CreditType CreditType `json:"credit_type" gorm:"primaryKey;type:credit_type;not null"`
}

func (ProgramClassCreditType) TableName() string { return "program_class_credit_types" }

// CreditHoursOrDefault resolves the two-level credit_hours: the cohort's own value wins,
// falling back to its class's default. Mirrors COALESCE(cohort, class) in SQL.
// Returns nil only when neither level has a value.
func (c *ProgramClassCohort) CreditHoursOrDefault() *int64 {
	if c.CreditHours != nil {
		return c.CreditHours
	}
	if c.Class != nil {
		return c.Class.CreditHours
	}
	return nil
}

// CreditTypesOrInherited applies the empty-means-inherit rule. Pass the program's credit
// types; they are returned unchanged when the class defines no override of its own.
func (pc *ProgramClass) CreditTypesOrInherited(programTypes []CreditType) []CreditType {
	if len(pc.CreditTypes) == 0 {
		return programTypes
	}
	out := make([]CreditType, 0, len(pc.CreditTypes))
	for _, ct := range pc.CreditTypes {
		out = append(out, ct.CreditType)
	}
	return out
}

type TodaysScheduleItem struct {
	CohortID       uint   `json:"cohort_id"`
	ClassName      string `json:"class_name"`
	InstructorName string `json:"instructor_name"`
	FacilityID     uint   `json:"facility_id"`
	FacilityName   string `json:"facility_name"`
	EventID        uint   `json:"event_id"`
	Date           string `json:"date"`
	StartTime      string `json:"start_time"`
	Room           string `json:"room"`
	HasAttendance  bool   `json:"has_attendance"`
	EnrolledCount  int    `json:"enrolled_count"`
}

func (c *ProgramClassCohort) BeforeCreate(tx *gorm.DB) error {
	if err := c.DatabaseFields.BeforeCreate(tx); err != nil {
		return err
	}
	c.UpdateUserID = nil

	// A cohort with no parent class is meaningless -- class_id is NOT NULL and the
	// certificate lives on the class. So when no class is given, create one from this
	// cohort's own attributes.
	//
	// This is deliberate, not a convenience: today's UI has a single "create a class"
	// action that creates what we now call a cohort, and it stays that way until the
	// Phase 4 frontend splits the two. Auto-creating the parent means that action keeps
	// working AND produces correctly-shaped two-tier data (one class, one cohort) instead
	// of an orphan. Callers that want to attach to an existing class set ClassID.
	if c.ClassID == 0 {
		// The cohort has no name to lend, so the parent takes the PROGRAM's name. That is
		// only a fallback: every UI path picks or creates a class explicitly and sets
		// ClassID, so this fires for legacy/API callers and test fixtures. Naming it after
		// the program keeps such a class recognisable rather than blank.
		var programName string
		if err := tx.Session(&gorm.Session{NewDB: true}).
			Model(&Program{}).
			Select("name").
			Where("id = ?", c.ProgramID).
			Scan(&programName).Error; err != nil {
			return fmt.Errorf("resolving program %d name for auto-created class: %w", c.ProgramID, err)
		}
		if programName == "" {
			return fmt.Errorf("cannot auto-create a class for cohort: program %d not found", c.ProgramID)
		}
		parent := ProgramClass{
			ProgramID:   c.ProgramID,
			FacilityID:  c.FacilityID,
			Name:        programName,
			Description: NilIfBlank(c.Description),
			CreditHours: c.CreditHours,
			ArchivedAt:  c.ArchivedAt,
			DatabaseFields: DatabaseFields{
				CreateUserID: c.CreateUserID,
			},
		}
		// NewDB is required, not optional: tx's Statement is bound to the COHORT schema, and
		// reusing it to create a different model makes GORM index the parent's fields
		// against the cohort's schema and panic in reflect.
		if err := tx.Session(&gorm.Session{NewDB: true, SkipHooks: true}).Create(&parent).Error; err != nil {
			return fmt.Errorf("creating parent class %q for cohort: %w", programName, err)
		}
		c.ClassID = parent.ID
	}
	return nil
}

type ProgramClassEnrollment struct {
	DatabaseFields
	CohortID          uint                    `json:"cohort_id" gorm:"column:cohort_id;not null"`
	ClassID           uint                    `json:"program_class_id" gorm:"not null"`
	UserID            uint                    `json:"user_id" gorm:"not null"`
	EnrollmentStatus  ProgramEnrollmentStatus `json:"enrollment_status" gorm:"size:255" validate:"max=255"`
	ChangeReason      *string                 `json:"change_reason" gorm:"size:255" validate:"max=255"`
	EnrolledAt        *time.Time              `json:"enrolled_at"`
	EnrollmentEndedAt *time.Time              `json:"enrollment_ended_at"`

	User   *User               `json:"user" gorm:"foreignKey:UserID;references:ID"`
	Cohort *ProgramClassCohort `json:"cohort" gorm:"foreignKey:CohortID;references:ID"`
	Class  *ProgramClass       `json:"program_class" gorm:"foreignKey:ClassID;references:ID"`
}

func (ProgramClassEnrollment) TableName() string { return "program_class_enrollments" }

// BeforeCreate hook that runs to set enrolled_at when enrolling in an Active COHORT.
// Status lives on the cohort, so this reads the cohort -- not the class.
func (e *ProgramClassEnrollment) BeforeCreate(tx *gorm.DB) (err error) {
	if err := e.DatabaseFields.BeforeCreate(tx); err != nil {
		return err
	}

	// ClassID is a denormalization of the cohort's parent, and the column is NOT NULL with
	// no database default. Resolving it here rather than at each call site means no caller
	// can forget it -- and, more importantly, means no caller can pass a COHORT id as
	// ClassID, which would compile cleanly and silently corrupt the class rollups.
	if e.ClassID == 0 && e.CohortID != 0 {
		var classID uint
		if err := tx.Model(&ProgramClassCohort{}).
			Select("class_id").
			Where("id = ?", e.CohortID).
			Scan(&classID).Error; err != nil {
			return err
		}
		if classID == 0 {
			return fmt.Errorf("cohort %d has no parent class; cannot enroll", e.CohortID)
		}
		e.ClassID = classID
	}

	// allow calling code to override
	if e.EnrolledAt != nil {
		return nil
	}

	var classStatus ClassStatus
	if e.CohortID != 0 {
		if err := tx.Model(&ProgramClassCohort{}).
			Select("status").
			Where("id = ?", e.CohortID).
			Scan(&classStatus).Error; err != nil {
			return err
		}
	}

	if classStatus == Active && e.EnrollmentStatus == Enrolled {
		tx.Statement.SetColumn("enrolled_at", time.Now().UTC())
		return nil
	}

	return nil
}

// BeforeUpdate hook that runs to set enrolled_at if enrolling in an Active class or enrollment_ended_at if entering a terminal state while class is Active|Paused.
func (e *ProgramClassEnrollment) BeforeUpdate(tx *gorm.DB) (err error) {
	if !tx.Statement.Changed("enrollment_status") {
		return nil
	}

	var newEnrollmentStatus ProgramEnrollmentStatus
	if m, ok := tx.Statement.Dest.(map[string]interface{}); ok {
		if v, ok := m["enrollment_status"]; ok {
			switch val := v.(type) {
			case string:
				newEnrollmentStatus = ProgramEnrollmentStatus(val)
			case ProgramEnrollmentStatus:
				newEnrollmentStatus = val
			default:
				return fmt.Errorf("unknown type %T", val)
			}
		}
	}

	// ⚠️  'cohort_id' is a runtime contract the compiler cannot see. Callers that update
	//     an enrollment's status must Set("cohort_id", ...) -- see withCohortID in
	//     class_enrollments_store.go. Absent it, cohortID stays 0 and the status
	//     lookup is skipped, so enrolled_at silently never gets set.
	var cohortID int
	if v, ok := tx.Get("cohort_id"); ok {
		switch val := v.(type) {
		case int:
			cohortID = val
		case uint:
			cohortID = int(val)
		default:
			return fmt.Errorf("unexpected type for 'cohort_id': %T", val)
		}
	}

	var classStatus ClassStatus
	if cohortID != 0 {
		if err := tx.Model(&ProgramClassCohort{}).
			Select("status").
			Where("id = ?", cohortID).
			Scan(&classStatus).Error; err != nil {
			return err
		}
	}

	// This likely gets hit when we introduce "Waitlist" as a status
	if newEnrollmentStatus == Enrolled && classStatus == Active && !tx.Statement.Changed("enrolled_at") {
		tx.Statement.SetColumn("enrolled_at", time.Now().UTC())
		// ? do we need to worry about updating fields to the same value (enrolled -> enrolled)?
	}

	// Clear enrollment_ended_at when status becomes Enrolled (reactivation)
	if newEnrollmentStatus == Enrolled && (classStatus == Active || classStatus == Paused) {
		tx.Statement.SetColumn("enrollment_ended_at", nil)
	}

	if IsTerminalEnrollment(newEnrollmentStatus) {
		tx.Statement.SetColumn("enrollment_ended_at", time.Now().UTC())
	}

	return nil
}

func IsTerminalEnrollment(s ProgramEnrollmentStatus) bool {
	return s == EnrollmentCancelled || s == EnrollmentCompleted ||
		strings.HasPrefix(string(s), "Incomplete:")
}

type ProgramClassDetail struct {
	ProgramClassCohort
	FacilityName          string   `json:"facility_name"`
	Enrolled              int      `json:"enrolled"`
	HistoricalEnrollments int      `json:"historical_enrollments"`
	Schedule              string   `json:"schedule"`
	Room                  string   `json:"room"`
	AttendanceRate        *float64 `json:"attendance_rate"`
	Completed             int      `json:"completed"`

	// Rate-eligible counts apply the same enrollment_ended_at predicate as the
	// program-level completion rate in programs_store.go, so a client
	// dividing these cannot contradict the server's own number. Completed and
	// HistoricalEnrollments above are raw counts with other consumers and are
	// deliberately left untightened.
	RateEligibleCompletions int `json:"rate_eligible_completions"`
	RateEligibleEnrollments int `json:"rate_eligible_enrollments"`
}

type ProgramEnrollmentStatus string

const (
	Enrolled                             ProgramEnrollmentStatus = "Enrolled"
	EnrollmentCancelled                  ProgramEnrollmentStatus = "Cancelled"
	EnrollmentCompleted                  ProgramEnrollmentStatus = "Completed"
	EnrollmentIncompleteWithdrawn        ProgramEnrollmentStatus = "Incomplete: Withdrawn"
	EnrollmentIncompleteDropped          ProgramEnrollmentStatus = "Incomplete: Dropped"
	EnrollmentIncompleteFailedToComplete ProgramEnrollmentStatus = "Incomplete: Failed to Complete"
	EnrollmentIncompleteTransfered       ProgramEnrollmentStatus = "Incomplete: Transfered"
	EnrollmentIncompleteSegregated       ProgramEnrollmentStatus = "Incomplete: Segregated"
)

// AllEnrollmentStatuses enumerates every value of ProgramEnrollmentStatus, so
// derived status sets (see CompletedOutcomeStatuses) have one place to stay
// in sync with the type's definition instead of hand-copying a subset of it.
var AllEnrollmentStatuses = []ProgramEnrollmentStatus{
	Enrolled,
	EnrollmentCancelled,
	EnrollmentCompleted,
	EnrollmentIncompleteWithdrawn,
	EnrollmentIncompleteDropped,
	EnrollmentIncompleteFailedToComplete,
	EnrollmentIncompleteTransfered,
	EnrollmentIncompleteSegregated,
}

// CompletedOutcomeStatuses are the terminal statuses that represent a real
// completion opportunity, and so are the correct denominator for every
// completion rate and historical-enrollment count. A Cancelled enrollment is
// terminal (IsTerminalEnrollment says so, because it still stamps
// enrollment_ended_at) but never had an opportunity to complete, so counting
// it in a denominator deflates the rate. Derived from IsTerminalEnrollment so
// the two definitions cannot drift apart as they previously did.
var CompletedOutcomeStatuses = func() []ProgramEnrollmentStatus {
	statuses := make([]ProgramEnrollmentStatus, 0, len(AllEnrollmentStatuses))
	for _, s := range AllEnrollmentStatuses {
		if IsTerminalEnrollment(s) && s != EnrollmentCancelled {
			statuses = append(statuses, s)
		}
	}
	return statuses
}()

type ClassCompletion struct {
	DatabaseFields
	UserID        uint      `json:"user_id" gorm:"not null"`
	CohortID      *uint     `json:"cohort_id" gorm:"column:cohort_id"`
	ClassID       *uint     `json:"program_class_id"`
	FacilityName  string    `json:"facility_name" gorm:"not null"`
	CreditType    string    `json:"credit_type" gorm:"not null"`
	AdminEmail    string    `json:"admin_email" gorm:"not null"`
	ProgramOwner  string    `json:"program_owner" gorm:"not null"`
	ProgramName   string    `json:"program_name" gorm:"not null"`
	ProgramID     *uint     `json:"program_id"`
	ClassName     string    `json:"class_name" gorm:"column:class_name"`
	CohortStartDt time.Time `json:"class_start_dt" gorm:"column:cohort_start_dt"`
	EnrolledOnDt  time.Time `json:"enrolled_on_dt"`

	User   *User               `json:"user" gorm:"foreignKey:UserID;references:ID"`
	Cohort *ProgramClassCohort `json:"cohort" gorm:"foreignKey:CohortID;references:ID"`
	Class  *ProgramClass       `json:"program_class" gorm:"foreignKey:ClassID;references:ID"`
}

func (ClassCompletion) TableName() string { return "class_completions" }

type ProgramClassesHistory struct {
	ID           uint            `json:"id"`
	ParentRefID  uint            `json:"parent_ref_id"`
	NameTable    string          `json:"table_name" gorm:"column:table_name;size:255"` // cant use TableName because used below
	BeforeUpdate json.RawMessage `json:"before_update" gorm:"type:json"`
	AfterUpdate  json.RawMessage `json:"after_update" gorm:"type:json"`
	CreatedAt    time.Time       `json:"created_at"`
}

func (ProgramClassesHistory) TableName() string { return "program_classes_history" }

const (
	TableNamePrograms = "programs"
	TableNameClass    = "program_classes"
	TableNameCohort   = "program_class_cohorts"
)

func (pc *ProgramClassCohort) CannotUpdateClassWithEnrollment(enrollmentStatus ProgramEnrollmentStatus) bool {
	isScheduledAndNotCancelled := pc.Status == Scheduled && enrollmentStatus != EnrollmentCancelled
	isActiveAndCancelled := pc.Status == Active && enrollmentStatus == EnrollmentCancelled
	return pc.CannotUpdateClass() || isScheduledAndNotCancelled || isActiveAndCancelled
}

func (pc *ProgramClassCohort) CannotUpdateClass() bool {
	return pc.Status == Completed || pc.Status == Cancelled || pc.ArchivedAt != nil
}

func (pc *ProgramClassCohort) GetProgramOwnerOrEmpty() string {
	facilityProg := pc.FacilityProg
	if facilityProg != nil {
		return FormatNullableString(facilityProg.ProgramOwner)
	}
	return ""
}

type ConflictDetail struct {
	UserID           uint      `json:"user_id"`
	UserName         string    `json:"user_name"`
	ConflictingClass string    `json:"conflicting_class"`
	ConflictStart    time.Time `json:"conflict_start"`
	ConflictEnd      time.Time `json:"conflict_end"`
	ConflictDays     []string  `json:"conflict_days"`
}

type BulkCancelSessionsRequest struct {
	InstructorID int    `json:"instructorId" validate:"gte=0"`
	StartDate    string `json:"startDate" validate:"required"`
	EndDate      string `json:"endDate" validate:"required"`
	Reason       string `json:"reason" validate:"required,min=10,max=255"`
}

type BulkCancelSessionsResponse struct {
	Success               bool            `json:"success"`
	SessionCount          int             `json:"sessionCount"`
	ClassCount            int             `json:"classCount"`
	StudentCount          int             `json:"studentCount"`
	AlreadyCancelledCount int             `json:"alreadyCancelledCount"`
	Message               string          `json:"message,omitempty"`
	Classes               []AffectedClass `json:"classes"`
}

type AffectedClass struct {
	ClassID           int    `json:"classId"`
	ClassName         string `json:"className"`
	UpcomingSessions  int    `json:"upcomingSessions"`
	CancelledSessions int    `json:"cancelledSessions"`
	StudentCount      int    `json:"studentCount"`
}

type Instructor struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	NameFirst string `json:"name_first"`
	NameLast  string `json:"name_last"`
	Email     string `json:"email"`
}

type InstructorClassData struct {
	ID                int      `json:"id"`
	Name              string   `json:"name"`
	SessionCount      int      `json:"sessionCount"`
	EnrolledCount     int      `json:"enrolledCount"`
	UpcomingSessions  int      `json:"upcomingSessions"`
	CancelledSessions int      `json:"cancelledSessions"`
	StartTime         string   `json:"startTime"`
	Duration          string   `json:"duration"`
	Room              string   `json:"room"`
	SessionDates      []string `json:"sessionDates" gorm:"-"`
}
