package database

import (
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
)

// FeatureCatalog reads every feature from feature_flags, top-level ones first.
// A sub-feature's parent comes from page_feature_flags, through the shadow row
// EN-96 gave each sub-feature. Sub-features are in the order they were added
// to page_feature_flags, since 00077 copied the shadow rows in no set order. It
// is read on every use, so a feature a product registers shows up without a
// restart.
func (db *DB) FeatureCatalog() (models.FeatureCatalog, error) {
	var catalog models.FeatureCatalog
	if err := db.Table("feature_flags AS ff").
		Select("ff.id, ff.name, ff.title, ff.description, ff.default_enabled, ff.is_page_feature, COALESCE(parent.name, '') AS parent").
		Joins("LEFT JOIN page_feature_flags pff ON ff.is_page_feature AND pff.page_feature = ff.name AND pff.deleted_at IS NULL").
		Joins("LEFT JOIN feature_flags parent ON parent.id = pff.feature_flag_id").
		Where("ff.deleted_at IS NULL").
		Order("ff.is_page_feature, pff.id, ff.id").
		Scan(&catalog).Error; err != nil {
		return nil, newGetRecordsDBError(err, "feature_flags")
	}
	return catalog, nil
}

// UpsertFeature adds a feature, or updates the title and description of one
// that exists. It never sets a feature's default or any facility's setting,
// so a new feature starts off everywhere and registering again changes only
// its text.
func (db *DB) UpsertFeature(name models.FeatureAccess, title string, description *string) error {
	flag := models.FeatureFlags{Name: name, Title: title, Description: description}
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"title", "description", "updated_at", "deleted_at"}),
	}).Create(&flag).Error; err != nil {
		return newCreateDBError(err, "feature_flags")
	}
	return nil
}

// seedTestFeatureFlags mirrors the feature_flags/page_feature_flags rows the
// Postgres migrations leave (00018 through 00079). The SQLite test DB only gets
// its schema from MigrateTesting's AutoMigrate, never those SQL files, so
// without this there would be no features to resolve.
func (db *DB) seedTestFeatureFlags() {
	seed := []struct {
		name, parent   models.FeatureAccess
		defaultEnabled bool
	}{
		{models.OpenContentAccess, "", true},
		{models.ProviderAccess, "", true},
		{models.ProgramAccess, "", true},
		{models.LearningRecordAccess, "", false},
		{models.AiTutorAccess, "", false},
		{models.RequestContentAccess, models.OpenContentAccess, true},
		{models.HelpfulLinksAccess, models.OpenContentAccess, true},
		{models.UploadVideoAccess, models.OpenContentAccess, true},
		{models.ResidentProgramsAccess, models.ProgramAccess, true},
	}
	ids := make(map[models.FeatureAccess]uint, len(seed))
	for _, s := range seed {
		flag := models.FeatureFlags{Name: s.name, Title: string(s.name), DefaultEnabled: s.defaultEnabled, IsPageFeature: s.parent != ""}
		if err := db.Create(&flag).Error; err != nil {
			logrus.Fatalf("Failed to create feature flag: %v", err)
		}
		ids[s.name] = flag.ID
		if s.parent == "" {
			continue
		}
		if err := db.Create(&models.PageFeatureFlags{
			FeatureFlagID: ids[s.parent],
			PageFeature:   s.name,
		}).Error; err != nil {
			logrus.Fatalf("Failed to create page feature flag: %v", err)
		}
	}
}
