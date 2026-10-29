package database

import (
	"context"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	ocapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/opencontent/api"
	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"
)

func (db *DB) RunOrResetDemoSeed(facilityId uint, openContent ocapi.OpenContent, providerPlatforms ppapi.ProviderPlatforms) error {
	ctx := context.Background()
	// seeding data for demo will only seed user activity/milestones/open-content activity for existing users
	lastSeed, found, err := providerPlatforms.LastDemoSeed(ctx)
	if err != nil || !found {
		return db.RunDemoSeed(facilityId, openContent, providerPlatforms)
	}
	if err := providerPlatforms.ResetDemoLearning(ctx, lastSeed); err != nil {
		return newDeleteDBError(err, "demo learning data")
	}
	if err := openContent.ResetDemoActivity(ctx, lastSeed); err != nil {
		return newDeleteDBError(err, "open_content_activities")
	}
	return db.RunDemoSeed(facilityId, openContent, providerPlatforms)
}

func (db *DB) RunDemoSeed(facilityId uint, openContent ocapi.OpenContent, providerPlatforms ppapi.ProviderPlatforms) error {
	var userIDs []uint
	if err := db.Model(&models.User{}).Where("facility_id = ?", facilityId).Pluck("id", &userIDs).Error; err != nil {
		return newGetRecordsDBError(err, "users")
	}
	ctx := context.Background()
	if err := openContent.SeedDemoActivity(ctx, facilityId, userIDs); err != nil {
		return newCreateDBError(err, "open_content_activities")
	}
	if err := providerPlatforms.SeedDemoLearning(ctx, userIDs); err != nil {
		return newCreateDBError(err, "demo learning data")
	}
	return nil
}
