package database

import (
	"errors"
	"slices"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FacilityFeatureOverviewRow struct {
	FacilityID   uint                          `json:"facility_id"`
	FacilityName string                        `json:"facility_name"`
	Features     map[models.FeatureAccess]bool `json:"features"`
}

type FacilityFeatureDetailRow struct {
	Feature models.FeatureAccess `json:"feature"`
	Enabled bool                 `json:"enabled"`
}

// GetFacilityFeatureAccess resolves the effective feature set for one facility.
// A facility's own override row wins in both directions: it may enable a feature
// that is off statewide, or disable one that is on. The statewide defaults
// (feature_flags.default_enabled) apply only to features the facility has no
// row for. A sub-feature is never effective when its parent isn't — that
// cascade is enforced here regardless of the sub-feature's own override row,
// since disabling the parent doesn't retroactively clear it.
func (db *DB) GetFacilityFeatureAccess(facilityID uint) ([]models.FeatureAccess, error) {
	catalog, err := db.FeatureCatalog()
	if err != nil {
		return nil, err
	}
	overrides, err := db.facilityOverrides(facilityID, catalog)
	if err != nil {
		return nil, err
	}
	return effectiveFeatures(catalog, overrides), nil
}

func effectiveFeatures(catalog models.FeatureCatalog, overrides map[models.FeatureAccess]bool) []models.FeatureAccess {
	enabled := resolveFeatures(catalog, overrides)
	effective := make([]models.FeatureAccess, 0, len(catalog))
	for _, f := range catalog {
		if !enabled[f.Name] {
			continue
		}
		if f.Parent != "" && !enabled[f.Parent] {
			continue
		}
		effective = append(effective, f.Name)
	}
	return effective
}

// resolveFeatures gives every known feature its pre-cascade state: the facility's
// override where one exists, otherwise the statewide default.
func resolveFeatures(catalog models.FeatureCatalog, overrides map[models.FeatureAccess]bool) map[models.FeatureAccess]bool {
	statewideSet := make(map[models.FeatureAccess]bool, len(catalog))
	for _, f := range statewideDefaults(catalog) {
		statewideSet[f] = true
	}
	states := make(map[models.FeatureAccess]bool, len(catalog))
	for _, f := range catalog {
		if override, ok := overrides[f.Name]; ok {
			states[f.Name] = override
		} else {
			states[f.Name] = statewideSet[f.Name]
		}
	}
	return states
}

// namesByID translates facility_feature_flags.feature_flag_id back into the
// FeatureAccess values the rest of this package works in.
func namesByID(catalog models.FeatureCatalog) map[uint]models.FeatureAccess {
	byID := make(map[uint]models.FeatureAccess, len(catalog))
	for _, f := range catalog {
		byID[f.ID] = f.Name
	}
	return byID
}

// facilityOverrides returns one facility's explicit settings. Presence of a key
// means the facility has been explicitly set; the value is that setting.
func (db *DB) facilityOverrides(facilityID uint, catalog models.FeatureCatalog) (map[models.FeatureAccess]bool, error) {
	var overrides []models.FacilityFeatureFlag
	if err := db.Model(&models.FacilityFeatureFlag{}).Where("facility_id = ?", facilityID).Find(&overrides).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facility_feature_flags")
	}
	names := namesByID(catalog)
	set := make(map[models.FeatureAccess]bool, len(overrides))
	for _, o := range overrides {
		set[names[o.FeatureFlagID]] = o.Enabled
	}
	return set, nil
}

// GetFacilityFeatureOverview returns, for every facility (optionally filtered by
// name search and by a single feature's on/off state), the effective state of
// each top-level feature — the data behind the Feature Control list panel's
// pills and "X of Y on" counts.
func (db *DB) GetFacilityFeatureOverview(args *models.QueryContext, filterFeature *models.FeatureAccess, filterEnabled *bool) ([]FacilityFeatureOverviewRow, error) {
	var facilities []models.Facility
	tx := db.WithContext(args.Ctx).Model(&models.Facility{}).Order("name asc")
	if args.Search != "" {
		tx = tx.Where("LOWER(name) LIKE LOWER(?)", "%"+args.Search+"%")
	}
	if err := tx.Find(&facilities).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facilities")
	}

	catalog, err := db.FeatureCatalog()
	if err != nil {
		return nil, err
	}
	topLevel := catalog.TopLevel()

	facilityIDs := make([]uint, len(facilities))
	for i, f := range facilities {
		facilityIDs[i] = f.ID
	}
	overridesByFacility := map[uint]map[models.FeatureAccess]bool{}
	if len(facilityIDs) > 0 {
		var overrides []models.FacilityFeatureFlag
		if err := db.Model(&models.FacilityFeatureFlag{}).Where("facility_id IN ?", facilityIDs).Find(&overrides).Error; err != nil {
			return nil, newGetRecordsDBError(err, "facility_feature_flags")
		}
		names := namesByID(catalog)
		for _, o := range overrides {
			if overridesByFacility[o.FacilityID] == nil {
				overridesByFacility[o.FacilityID] = map[models.FeatureAccess]bool{}
			}
			overridesByFacility[o.FacilityID][names[o.FeatureFlagID]] = o.Enabled
		}
	}

	rows := make([]FacilityFeatureOverviewRow, 0, len(facilities))
	for _, f := range facilities {
		enabled := resolveFeatures(catalog, overridesByFacility[f.ID])
		features := make(map[models.FeatureAccess]bool, len(topLevel))
		for _, feat := range topLevel {
			features[feat] = enabled[feat]
		}
		if filterFeature != nil {
			want := true
			if filterEnabled != nil {
				want = *filterEnabled
			}
			if features[*filterFeature] != want {
				continue
			}
		}
		rows = append(rows, FacilityFeatureOverviewRow{FacilityID: f.ID, FacilityName: f.Name, Features: features})
	}
	return rows, nil
}

// GetFacilityFeatureDetail returns every feature (top-level and sub) with its
// effective state for one facility — the data behind the detail panel.
func (db *DB) GetFacilityFeatureDetail(facilityID uint) ([]FacilityFeatureDetailRow, error) {
	catalog, err := db.FeatureCatalog()
	if err != nil {
		return nil, err
	}
	overrides, err := db.facilityOverrides(facilityID, catalog)
	if err != nil {
		return nil, err
	}
	effective := effectiveFeatures(catalog, overrides)
	rows := make([]FacilityFeatureDetailRow, 0, len(catalog))
	for _, f := range catalog {
		rows = append(rows, FacilityFeatureDetailRow{Feature: f.Name, Enabled: slices.Contains(effective, f.Name)})
	}
	return rows, nil
}

// UpsertFacilityFeatureFlag sets one facility's override for one feature. Any
// feature may be enabled or disabled per facility regardless of its statewide
// default; only enabling a sub-feature whose parent is disabled at this facility
// is rejected, since that state can never be represented in the UI.
func (db *DB) UpsertFacilityFeatureFlag(args *models.QueryContext, facilityID uint, feature models.FeatureAccess, enabled bool) error {
	catalog, err := db.FeatureCatalog()
	if err != nil {
		return err
	}
	flag, ok := catalog.Find(feature)
	if !ok {
		return newBadRequestDBError(errors.New("invalid feature"), "invalid feature")
	}
	if enabled && flag.Parent != "" {
		overrides, err := db.facilityOverrides(facilityID, catalog)
		if err != nil {
			return err
		}
		if !slices.Contains(effectiveFeatures(catalog, overrides), flag.Parent) {
			return newBadRequestDBError(errors.New("parent feature disabled"), "cannot enable this feature while its parent feature is disabled for this facility")
		}
	}
	row := models.FacilityFeatureFlag{FacilityID: facilityID, FeatureFlagID: flag.ID, Enabled: enabled}
	if args.UserID != 0 {
		uid := args.UserID
		row.UpdateUserID = &uid
	}
	if err := db.WithContext(args.Ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "facility_id"}, {Name: "feature_flag_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "update_user_id", "updated_at"}),
	}).Create(&row).Error; err != nil {
		return newCreateDBError(err, "facility_feature_flags")
	}
	return nil
}

// ApplyFacilityFeaturesToAll copies the source facility's effective value for every
// feature (top-level and sub) to every other facility, in one transaction.
func (db *DB) ApplyFacilityFeaturesToAll(args *models.QueryContext, sourceFacilityID uint) error {
	// An unknown source facility has no override rows, so its effective set would
	// resolve to the untouched statewide defaults, and every real facility would
	// then match the `id != source` target filter below — silently resetting every
	// facility in the system. Reject it before anything is written.
	var sourceExists bool
	if err := db.WithContext(args.Ctx).Model(&models.Facility{}).
		Select("count(*) > 0").Where("id = ?", sourceFacilityID).Find(&sourceExists).Error; err != nil {
		return newGetRecordsDBError(err, "facilities")
	}
	if !sourceExists {
		return newNotFoundDBError(gorm.ErrRecordNotFound, "facilities")
	}

	catalog, err := db.FeatureCatalog()
	if err != nil {
		return err
	}
	overrides, err := db.facilityOverrides(sourceFacilityID, catalog)
	if err != nil {
		return err
	}
	effective := effectiveFeatures(catalog, overrides)
	effectiveSet := make(map[models.FeatureAccess]bool, len(effective))
	for _, f := range effective {
		effectiveSet[f] = true
	}

	var facilityIDs []uint
	if err := db.WithContext(args.Ctx).Model(&models.Facility{}).
		Where("id != ?", sourceFacilityID).Pluck("id", &facilityIDs).Error; err != nil {
		return newGetRecordsDBError(err, "facilities")
	}
	if len(facilityIDs) == 0 {
		return nil
	}

	var uid *uint
	if args.UserID != 0 {
		u := args.UserID
		uid = &u
	}

	rows := make([]models.FacilityFeatureFlag, 0, len(facilityIDs)*len(catalog))
	for _, fid := range facilityIDs {
		for _, feature := range catalog {
			rows = append(rows, models.FacilityFeatureFlag{
				FacilityID:    fid,
				FeatureFlagID: feature.ID,
				Enabled:       effectiveSet[feature.Name],
				UpdateUserID:  uid,
			})
		}
	}
	if len(rows) == 0 {
		return nil
	}

	return db.WithContext(args.Ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "facility_id"}, {Name: "feature_flag_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"enabled", "update_user_id", "updated_at"}),
		}).Create(&rows).Error; err != nil {
			return newCreateDBError(err, "facility_feature_flags")
		}
		return nil
	})
}
