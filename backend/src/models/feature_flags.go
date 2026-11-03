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
	IsPageFeature  bool          `json:"-"`
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

// TopLevel are the features shown as their own card on the Feature Control
// page; sub-features are nested under their parent.
func (c FeatureCatalog) TopLevel() []FeatureAccess {
	var names []FeatureAccess
	for _, f := range c {
		if !f.IsPageFeature {
			names = append(names, f.Name)
		}
	}
	return names
}

// StatewideDefaults is every feature on by default: a top-level feature by its
// own default, and a sub-feature only when its parent is on by default too.
func (c FeatureCatalog) StatewideDefaults() []FeatureAccess {
	var on []FeatureAccess
	for _, f := range c {
		if !f.DefaultEnabled {
			continue
		}
		if f.Parent != "" {
			if parent, ok := c.Find(f.Parent); !ok || !parent.DefaultEnabled {
				continue
			}
		}
		on = append(on, f.Name)
	}
	return on
}

// Resolve gives every feature its state before the parent rule: the facility's
// override where it has one, otherwise the statewide default. A facility's
// override wins in both directions.
func (c FeatureCatalog) Resolve(overrides map[FeatureAccess]bool) map[FeatureAccess]bool {
	statewide := make(map[FeatureAccess]bool, len(c))
	for _, f := range c.StatewideDefaults() {
		statewide[f] = true
	}
	states := make(map[FeatureAccess]bool, len(c))
	for _, f := range c {
		if override, ok := overrides[f.Name]; ok {
			states[f.Name] = override
		} else {
			states[f.Name] = statewide[f.Name]
		}
	}
	return states
}

// Effective is every feature on at a facility with these overrides. A
// sub-feature is never on where its parent is off, whatever its own override
// says, since turning the parent off doesn't clear it.
func (c FeatureCatalog) Effective(overrides map[FeatureAccess]bool) []FeatureAccess {
	states := c.Resolve(overrides)
	effective := make([]FeatureAccess, 0, len(c))
	for _, f := range c {
		if !states[f.Name] {
			continue
		}
		if f.Parent != "" && !states[f.Parent] {
			continue
		}
		effective = append(effective, f.Name)
	}
	return effective
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
