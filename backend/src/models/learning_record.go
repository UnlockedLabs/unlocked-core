package models

import (
	"database/sql/driver"
	"encoding/json"
)

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
	// The uniqueIndex tag exists only so AutoMigrate gives the SQLite test
	// database the same constraint migration 00069 gives production
	// (idx_learning_record_entries_client_id, a partial index on
	// deleted_at IS NULL) -- without it, UpsertLearningRecordDraft's ON
	// CONFLICT has nothing to target and every test against it 500s. GORM's
	// AutoMigrate on SQLite does not support the partial "where" production
	// uses, so this is a full index; it is never applied to production,
	// which keeps migration 00069's.
	UserID   uint   `gorm:"not null;uniqueIndex:idx_learning_record_entries_client_id" json:"user_id"`
	ClientID string `gorm:"not null;uniqueIndex:idx_learning_record_entries_client_id" json:"client_id"`
	IsDraft             bool        `gorm:"not null;default:false" json:"is_draft"`
	StepIndex           int         `                         json:"step_index"`
	UiPhase             string      `                         json:"ui_phase"`
	EditingEntryID      *uint       `                         json:"editing_entry_id"`
	ProgramName         string      `                         json:"program_name"`
	FacilityID          *uint       `                 json:"facility_id"`
	FacilityOther       string      `                 json:"facility_other"`
	FacilityName        string      `gorm:"->;-:migration" json:"facility_name"`
	CompletionDate      string      `                         json:"completion_date"`
	Confidence          string      `                         json:"confidence"`
	Summary             string      `                         json:"summary"`
	TopSkills           StringSlice `gorm:"type:text"         json:"top_skills"`
	BarrierToCompletion string      `                         json:"barrier_to_completion"`
	GoalConnection      string      `                         json:"goal_connection"`
	Pride               string      `                         json:"pride"`
	StandoutMoment      string      `                         json:"standout_moment"`
	AdviceToPeer        string      `                         json:"advice_to_peer"`
	ChallengeToggle     *string     `                         json:"challenge_toggle"`
	ChallengeText       string      `                         json:"challenge_text"`
	SkillTagsBefore     StringSlice `gorm:"type:text"         json:"skill_tags_before"`
	SkillTagsAfter      StringSlice `gorm:"type:text"         json:"skill_tags_after"`
	SkillReflection     string      `                         json:"skill_reflection"`
	GrowthReflection    string      `                         json:"growth_reflection"`
	SupportSelections   StringSlice `gorm:"type:text"         json:"support_selections"`
	NextStepSelections  StringSlice `gorm:"type:text"         json:"next_step_selections"`
}

func (LearningRecordEntry) TableName() string { return "learning_record_entries" }
