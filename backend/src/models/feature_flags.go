package models

import (
	"slices"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"gorm.io/gorm"
)

type (
	FeatureAccess = features.Access

	FeatureFlags struct {
		DatabaseFields
		Name           FeatureAccess `json:"name" gorm:"not null;unique"`
		DefaultEnabled bool          `json:"default_enabled" gorm:"not null;default:false"`
		Title          string        `json:"title" gorm:"not null"`
		Description    *string       `json:"description"`
		// IsPageFeature marks a shadow row copied from page_feature_flags, so every
		// feature (top-level or sub) has one real id for facility_feature_flags to
		// reference. GetFeatureAccess() excludes these to avoid double-counting.
		IsPageFeature bool               `json:"-" gorm:"not null;default:false"`
		PageFeatures  []PageFeatureFlags `gorm:"foreignKey:FeatureFlagID"`
	}

	PageFeatureFlags struct {
		DatabaseFields
		FeatureFlagID uint          `json:"feature_flag_id" gorm:"not null;index"`
		PageFeature   FeatureAccess `json:"page_feature" gorm:"not null"`
	}

	// FacilityFeatureFlag is a per-facility override of a statewide feature default.
	// An absent row means the facility inherits the statewide default; presence of a
	// row (either value) means the facility has been explicitly set.
	FacilityFeatureFlag struct {
		FacilityID    uint      `json:"facility_id" gorm:"primaryKey"`
		FeatureFlagID uint      `json:"feature_flag_id" gorm:"primaryKey"`
		Enabled       bool      `json:"enabled" gorm:"not null"`
		CreateUserID  *uint     `json:"create_user_id,omitempty"`
		UpdateUserID  *uint     `json:"update_user_id,omitempty"`
		CreatedAt     time.Time `json:"created_at"`
		UpdatedAt     time.Time `json:"updated_at"`
	}
)

const (
	OpenContentAccess    = features.OpenContent
	ProviderAccess       = features.ProviderPlatforms
	ProgramAccess        = features.ProgramManagement
	LearningRecordAccess = features.LearningRecord
	AiTutorAccess        = features.AITutor

	// these are the page/sub level features
	RequestContentAccess   = features.RequestContent
	HelpfulLinksAccess     = features.HelpfulLinks
	UploadVideoAccess      = features.UploadVideo
	ResidentProgramsAccess = features.ResidentPrograms
)

var AllFeatures = features.All

// TopLevelFeatures are the features shown as their own card/pill on the Feature Control
// page. Page-level (sub-)features are nested under their parent below.
var TopLevelFeatures = []FeatureAccess{OpenContentAccess, ProviderAccess, ProgramAccess, LearningRecordAccess, AiTutorAccess}

// SubFeatureParent maps a page-level feature to the top-level feature that gates it:
// a sub-feature can never be enabled at a facility where its parent is disabled.
var SubFeatureParent = map[FeatureAccess]FeatureAccess{
	RequestContentAccess:   OpenContentAccess,
	HelpfulLinksAccess:     OpenContentAccess,
	UploadVideoAccess:      OpenContentAccess,
	ResidentProgramsAccess: ProgramAccess,
}

// DefaultEnabled is the statewide default for every feature (top-level and sub),
// replacing both the dropped feature_flags.enabled and page_feature_flags.enabled
// columns -- nothing ever edited either after their initial seed insert.
var DefaultEnabled = map[FeatureAccess]bool{
	OpenContentAccess:    true,
	ProviderAccess:       true,
	ProgramAccess:        true,
	LearningRecordAccess: false,
	AiTutorAccess:        false,

	RequestContentAccess:   true,
	HelpfulLinksAccess:     true,
	UploadVideoAccess:      true,
	ResidentProgramsAccess: true,
}

func Feature(kinds ...FeatureAccess) []FeatureAccess {
	return kinds
}
func ValidFeature(feature FeatureAccess) bool {
	return slices.Contains(AllFeatures, feature)
}
func (FeatureFlags) TableName() string { return "feature_flags" }

func (PageFeatureFlags) TableName() string { return "page_feature_flags" }

func (FacilityFeatureFlag) TableName() string { return "facility_feature_flags" }

// BeforeCreate sets CreateUserID from context. FacilityFeatureFlag doesn't embed
// DatabaseFields (its primary key is the composite facility_id/feature_flag_id pair), so
// it needs its own copy of DatabaseFields.BeforeCreate's behavior.
func (f *FacilityFeatureFlag) BeforeCreate(tx *gorm.DB) error {
	if userID, ok := tx.Statement.Context.Value(UserIDKey).(uint); ok {
		f.CreateUserID = &userID
	}
	return nil
}
