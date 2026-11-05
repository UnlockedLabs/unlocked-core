package database

import (
	"context"
	"fmt"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
)

// QueuedMappingDeletions is the users DeleteUser queued for Provider Platforms
// to unlink, oldest first.
func (db *DB) QueuedMappingDeletions(ctx context.Context) ([]uint, error) {
	var ids []uint
	if err := db.WithContext(ctx).Model(&models.ProviderMappingDeletion{}).Order("created_at, user_id").Pluck("user_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list queued mapping deletions: %w", err)
	}
	return ids, nil
}

// DequeueMappingDeletion removes userID from the queue once Provider Platforms
// has unlinked them.
func (db *DB) DequeueMappingDeletion(ctx context.Context, userID uint) error {
	if err := db.WithContext(ctx).Delete(&models.ProviderMappingDeletion{}, "user_id = ?", userID).Error; err != nil {
		return fmt.Errorf("dequeue user %d: %w", userID, err)
	}
	return nil
}
