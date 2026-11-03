package database

import (
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/stretchr/testify/assert"
)

// A facility override is authoritative in both directions; the statewide default
// applies only where the facility has no row.
func TestResolveFeatures_OverrideWinsOverStatewide(t *testing.T) {
	catalog := models.FeatureCatalog{
		{ID: 1, Name: models.OpenContentAccess, DefaultEnabled: true},
		{ID: 2, Name: models.ProgramAccess, DefaultEnabled: true},
		{ID: 3, Name: models.LearningRecordAccess, DefaultEnabled: false},
	}

	tests := []struct {
		name      string
		overrides map[models.FeatureAccess]bool
		feature   models.FeatureAccess
		want      bool
	}{
		{
			name:      "override enables a statewide-disabled feature",
			overrides: map[models.FeatureAccess]bool{models.LearningRecordAccess: true},
			feature:   models.LearningRecordAccess,
			want:      true,
		},
		{
			name:      "override disables a statewide-enabled feature",
			overrides: map[models.FeatureAccess]bool{models.OpenContentAccess: false},
			feature:   models.OpenContentAccess,
			want:      false,
		},
		{
			name:      "no override inherits statewide on",
			overrides: nil,
			feature:   models.ProgramAccess,
			want:      true,
		},
		{
			name:      "no override inherits statewide off",
			overrides: nil,
			feature:   models.LearningRecordAccess,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			states := resolveFeatures(catalog, tt.overrides)
			assert.Equal(t, tt.want, states[tt.feature])
		})
	}
}

// Every known feature gets an entry, so callers can distinguish "off" from
// "absent from the statewide list".
func TestResolveFeatures_CoversAllFeatures(t *testing.T) {
	catalog := models.FeatureCatalog{
		{ID: 1, Name: models.OpenContentAccess, DefaultEnabled: true},
		{ID: 2, Name: models.UploadVideoAccess, DefaultEnabled: true, Parent: models.OpenContentAccess},
		{ID: 3, Name: "registered_tool"},
	}
	states := resolveFeatures(catalog, nil)
	for _, f := range catalog {
		_, ok := states[f.Name]
		assert.True(t, ok, "expected an entry for %s", f.Name)
	}
}

// A sub-feature is on by default only when its parent is too.
func TestStatewideDefaults(t *testing.T) {
	tests := []struct {
		name          string
		parentDefault bool
		want          []models.FeatureAccess
	}{
		{"parent on by default", true, []models.FeatureAccess{models.OpenContentAccess, models.UploadVideoAccess}},
		{"parent off by default", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := models.FeatureCatalog{
				{ID: 1, Name: models.OpenContentAccess, DefaultEnabled: tt.parentDefault},
				{ID: 2, Name: models.UploadVideoAccess, DefaultEnabled: true, Parent: models.OpenContentAccess},
			}
			assert.Equal(t, tt.want, statewideDefaults(catalog))
		})
	}
}
