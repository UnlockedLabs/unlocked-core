package providerplatforms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLiveProviders lists users for the providers in users, and says no for
// the rest, like core's live provider code.
type fakeLiveProviders struct {
	users map[uint][]module.ProviderUser
	err   error
}

func (f fakeLiveProviders) Users(_ context.Context, providerID uint) ([]module.ProviderUser, bool, error) {
	if f.err != nil {
		return nil, true, f.err
	}
	users, ok := f.users[providerID]
	return users, ok, nil
}

func (fakeLiveProviders) MappingsChanged(uint, uint) {}

// A provider that lists its own users (Essential Ed) is read through core's
// live code; the rest go through the worker.
func TestProviderUsers(t *testing.T) {
	db := openTestDB(t)
	essentialEd := &ProviderPlatform{Name: "Essential Ed", Type: EssentialEd, State: Enabled}
	kolibri := &ProviderPlatform{Name: "Kolibri", Type: Kolibri, AccessKey: "admin:pass", State: Enabled}
	require.NoError(t, db.Create(essentialEd).Error)
	require.NoError(t, db.Create(kolibri).Error)

	liveUsers := []module.ProviderUser{
		{Username: "mapped", NameFirst: "Mapped", ExternalUserID: "e-1"},
		{Username: "new", NameFirst: "New", ExternalUserID: "e-2"},
	}
	middleware := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]ImportUser{{Username: "from-" + r.URL.Path, ExternalUserID: "k-1"}})
	}))
	defer middleware.Close()
	t.Setenv("PROVIDER_SERVICE_URL", middleware.URL)

	srv := &server{
		accounts: accounts{store: &store{db: db}, client: middleware.Client()},
		live:     fakeLiveProviders{users: map[uint][]module.ProviderUser{essentialEd.ID: liveUsers}},
	}
	require.NoError(t, srv.store.createProviderUserMapping(&ProviderUserMapping{
		UserID: 1, ProviderPlatformID: essentialEd.ID, ExternalUserID: "e-1", ExternalUsername: "mapped",
	}))
	ctx := context.Background()

	t.Run("all of a live provider's users", func(t *testing.T) {
		users, err := srv.allProviderUsers(ctx, essentialEd)
		require.NoError(t, err)
		assert.Equal(t, []ImportUser{
			{Username: "mapped", NameFirst: "Mapped", ExternalUserID: "e-1"},
			{Username: "new", NameFirst: "New", ExternalUserID: "e-2"},
		}, users)
	})

	t.Run("a live provider's unmapped users", func(t *testing.T) {
		users, err := srv.listProviderUsers(ctx, essentialEd)
		require.NoError(t, err)
		assert.Equal(t, []ImportUser{{Username: "new", NameFirst: "New", ExternalUserID: "e-2"}}, users)
	})

	t.Run("other providers go through the worker", func(t *testing.T) {
		all, err := srv.allProviderUsers(ctx, kolibri)
		require.NoError(t, err)
		assert.Equal(t, []ImportUser{{Username: "from-/api/users/all", ExternalUserID: "k-1"}}, all)
		unmapped, err := srv.listProviderUsers(ctx, kolibri)
		require.NoError(t, err)
		assert.Equal(t, []ImportUser{{Username: "from-/api/users", ExternalUserID: "k-1"}}, unmapped)
	})

	t.Run("a live provider's error is returned", func(t *testing.T) {
		failing := &server{accounts: srv.accounts, live: fakeLiveProviders{err: errors.New("essential ed is down")}}
		_, err := failing.allProviderUsers(ctx, essentialEd)
		assert.ErrorContains(t, err, "essential ed is down")
		_, err = failing.listProviderUsers(ctx, essentialEd)
		assert.ErrorContains(t, err, "essential ed is down")
	})
}
