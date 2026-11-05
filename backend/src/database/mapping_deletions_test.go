package database

import (
	"context"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newUser(t *testing.T, db *DB, name string) *models.User {
	t.Helper()
	u := &models.User{Username: name, NameFirst: name, NameLast: name, Email: name + "@unlocked.v2", Role: "student", FacilityID: 1}
	require.NoError(t, db.Create(u).Error)
	return u
}

func pendingUserIDs(t *testing.T, db *DB) []uint {
	t.Helper()
	var ids []uint
	require.NoError(t, db.Model(&models.ProviderMappingDeletion{}).Order("user_id").Pluck("user_id", &ids).Error)
	return ids
}

func TestDeleteUserQueuesMappingDeletion(t *testing.T) {
	db := InitDB(context.Background(), true)
	u := newUser(t, db, "gone")

	require.NoError(t, db.DeleteUser(int(u.ID)))
	assert.Equal(t, []uint{u.ID}, pendingUserIDs(t, db))
}

func TestDeleteUserMissingQueuesNothing(t *testing.T) {
	db := InitDB(context.Background(), true)

	assert.Error(t, db.DeleteUser(9999))
	assert.Empty(t, pendingUserIDs(t, db))
}

func TestMappingDeletionQueue(t *testing.T) {
	db := InitDB(context.Background(), true)
	ctx := context.Background()
	require.NoError(t, db.Create(&[]models.ProviderMappingDeletion{{UserID: 3}, {UserID: 1}}).Error)

	queued, err := db.QueuedMappingDeletions(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uint{1, 3}, queued)

	require.NoError(t, db.DequeueMappingDeletion(ctx, 3))
	assert.Equal(t, []uint{1}, pendingUserIDs(t, db))
}
