package learningrecord

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"gorm.io/gorm"
)

// DatabaseFields are the columns most tables share. The create and update
// user come from the identity.Claims in the query's context.
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

// StringSlice persists a Go string slice as a JSON text column.
type StringSlice []string

func (s StringSlice) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal(s)
	return string(b), err
}

func (s *StringSlice) Scan(value any) error {
	var raw string
	switch v := value.(type) {
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		raw = "[]"
	}
	return json.Unmarshal([]byte(raw), s)
}

type LearningRecordEntry struct {
	DatabaseFields
	UserID              uint        `gorm:"not null" json:"user_id"`
	ClientID            string      `gorm:"not null" json:"client_id"`
	IsDraft             bool        `gorm:"not null;default:false" json:"is_draft"`
	StepIndex           int         `json:"step_index"`
	UiPhase             string      `json:"ui_phase"`
	EditingEntryID      *uint       `json:"editing_entry_id"`
	ProgramName         string      `json:"program_name"`
	FacilityID          *uint       `json:"facility_id"`
	FacilityOther       string      `json:"facility_other"`
	FacilityName        string      `gorm:"->;-:migration" json:"facility_name"`
	CompletionDate      string      `json:"completion_date"`
	Confidence          string      `json:"confidence"`
	Summary             string      `json:"summary"`
	TopSkills           StringSlice `gorm:"type:text" json:"top_skills"`
	BarrierToCompletion string      `json:"barrier_to_completion"`
	GoalConnection      string      `json:"goal_connection"`
	Pride               string      `json:"pride"`
	StandoutMoment      string      `json:"standout_moment"`
	AdviceToPeer        string      `json:"advice_to_peer"`
	ChallengeToggle     *string     `json:"challenge_toggle"`
	ChallengeText       string      `json:"challenge_text"`
	SkillTagsBefore     StringSlice `gorm:"type:text" json:"skill_tags_before"`
	SkillTagsAfter      StringSlice `gorm:"type:text" json:"skill_tags_after"`
	SkillReflection     string      `json:"skill_reflection"`
	GrowthReflection    string      `json:"growth_reflection"`
	SupportSelections   StringSlice `gorm:"type:text" json:"support_selections"`
	NextStepSelections  StringSlice `gorm:"type:text" json:"next_step_selections"`
}

func (LearningRecordEntry) TableName() string { return "learning_record_entries" }

// facility is the read Learning Record makes of core's facilities table: the
// id and name shown in the achievement-location picker. It carries
// DatabaseFields' DeletedAt so GORM's default soft-delete scope excludes a
// deleted facility from that list, the same as models.Facility does in core.
type facility struct {
	DatabaseFields
	Name string `gorm:"size:255;not null"`
}

func (facility) TableName() string { return "facilities" }

// UserAccountHistory is a row of core's user_account_history table. Learning
// Record adds one when a resident deletes an entry.
type UserAccountHistory struct {
	UserID           uint                  `json:"user_id" gorm:"primaryKey"`
	AdminID          *uint                 `json:"admin_id"`
	Action           ActivityHistoryAction `json:"action" gorm:"size:255;primaryKey"`
	CreatedAt        time.Time             `json:"created_at" gorm:"primaryKey"`
	AttendanceStatus string                `json:"attendance_status" gorm:"size:27"`
}

func (UserAccountHistory) TableName() string { return "user_account_history" }

// ActivityHistoryAction is a user_account_history action.
type ActivityHistoryAction string

const LearningRecordDeleted ActivityHistoryAction = "learning_record_deleted"

func NewUserAccountHistory(userID uint, action ActivityHistoryAction, adminID *uint) *UserAccountHistory {
	return &UserAccountHistory{
		UserID:    userID,
		AdminID:   adminID,
		Action:    action,
		CreatedAt: time.Now(),
	}
}
