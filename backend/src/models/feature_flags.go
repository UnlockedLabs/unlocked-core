package models

import (
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
		// reference. FeatureCatalog reads their parent from page_feature_flags.
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

// CatalogFeature is one feature in the list core reads from feature_flags.
// Parent is set only for a sub-feature, which is never on where its parent is
// off.
type CatalogFeature struct {
	ID             uint          `json:"-"`
	Name           FeatureAccess `json:"name"`
	Title          string        `json:"title"`
	Description    *string       `json:"description"`
	DefaultEnabled bool          `json:"-"`
	Parent         FeatureAccess `json:"parent,omitempty"`
}

// FeatureCatalog is every feature core knows, top-level ones first: core's own
// and any a product registered (EN-257).
type FeatureCatalog []CatalogFeature

func (c FeatureCatalog) Find(name FeatureAccess) (CatalogFeature, bool) {
	for _, f := range c {
		if f.Name == name {
			return f, true
		}
	}
	return CatalogFeature{}, false
}

func (c FeatureCatalog) Names() []FeatureAccess {
	names := make([]FeatureAccess, len(c))
	for i, f := range c {
		names[i] = f.Name
	}
	return names
}

// TopLevel are the features shown as their own card on the Feature Control
// page; sub-features are nested under their parent.
func (c FeatureCatalog) TopLevel() []FeatureAccess {
	var names []FeatureAccess
	for _, f := range c {
		if f.Parent == "" {
			names = append(names, f.Name)
		}
	}
	return names
}

func Feature(kinds ...FeatureAccess) []FeatureAccess {
	return kinds
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
