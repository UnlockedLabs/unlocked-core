package models

import "time"

// ProviderMappingDeletion is a deleted user whose provider account links
// Provider Platforms hasn't removed yet. Deleting a user queues one in the same
// transaction; a job removes it once Provider Platforms has unlinked the user.
type ProviderMappingDeletion struct {
	UserID    uint `gorm:"primaryKey;autoIncrement:false"`
	CreatedAt time.Time
}
