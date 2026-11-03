package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A facility override is authoritative in both directions; the statewide default
// applies only where the facility has no row.
func TestResolve_OverrideWinsOverStatewide(t *testing.T) {
	catalog := FeatureCatalog{
		{ID: 1, Name: OpenContentAccess, DefaultEnabled: true},
		{ID: 2, Name: ProgramAccess, DefaultEnabled: true},
		{ID: 3, Name: LearningRecordAccess, DefaultEnabled: false},
	}

	tests := []struct {
		name      string
		overrides map[FeatureAccess]bool
		feature   FeatureAccess
		want      bool
	}{
		{
			name:      "override enables a statewide-disabled feature",
			overrides: map[FeatureAccess]bool{LearningRecordAccess: true},
			feature:   LearningRecordAccess,
			want:      true,
		},
		{
			name:      "override disables a statewide-enabled feature",
			overrides: map[FeatureAccess]bool{OpenContentAccess: false},
			feature:   OpenContentAccess,
			want:      false,
		},
		{
			name:      "no override inherits statewide on",
			overrides: nil,
			feature:   ProgramAccess,
			want:      true,
		},
		{
			name:      "no override inherits statewide off",
			overrides: nil,
			feature:   LearningRecordAccess,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			states := catalog.Resolve(tt.overrides)
			assert.Equal(t, tt.want, states[tt.feature])
		})
	}
}

// Every known feature gets an entry, so callers can distinguish "off" from
// "absent from the statewide list".
func TestResolve_CoversAllFeatures(t *testing.T) {
	catalog := FeatureCatalog{
		{ID: 1, Name: OpenContentAccess, DefaultEnabled: true},
		{ID: 2, Name: UploadVideoAccess, DefaultEnabled: true, Parent: OpenContentAccess},
		{ID: 3, Name: "registered_tool"},
	}
	states := catalog.Resolve(nil)
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
		want          []FeatureAccess
	}{
		{"parent on by default", true, []FeatureAccess{OpenContentAccess, UploadVideoAccess}},
		{"parent off by default", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := FeatureCatalog{
				{ID: 1, Name: OpenContentAccess, DefaultEnabled: tt.parentDefault},
				{ID: 2, Name: UploadVideoAccess, DefaultEnabled: true, Parent: OpenContentAccess},
			}
			assert.Equal(t, tt.want, catalog.StatewideDefaults())
		})
	}
}

// A sub-feature's row is never top-level, even if its parent link is missing.
func TestTopLevel(t *testing.T) {
	catalog := FeatureCatalog{
		{ID: 1, Name: OpenContentAccess},
		{ID: 2, Name: UploadVideoAccess, IsPageFeature: true, Parent: OpenContentAccess},
		{ID: 3, Name: HelpfulLinksAccess, IsPageFeature: true},
		{ID: 4, Name: "registered_tool"},
	}
	assert.Equal(t, []FeatureAccess{OpenContentAccess, "registered_tool"}, catalog.TopLevel())
}
