package programs

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
	"gorm.io/gorm"
)

type Attendance string

const (
	Present          Attendance = "present"
	Partial          Attendance = "partial"
	Absent_Excused   Attendance = "absent_excused"
	Absent_Unexcused Attendance = "absent_unexcused"
)

func (a Attendance) HumanReadable() string {
	switch a {
	case Present:
		return "Present"
	case Absent_Excused:
		return "Absent Excused"
	case Absent_Unexcused:
		return "Absent Unexcused"
	default:
		return string(a)
	}
}

var rruleDayAbbrev = map[string]string{
	"MO": "M", "TU": "T", "WE": "W", "TH": "Th", "FR": "F", "SA": "Sa", "SU": "Su",
}

func FormatScheduleFromRRule(rruleStr string) string {
	if rruleStr == "" {
		return ""
	}
	opts, err := rrule.StrToROption(rruleStr)
	if err != nil {
		return ""
	}
	days := make([]string, 0, len(opts.Byweekday))
	for _, wd := range opts.Byweekday {
		full := strings.ToUpper(wd.String()[:2])
		if abbr, ok := rruleDayAbbrev[full]; ok {
			days = append(days, abbr)
		}
	}
	timeStr := ""
	if !opts.Dtstart.IsZero() {
		timeStr = opts.Dtstart.Format("3:04 PM")
	}
	if len(days) > 0 && timeStr != "" {
		return strings.Join(days, "/") + " " + timeStr
	}
	if len(days) > 0 {
		return strings.Join(days, "/")
	}
	return timeStr
}

/** Events are a physical time/place where a 'class' is held in a facility **/
type ProgramClassEvent struct {
	DatabaseFields
	CohortID       uint    `json:"cohort_id" gorm:"column:cohort_id;not null" validate:"required"`
	Duration       string  `json:"duration" gorm:"not null" validate:"required"`
	RecurrenceRule string  `json:"recurrence_rule" gorm:"not null" validate:"required"`
	RoomID         *uint   `json:"room_id" gorm:"not null"`
	InstructorID   *uint   `json:"instructor_id" gorm:"not null" validate:"required"`
	Reason         *string `json:"reason"`
	IsCancelled    bool    `json:"is_cancelled"`

	/* Foreign keys */
	Cohort     *ProgramClassCohort           `json:"cohort" gorm:"foreignKey:CohortID;references:ID"`
	RoomRef    *Room                         `json:"room_ref,omitempty" gorm:"foreignKey:RoomID;references:ID"`
	Instructor *User                         `json:"instructor_ref,omitempty" gorm:"foreignKey:InstructorID;references:ID"`
	Attendees  []ProgramClassEventAttendance `json:"attendees" gorm:"foreignKey:EventID;references:ID"`
	Overrides  []ProgramClassEventOverride   `json:"overrides" gorm:"foreignKey:EventID;references:ID"`
}

type DateRange struct {
	Start time.Time
	End   time.Time
	Tzone *time.Location
}

func (dt *DateRange) LoadLocation(location *time.Location) {
	dt.Start = dt.Start.In(location)
	dt.End = dt.End.In(location)
}

func (secEvent *ProgramClassEvent) BeforeCreate(tx *gorm.DB) (err error) {
	if err := secEvent.DatabaseFields.BeforeCreate(tx); err != nil {
		return err
	}
	// For events we only want to stamp creator on insert; leave update_user_id nil until first update
	secEvent.UpdateUserID = nil

	duration, parseErr := time.ParseDuration(secEvent.Duration)
	if parseErr != nil {
		err = parseErr
		return
	}
	if duration <= 0 {
		err = errors.New("duration cannot be negative")
		return
	}
	_, err = rrule.StrToRRule(secEvent.RecurrenceRule)
	if err != nil {
		return err
	}
	return
}

func (ProgramClassEvent) TableName() string { return "program_class_events" }

func (e *ProgramClassEvent) GetRRule() (*rrule.RRule, error) {
	return e.GetRRuleWithTimezone("")
}

func (e *ProgramClassEvent) GetRRuleWithTimezone(facilityTimezone string) (*rrule.RRule, error) {
	rruleStr := e.RecurrenceRule
	if strings.Contains(rruleStr, "TZID=Local") && facilityTimezone != "" {
		rruleStr = strings.Replace(rruleStr, "TZID=Local", "TZID="+facilityTimezone, 1)
	}
	rruleOptions, err := rrule.StrToROption(rruleStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse event recurrence rule: %w", err)
	}
	// NOTE: We no longer convert Dtstart to UTC here.
	// For timezone-aware RRULEs (DTSTART;TZID=...), this preserves wall-clock time across DST.
	// For legacy UTC-based RRULEs (DTSTART:...Z), the DTSTART is already in UTC.
	if !rruleOptions.Until.IsZero() {
		loc := rruleOptions.Until.Location()
		endOfDay := time.Date(
			rruleOptions.Until.Year(),
			rruleOptions.Until.Month(),
			rruleOptions.Until.Day(),
			23, 59, 59, int(time.Second-time.Nanosecond), loc)
		rruleOptions.Until = endOfDay.In(time.UTC)
	}
	rule, err := rrule.NewRRule(*rruleOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create event recurrence rule: %w", err)
	}
	return rule, nil
}

func (event *ProgramClassEvent) RRuleUntil() (time.Time, error) {
	rrule, err := event.GetRRule()
	if err != nil {
		return time.Time{}, err
	}
	return rrule.GetUntil(), nil
}

/** Overrides are used to cancel or reschedule events **/
type ProgramClassEventOverride struct {
	DatabaseFields
	EventID       uint   `json:"event_id" gorm:"not null"`
	Duration      string `json:"duration" gorm:"not null"`
	OverrideRrule string `json:"override_rrule" gorm:"not null"`
	// NOT a column -- a read-only query-time alias for the parent COHORT. Queries must
	// supply it explicitly as `cohort_id AS cohort_id`. Do not add a gorm column tag.
	CohortID              uint    `json:"cohort_id" gorm:"->" `
	IsCancelled           bool    `json:"is_cancelled"`
	RoomID                *uint   `json:"room_id"`
	Reason                *string `json:"reason"`
	LinkedOverrideEventID *uint   `json:"linked_override_event_id"`
	InstructorID          *uint   `json:"instructor_id"`

	/* Foreign keys */
	Event      *ProgramClassEvent `json:"event" gorm:"foreignKey:EventID;references:ID"`
	RoomRef    *Room              `json:"room_ref,omitempty" gorm:"foreignKey:RoomID;references:ID"`
	Instructor *User              `json:"instructor_ref,omitempty" gorm:"foreignKey:InstructorID;references:ID"`
}

func (ProgramClassEventOverride) TableName() string { return "program_class_event_overrides" }

// format argument will take a string in the format of "2006-01-02", "1/02/2006", ect
func (pce *ProgramClassEventOverride) GetFormattedOverrideDate(format string) (*string, error) {
	rRule, err := rrule.StrToRRule(pce.OverrideRrule)
	if err != nil {
		return nil, err
	}
	if len(rRule.All()) < 1 {
		return nil, errors.New("cancelled override rule does not contain a date instance")
	}
	cancelledDate := rRule.All()[0]
	cancelledDateStr := cancelledDate.Format(format)
	return StringPtr(cancelledDateStr), nil
}

func (pce *ProgramClassEventOverride) GetRescheduleSummary(timezone string) (*string, error) {
	timezoneLoc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	rRule, err := rrule.StrToRRule(pce.OverrideRrule)
	if err != nil {
		return nil, err
	}
	startDate := rRule.All()[0].In(timezoneLoc) //reschedule's will always be count=1 so we can do this
	duration, err := time.ParseDuration(pce.Duration)
	if err != nil {
		return nil, err
	}
	end := startDate.Add(duration)

	date := startDate.Format("1/02/2006")
	startTime := startDate.Format("3:04PM")
	endTime := end.Format("3:04PM")

	roomName := "TBD"
	if pce.RoomRef != nil {
		roomName = pce.RoomRef.Name
	}

	rescheduleSummary := date + " " + startTime + " - " + endTime + " (" + roomName + ")"
	return &rescheduleSummary, nil
}

/** Attendance records for Events **/
type ProgramClassEventAttendance struct {
	DatabaseFields
	EventID          uint       `json:"event_id" gorm:"not null;uniqueIndex:idx_event_user_date"`
	UserID           uint       `json:"user_id" gorm:"not null; uniqueIndex:idx_event_user_date"`
	Date             string     `json:"date" gorm:"not null; uniqueIndex:idx_event_user_date" validate:"required,datetime"`
	AttendanceStatus Attendance `json:"attendance_status" gorm:"column:attendance_status"`
	Note             *string    `json:"note" gorm:"column:note"`
	ReasonCategory   *string    `json:"reason_category" gorm:"column:reason_category"`
	CheckInAt        *string    `json:"check_in_at" gorm:"column:check_in_at"`
	CheckOutAt       *string    `json:"check_out_at" gorm:"column:check_out_at"`
	MinutesAttended  *int       `json:"minutes_attended" gorm:"column:minutes_attended"`
	ScheduledMinutes *int       `json:"scheduled_minutes" gorm:"column:scheduled_minutes"`

	/* Foreign Keys */
	Event *ProgramClassEvent `json:"event" gorm:"foreignKey:EventID;references:ID"`
	User  *User              `json:"user" gorm:"foreignKey:UserID;references:ID"`
}

func (ProgramClassEventAttendance) TableName() string { return "program_class_event_attendance" }

func (p *ProgramClassEventAttendance) BeforeCreate(tx *gorm.DB) error {
	if err := p.DatabaseFields.BeforeCreate(tx); err != nil {
		return err
	}
	return nil
}

type AttendanceRecordResponse struct {
	UserID           uint   `json:"user_id"`
	Date             string `json:"date"`
	AttendanceStatus string `json:"attendance_status"`
	Note             string `json:"note"`
	MarkedBy         string `json:"marked_by"`
}

type ClassEventInstance struct {
	EventID             uint                          `json:"event_id"`
	ClassTime           string                        `json:"class_time"` // e.g. "12:00-14:00"
	Date                string                        `json:"date"`
	AttendanceRecords   []ProgramClassEventAttendance `json:"attendance_records"`
	IsCancelled         bool                          `json:"is_cancelled"`
	IsRescheduled       bool                          `json:"is_rescheduled"`
	RescheduledToDate   string                        `json:"rescheduled_to_date,omitempty"`
	RescheduledFromDate string                        `json:"rescheduled_from_date,omitempty"`
	OverrideID          uint                          `json:"override_id,omitempty"`
}

type EnrollmentAttendance struct {
	EnrollmentID     uint    `json:"enrollment_id"`
	CohortID         uint    `json:"cohort_id"`
	EnrollmentStatus string  `json:"enrollment_status"`
	UserID           uint    `json:"user_id"`
	NameFirst        string  `json:"name_first"`
	NameLast         string  `json:"name_last"`
	DocID            string  `json:"doc_id"`
	AttendanceID     *uint   `json:"attendance_id"`
	EventID          *uint   `json:"event_id"`
	EventDate        *string `json:"date"`
	AttendanceStatus *string `json:"attendance_status"`
	Note             *string `json:"note"`
	ReasonCategory   *string `json:"reason_category"`
	CheckInAt        *string `json:"check_in_at"`
	CheckOutAt       *string `json:"check_out_at"`
	MinutesAttended  *int    `json:"minutes_attended"`
	ScheduledMinutes *int    `json:"scheduled_minutes"`
}

type AttendanceFlagType string

const (
	NoAttendance     AttendanceFlagType = "no_attendance"
	MultipleAbsences AttendanceFlagType = "multiple_absences"
)

type AttendanceFlag struct {
	NameFirst           string             `json:"name_first"`
	NameLast            string             `json:"name_last"`
	DocID               string             `json:"doc_id"`
	FlagType            AttendanceFlagType `json:"flag_type"`
	UserID              uint               `json:"user_id"`
	TotalSessions       int                `json:"total_sessions"`
	AttendedSessions    int                `json:"attended_sessions"`
	MissedSessions      int                `json:"missed_sessions"`
	AttendanceRate      int                `json:"attendance_rate"`
	ConsecutiveAbsences int                `json:"consecutive_absences"`
}

type MissingAttendanceItem struct {
	CohortID     uint   `json:"cohort_id"`
	ClassName    string `json:"class_name"`
	FacilityName string `json:"facility_name,omitempty"`
	EventID      uint   `json:"event_id"`
	Date         string `json:"date"`
	StartTime    string `json:"start_time"`
}

type MissingAttendanceClass struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	FacilityName string `json:"facility_name"`
	FacilityID   uint   `json:"facility_id"`
}

type EventDates struct {
	EventID   uint   `json:"event_id"`
	Date      string `json:"date"`
	ClassTime string `json:"class_time"` // e.g. "12:00-14:00"
}

// GetInstructorFromEvents returns the instructor ID and display name from the
// event with the highest ID, mirroring the frontend getInstructorName utility.
func GetInstructorFromEvents(events []ProgramClassEvent) (*uint, string) {
	var maxEvent *ProgramClassEvent
	for i := range events {
		if maxEvent == nil || events[i].ID > maxEvent.ID {
			maxEvent = &events[i]
		}
	}
	if maxEvent == nil {
		return nil, ""
	}
	name := ""
	if maxEvent.Instructor != nil {
		name = strings.TrimSpace(maxEvent.Instructor.NameFirst + " " + maxEvent.Instructor.NameLast)
	}
	return maxEvent.InstructorID, name
}

type FacilityProgramClassEvent struct {
	ProgramClassEvent
	Room                   string                     `json:"room" gorm:"->"` // read-only, populated from joined rooms table
	OriginalRoom           string                     `json:"original_room,omitempty" gorm:"-"`
	InstructorName         string                     `json:"instructor_name"`
	OriginalInstructorName string                     `json:"original_instructor_name,omitempty" gorm:"-"`
	ProgramID              uint                       `json:"program_id"`
	ProgramName            string                     `json:"program_name"`
	ClassName              string                     `json:"title"`
	IsCancelled            bool                       `json:"is_cancelled"`
	IsOverride             bool                       `json:"is_override"`
	EnrolledUsers          string                     `json:"enrolled_users"`
	StartTime              *time.Time                 `json:"start"`
	EndTime                *time.Time                 `json:"end"`
	Frequency              string                     `json:"frequency"`
	ClassStatus            ClassStatus                `json:"class_status"`
	OverrideID             uint                       `json:"override_id"`
	LinkedOverrideEvent    *FacilityProgramClassEvent `json:"linked_override_event" gorm:"-"`
	IsCanvasEvent          bool                       `json:"is_canvas_event" gorm:"-"`
	Source                 string                     `json:"source,omitempty" gorm:"-"`
	CanvasTimezone         string                     `json:"canvas_timezone,omitempty" gorm:"-"`
	// Loading marks a placeholder row standing in for a live provider whose
	// events are still being fetched. It carries no times and is not a real event.
	Loading bool `json:"loading,omitempty" gorm:"-"`
}
