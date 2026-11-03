package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
)

// DrainMappingDeletions calls unlink for each user queued by DeleteUser and
// removes the ones it unlinks. A user it can't unlink stays queued for the next
// run, and the rest still go.
func (db *DB) DrainMappingDeletions(ctx context.Context, unlink func(ctx context.Context, userID uint) error) error {
	var queued []models.ProviderMappingDeletion
	if err := db.WithContext(ctx).Order("created_at, user_id").Find(&queued).Error; err != nil {
		return fmt.Errorf("list queued mapping deletions: %w", err)
	}
	var errs []error
	for _, q := range queued {
		if err := unlink(ctx, q.UserID); err != nil {
			errs = append(errs, fmt.Errorf("unlink user %d: %w", q.UserID, err))
			continue
		}
		if err := db.WithContext(ctx).Delete(&models.ProviderMappingDeletion{}, "user_id = ?", q.UserID).Error; err != nil {
			errs = append(errs, fmt.Errorf("dequeue user %d: %w", q.UserID, err))
		}
	}
	return errors.Join(errs...)
}
