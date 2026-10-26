package database

import (
	"context"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	ocapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/opencontent/api"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"
)

func (db *DB) RunOrResetDemoSeed(facilityId uint, openContent ocapi.OpenContent) error {
	ctx := context.Background()
	learning := registry.ProviderPlatforms(db.DB)
	// seeding data for demo will only seed user activity/milestones/open-content activity for existing users
	lastSeed, found, err := learning.LastDemoSeed(ctx)
	if err != nil || !found {
		return db.RunDemoSeed(facilityId, openContent)
	}
	if err := learning.ResetDemoLearning(ctx, lastSeed); err != nil {
		return newDeleteDBError(err, "demo learning data")
	}
	if err := openContent.ResetDemoActivity(ctx, lastSeed); err != nil {
		return newDeleteDBError(err, "open_content_activities")
	}
	return db.RunDemoSeed(facilityId, openContent)
}

func (db *DB) RunDemoSeed(facilityId uint, openContent ocapi.OpenContent) error {
	var userIDs []uint
	if err := db.Model(&models.User{}).Where("facility_id = ?", facilityId).Pluck("id", &userIDs).Error; err != nil {
		return newGetRecordsDBError(err, "users")
	}
	ctx := context.Background()
	if err := openContent.SeedDemoActivity(ctx, facilityId, userIDs); err != nil {
		return newCreateDBError(err, "open_content_activities")
	}
	if err := registry.ProviderPlatforms(db.DB).SeedDemoLearning(ctx, userIDs); err != nil {
		return newCreateDBError(err, "demo learning data")
	}
	return nil
}
