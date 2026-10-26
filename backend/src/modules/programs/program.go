package programs

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

type FundingType string
type ProgType string
type CreditType string

const (
	CanvasProgramIDOffset = uint(100_000_000)
	CanvasClassIDOffset   = uint(100_000_000)
	// CanvasFacilityClassIDOffset marks facility-scoped synthetic canvas class IDs.
	// Encoding: offset + facilityID×1_000_000_000 + providerID×1_000_000 + rawCourseID
	// Limits: facilityID 0–999, providerID 0–999, rawCourseID 0–999_999.
	// Larger than CanvasClassIDOffset so existing ">= CanvasClassIDOffset" guards still fire.
	CanvasFacilityClassIDOffset = uint(1_000_000_000_000)
)

const (
	//funding types
	FederalGrants FundingType = "Federal_Grants"
	StateGrants   FundingType = "State_Grants"
	NonProfitOrgs FundingType = "Nonprofit_Organizations"
	EduGrants     FundingType = "Educational_Grants"
	InmateWelfare FundingType = "Inmate_Welfare_Funds"
	Other         FundingType = "Other"
	//program types
	Educational  ProgType = "Educational"
	Vocational   ProgType = "Vocational"
	MentalHealth ProgType = "Mental_Health_Behavioral"
	Religious    ProgType = "Religious_Faith-Based"
	ReEntry      ProgType = "Re-Entry"
	Therapeutic  ProgType = "Therapeutic"
	LifeSkills   ProgType = "Life_Skills"
	//credit types
	Completion    CreditType = "Completion"
	Participation CreditType = "Participation"
	EarnedTime    CreditType = "Earned-time"
	Education     CreditType = "Education"
)

func (p ProgType) HumanReadable() string {
	switch p {
	case Educational:
		return "Educational"
	case Vocational:
		return "Vocational"
	case MentalHealth:
		return "Mental Health/Behavioral"
	case Religious:
		return "Religious/Faith-Based"
	case ReEntry:
		return "Re-Entry"
	case Therapeutic:
		return "Therapeutic"
	case LifeSkills:
		return "Life Skills"
	default:
		return string(p)
	}
}

func (f FundingType) HumanReadable() string {
	switch f {
	case FederalGrants:
		return "Federal Grants"
	case StateGrants:
		return "State Grants"
	case NonProfitOrgs:
		return "Nonprofit Organizations"
	case EduGrants:
		return "Educational Grants"
	case InmateWelfare:
		return "Inmate Welfare Funds"
	case Other:
		return "Other"
	default:
		return string(f)
	}
}

var AllFundingTypes = []FundingType{
	FederalGrants, StateGrants, NonProfitOrgs, EduGrants, InmateWelfare, Other,
}

var AllProgTypes = []ProgType{
	Educational, Vocational, MentalHealth, Religious, ReEntry, Therapeutic, LifeSkills,
}

var AllCreditTypes = []CreditType{
	Completion, Participation, EarnedTime, Education,
}

type Program struct {
	DatabaseFields
	Name        string      `json:"name" gorm:"not null;unique" validate:"required,max=255"`
	Description string      `json:"description" gorm:"not null" validate:"required,max=255"`
	FundingType FundingType `json:"funding_type" gorm:"type:funding_type" validate:"required"`
	IsActive    bool        `json:"is_active" gorm:"not null"`
	IsFavorited bool        `json:"is_favorited" gorm:"-"`
	ArchivedAt  *time.Time  `json:"archived_at"`

	HasProgramCompletion bool `json:"has_program_completion" gorm:"not null;default:false"`

	ProgramTypes       []ProgramType        `json:"program_types" gorm:"foreignKey:ProgramID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
	ProgramCreditTypes []ProgramCreditType  `json:"credit_types" gorm:"foreignKey:ProgramID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
	Facilities         []Facility           `json:"facilities" gorm:"-"`                         //preserve original json key
	FacilitiesPrograms []FacilitiesPrograms `json:"-" gorm:"foreignKey:ProgramID;references:ID"` //gorm had issues with many2many
	Classes            []ProgramClass       `json:"-" gorm:"foreignKey:ProgramID;references:ID"`
}

func (Program) TableName() string { return "programs" }

type ProgramType struct {
	ProgramType ProgType `json:"program_type" gorm:"primaryKey;type:program_type" validate:"required"`
	ProgramID   uint     `json:"program_id" gorm:"primaryKey;not null" validate:"required"`

	Program *Program `json:"program" gorm:"foreignKey:ProgramID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
}

func (ProgramType) TableName() string { return "program_types" }

type ProgramCreditType struct {
	CreditType CreditType `json:"credit_type" gorm:"primaryKey;type:credit_type" validate:"required"`
	ProgramID  uint       `json:"program_id" gorm:"primaryKey;not null" validate:"required"`

	Program *Program `json:"program" gorm:"foreignKey:ProgramID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
}

func (ProgramCreditType) TableName() string { return "program_credit_types" }

type ProgramTypeInfo struct {
	ProgramTypes       []ProgType
	ProgramCreditTypes []CreditType
}

type FacilitiesPrograms struct {
	DatabaseFields
	ProgramID    uint    `json:"program_id"`
	FacilityID   uint    `json:"facility_id"`
	ProgramOwner *string `json:"program_owner" gorm:"size:255;column:program_owner" validate:"max=255"`

	Program  *Program  `json:"-" gorm:"foreignKey:ProgramID;references:ID"`
	Facility *Facility `json:"-" gorm:"foreignKey:FacilityID;references:ID"`
}

func (FacilitiesPrograms) TableName() string { return "facilities_programs" }

func (fp *FacilitiesPrograms) BeforeCreate(tx *gorm.DB) error {
	if err := fp.DatabaseFields.BeforeCreate(tx); err != nil {
		return err
	}
	fp.UpdateUserID = nil
	return nil
}

type ProgramsFacilitiesStats struct {
	TotalPrograms                *int64   `json:"total_programs"`
	AvgActiveProgramsPerFacility *float64 `json:"avg_active_programs_per_facility"`
	TotalEnrollments             *int64   `json:"total_enrollments"`
	AttendanceRate               *float64 `json:"attendance_rate"`
	CompletionRate               *float64 `json:"completion_rate"`
}

type ProgramsOverviewTable struct {
	ProgramID              uint     `json:"program_id"`
	ProgramName            string   `json:"program_name"`
	Description            string   `json:"description"`
	ArchivedAt             *string  `json:"archived_at"`
	TotalActiveFacilities  *int64   `json:"total_active_facilities"`
	TotalEnrollments       *int64   `json:"total_enrollments"`
	TotalActiveEnrollments *int64   `json:"total_active_enrollments"`
	TotalClasses           *int64   `json:"total_classes"`
	TotalActiveClasses     *int64   `json:"total_active_classes"`
	TotalCancelledClasses  *int64   `json:"total_cancelled_classes"`
	TotalCapacity          *int64   `json:"total_capacity"`
	TotalFilledSeats       *int64   `json:"total_filled_seats"`
	CompletionRate         *float64 `json:"completion_rate"`
	AttendanceRate         *float64 `json:"attendance_rate"`
	// The numerator and denominator CompletionRate was built from, so a client
	// aggregating across programs can weight by the real rate-eligible
	// population instead of reconstructing one from TotalEnrollments.
	RateEligibleCompletions *int64 `json:"rate_eligible_completions"`
	RateEligibleEnrollments *int64 `json:"rate_eligible_enrollments"`
	Types                   string `json:"program_types" gorm:"column:program_types"`
	CreditTypes             string `json:"credit_types"`
	FundingType             string `json:"funding_type"`
	Status                  bool   `json:"status"`
	Source                  string `json:"source"`
	Loading                 bool   `json:"loading,omitempty"`
}

type ProgramOverviewResponse struct {
	Program                `gorm:"-"`
	ActiveResidents        int      `json:"active_residents"`
	ActiveEnrollments      int      `json:"active_enrollments"`
	Completions            int      `json:"completions"`
	TotalEnrollments       int      `json:"total_enrollments"`
	CompletionRate         *float64 `json:"completion_rate"`
	AttendanceRate         *float64 `json:"attendance_rate"`
	ActiveClassFacilityIDs []int    `json:"active_class_facility_ids" gorm:"-"`
	Source                 string   `json:"source,omitempty" gorm:"-"`
	Loading                bool     `json:"loading,omitempty"`
}

type ProgramCSVData struct {
	FacilityName         string     `json:"facility_name" gorm:"column:facility_name"`
	ProgramName          string     `json:"program_name" gorm:"column:program_name"`
	ClassName            string     `json:"class_name" gorm:"column:class_name"`
	InstructorName       string     `json:"instructor_name" gorm:"column:instructor_name"`
	UnlockEdID           uint       `json:"unlock_ed_id" gorm:"column:unlock_ed_id"`
	NameFirst            string     `json:"name_first" gorm:"column:name_first"`
	NameLast             string     `json:"name_last" gorm:"column:name_last"`
	ResidentID           string     `json:"resident_id" gorm:"column:resident_id"`
	EnrollmentDate       time.Time  `json:"enrollment_date" gorm:"column:enrollment_date"`
	EndDate              *time.Time `json:"end_date" gorm:"column:end_date"`
	EndStatus            string     `json:"end_status" gorm:"column:end_status"`
	AttendancePercentage float64    `json:"attendance_percentage" gorm:"column:attendance_percentage"`
}

func ProgramDataToCSVFormat(data []ProgramCSVData) ([][]string, error) {
	csvData := [][]string{{"Facility Name", "Program Name", "Class Name", "Instructor Name", "Last Name", "First Name", "Resident ID", "UnlockEd ID", "Enrollment Date", "End Date", "End Status", "Attendance Percentage"}}
	for _, record := range data {
		var endDateStr string
		if record.EndDate != nil && !record.EndDate.IsZero() {
			endDateStr = record.EndDate.Format("2006-01-02")
		} else {
			endDateStr = ""
		}

		row := []string{
			record.FacilityName,
			record.ProgramName,
			record.ClassName,
			record.InstructorName,
			record.NameLast,
			record.NameFirst,
			record.ResidentID,
			strconv.Itoa(int(record.UnlockEdID)),
			record.EnrollmentDate.Format("2006-01-02"),
			endDateStr,
			record.EndStatus,
			fmt.Sprintf("%.2f%%", record.AttendancePercentage),
		}
		csvData = append(csvData, row)
	}
	return csvData, nil
}

func (p *Program) GetUniqueCreditTypeString() string {
	uniqueCreditTypes := make(map[string]struct{})
	for _, ct := range p.ProgramCreditTypes {
		uniqueCreditTypes[string(ct.CreditType)] = struct{}{}
	}
	keys := make([]string, 0, len(uniqueCreditTypes))
	for k := range uniqueCreditTypes {
		keys = append(keys, k)
	}
	return strings.Join(keys, ",")
}
