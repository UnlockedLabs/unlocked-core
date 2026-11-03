package database

import (
	"context"
	"errors"
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

func TestDrainMappingDeletions(t *testing.T) {
	db := InitDB(context.Background(), true)
	require.NoError(t, db.Create(&[]models.ProviderMappingDeletion{{UserID: 1}, {UserID: 2}, {UserID: 3}}).Error)

	var asked []uint
	err := db.DrainMappingDeletions(context.Background(), func(_ context.Context, id uint) error {
		asked = append(asked, id)
		if id == 2 {
			return errors.New("service down")
		}
		return nil
	})

	assert.ErrorContains(t, err, "service down")
	assert.Equal(t, []uint{1, 2, 3}, asked, "one failure doesn't stop the rest")
	assert.Equal(t, []uint{2}, pendingUserIDs(t, db), "only the failed one stays queued")
}
