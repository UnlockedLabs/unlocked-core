package integration

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/UnlockedLabs/unlocked-contracts/providerplatforms"
	"gorm.io/gorm"
)

const fakeServiceToken = "test-service-token"

// fakeProviderPlatforms stands in for the Provider Platforms service, which is
// its own repo since EN-206. It answers core's questions from the two tables a
// test creates with createTables, so core's Programs screens run against real
// provider rows without that service. Everything else panics, which is how a
// test finds out it needs more.
type fakeProviderPlatforms struct {
	providerplatforms.ProviderPlatforms
	db *gorm.DB
}

// startFakeProviderPlatforms serves the fake the way the service is served and
// points core at it. The test server reads these when it is built, so call it
// first; point the fake at the server's database with attach.
func startFakeProviderPlatforms(t *testing.T) *fakeProviderPlatforms {
	t.Helper()
	fake := &fakeProviderPlatforms{}
	srv := httptest.NewServer(providerplatforms.NewHandler(fake, fakeServiceToken))
	t.Cleanup(srv.Close)
	t.Setenv("PROVIDER_PLATFORMS_URL", srv.URL)
	t.Setenv("SERVICE_TOKEN", fakeServiceToken)
	return fake
}

// attach gives the fake the database it reads and creates its tables there.
func (f *fakeProviderPlatforms) attach(t *testing.T, db *gorm.DB) {
	t.Helper()
	f.db = db
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS provider_platforms (
			id integer PRIMARY KEY, type text, name text, account_id text, access_key text, base_url text,
			state text, external_auth_provider_id text, created_at datetime, updated_at datetime, deleted_at datetime)`,
		`CREATE TABLE IF NOT EXISTS provider_user_mappings (
			id integer PRIMARY KEY, user_id integer, provider_platform_id integer, external_user_id text,
			external_username text, authentication_provider_status text, created_at datetime, updated_at datetime, deleted_at datetime)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatalf("create the fake provider tables: %v", err)
		}
	}
}

type providerRow struct {
	ID        uint
	Type      providerplatforms.ProviderType
	Name      string
	AccountID string
	AccessKey string
	BaseURL   string
}

func (r providerRow) provider() providerplatforms.Provider {
	return providerplatforms.Provider{ID: r.ID, Type: r.Type, Name: r.Name, BaseURL: r.BaseURL, AccountID: r.AccountID, AccessKey: r.AccessKey}
}

const providerColumns = "id, type, name, account_id, access_key, base_url"

func (f *fakeProviderPlatforms) Provider(ctx context.Context, id uint) (providerplatforms.Provider, error) {
	var rows []providerRow
	err := f.db.WithContext(ctx).Raw("SELECT "+providerColumns+" FROM provider_platforms WHERE id = ? AND deleted_at IS NULL", id).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return providerplatforms.Provider{}, err
	}
	return rows[0].provider(), nil
}

func (f *fakeProviderPlatforms) ActiveProviders(ctx context.Context) ([]providerplatforms.Provider, error) {
	var rows []providerRow
	err := f.db.WithContext(ctx).Raw("SELECT " + providerColumns + " FROM provider_platforms WHERE state = 'enabled' AND deleted_at IS NULL ORDER BY id").Scan(&rows).Error
	out := make([]providerplatforms.Provider, len(rows))
	for i, r := range rows {
		out[i] = r.provider()
	}
	return out, err
}

func (f *fakeProviderPlatforms) UserMappings(ctx context.Context, userID uint) ([]providerplatforms.Mapping, error) {
	var out []providerplatforms.Mapping
	err := f.db.WithContext(ctx).Raw(`SELECT provider_platform_id AS provider_id, external_user_id
		FROM provider_user_mappings WHERE user_id = ? AND deleted_at IS NULL ORDER BY id`, userID).Scan(&out).Error
	return out, err
}

func (f *fakeProviderPlatforms) MappedUsers(ctx context.Context, providerID uint, externalUserIDs []string, facilityID uint) ([]providerplatforms.MappedUser, error) {
	var out []providerplatforms.MappedUser
	q := f.db.WithContext(ctx).Table("provider_user_mappings").
		Select("provider_user_mappings.external_user_id, provider_user_mappings.user_id, users.name_first, users.name_last, users.doc_id").
		Joins("JOIN users ON users.id = provider_user_mappings.user_id").
		Where("provider_user_mappings.provider_platform_id = ? AND provider_user_mappings.external_user_id IN ? AND users.deleted_at IS NULL", providerID, externalUserIDs)
	if facilityID != 0 {
		q = q.Where("users.facility_id = ?", facilityID)
	}
	return out, q.Scan(&out).Error
}

func (f *fakeProviderPlatforms) CountMapped(ctx context.Context, providerID uint, externalUserIDs []string) (int64, error) {
	var n int64
	err := f.db.WithContext(ctx).Table("provider_user_mappings").
		Joins("JOIN users ON users.id = provider_user_mappings.user_id").
		Where("provider_user_mappings.provider_platform_id = ? AND provider_user_mappings.external_user_id IN ? AND users.deleted_at IS NULL", providerID, externalUserIDs).
		Count(&n).Error
	return n, err
}

func (f *fakeProviderPlatforms) CountMappedAtFacility(ctx context.Context, providerID uint, externalUserIDs []string, facilityID uint) (int64, error) {
	var n int64
	err := f.db.WithContext(ctx).Table("provider_user_mappings").
		Joins("JOIN users ON users.id = provider_user_mappings.user_id").
		Where("provider_user_mappings.provider_platform_id = ? AND provider_user_mappings.external_user_id IN ? AND users.facility_id = ? AND users.deleted_at IS NULL",
			providerID, externalUserIDs, facilityID).
		Count(&n).Error
	return n, err
}

func (f *fakeProviderPlatforms) CountMappedPerFacility(ctx context.Context, providerID uint, externalUserIDs []string) (map[uint]int64, error) {
	var rows []struct {
		FacilityID uint
		Count      int64
	}
	err := f.db.WithContext(ctx).Raw(`SELECT u.facility_id AS facility_id, COUNT(*) AS count
		FROM provider_user_mappings pum JOIN users u ON u.id = pum.user_id
		WHERE pum.provider_platform_id = ? AND pum.external_user_id IN ? AND pum.deleted_at IS NULL AND u.deleted_at IS NULL
		GROUP BY u.facility_id`, providerID, externalUserIDs).Scan(&rows).Error
	out := make(map[uint]int64, len(rows))
	for _, r := range rows {
		out[r.FacilityID] = r.Count
	}
	return out, err
}
