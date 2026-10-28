package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	ory "github.com/ory/kratos-client-go"
	"github.com/stretchr/testify/require"
)

// These tests pin what every Provider Platforms route answers (EN-193): the
// status and the JSON, and who may call it. They also cover the core routes
// that reach into Provider Platforms data. Like the Open Content tests, they
// seed with SQL and read raw JSON, and must pass unchanged after the move.
//
// Regenerate the golden files after an intended change with:
//
//	go test ./tests/integration -run 'TestProviderPlatform' -update

// fakeExternal stands in for everything Provider Platforms calls over HTTP:
// its worker's user lists, Kratos, Hydra and Kolibri. IDs it hands out count up,
// so the golden files are stable.
type fakeExternal struct {
	*httptest.Server
	mu         sync.Mutex
	identities int
	clients    int
}

// providerUser is a user's account at a provider, as the worker sends it.
type providerUser struct {
	Username         string `json:"username"`
	NameFirst        string `json:"name_first"`
	NameLast         string `json:"name_last"`
	Email            string `json:"email"`
	ExternalUserID   string `json:"external_user_id"`
	ExternalUsername string `json:"external_username"`
}

// The users the worker returns for the Kolibri provider. /api/users
// is the unmapped ones; /api/users/all adds the one mapped to jdoe.
var (
	unmappedProviderUsers = []providerUser{
		{Username: "mary.major", NameFirst: "Mary", NameLast: "Major", Email: "mary@example.com", ExternalUserID: "k-2002", ExternalUsername: "mary.major"},
		{Username: "jane.smith", NameFirst: "Jane", NameLast: "Smith", Email: "jane@example.com", ExternalUserID: "k-2003", ExternalUsername: "jane.smith"},
	}
	allProviderUsers = append([]providerUser{
		{Username: "john.doe", NameFirst: "John", NameLast: "Doe", Email: "john@example.com", ExternalUserID: "k-1001", ExternalUsername: "john.doe"},
	}, unmappedProviderUsers...)
)

func newFakeExternal(t *testing.T) *fakeExternal {
	t.Helper()
	f := &fakeExternal{}
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	identity := func(id string) map[string]any {
		return map[string]any{"id": id, "schema_id": "default", "schema_url": "http://kratos.test/schema", "traits": map[string]any{}}
	}

	// The worker's user lists
	mux.HandleFunc("GET /api/users", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, unmappedProviderUsers)
	})
	mux.HandleFunc("GET /api/users/all", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, allProviderUsers)
	})

	// Kratos
	mux.HandleFunc("POST /admin/identities", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.identities++
		id := fmt.Sprintf("kratos-%d", f.identities)
		f.mu.Unlock()
		writeJSON(w, http.StatusCreated, identity(id))
	})
	mux.HandleFunc("GET /admin/identities/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, identity(r.PathValue("id")))
	})
	mux.HandleFunc("PUT /admin/identities/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, identity(r.PathValue("id")))
	})

	// Hydra
	mux.HandleFunc("POST /admin/clients", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.clients++
		n := f.clients
		f.mu.Unlock()
		writeJSON(w, http.StatusCreated, map[string]any{
			"client_id":     fmt.Sprintf("client-%d", n),
			"client_name":   body["client_name"],
			"client_secret": fmt.Sprintf("secret-%d", n),
			"scope":         body["scope"],
		})
	})
	mux.HandleFunc("DELETE /admin/clients/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// Kolibri
	mux.HandleFunc("POST /api/public/signup/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, http.StatusCreated, map[string]any{"id": fmt.Sprintf("kolibri-%v", body["username"])})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected call to fake external service: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

type providerPlatformsWorld struct {
	north, south *models.Facility
	// jdoe is mapped to the Kolibri provider and has its learning data.
	// mmajor matches a provider user by name. newbie gets mapped by a test.
	resident, mmajor, newbie, southResident *models.User
	facAdmin, deptAdmin                     *models.User
}

func (w providerPlatformsWorld) claims(u *models.User) *handlers.Claims {
	return &handlers.Claims{UserID: u.ID, Username: u.Username, Role: u.Role, FacilityID: u.FacilityID, FacilityName: w.north.Name}
}

// seedProviderPlatforms adds three providers, an OIDC client, two courses,
// and jdoe's mapping, enrollment, milestone, outcomes and activity. The
// Kolibri provider points at the fake and its access key is stored encrypted
// with APP_KEY "test-app-key", the way the API stores it.
func seedProviderPlatforms(t *testing.T, env *TestEnv, fake *fakeExternal) providerPlatformsWorld {
	t.Helper()
	t.Setenv("APP_KEY", "test-app-key")
	t.Setenv("APP_URL", "https://unlocked.test")
	t.Setenv("PROVIDER_SERVICE_URL", fake.URL)
	t.Setenv("KRATOS_ADMIN_URL", fake.URL)
	t.Setenv("HYDRA_ADMIN_URL", fake.URL)
	// The testing server has no Kratos or HTTP client, so give it ones that
	// reach the fake.
	cfg := ory.NewConfiguration()
	cfg.Servers = ory.ServerConfigurations{{URL: fake.URL}}
	env.Server.OryClient = ory.NewAPIClient(cfg)
	env.Server.Client = &http.Client{Timeout: 5 * time.Second}

	var w providerPlatformsWorld
	var err error
	w.north, err = env.CreateTestFacility("North")
	require.NoError(t, err)
	w.south, err = env.CreateTestFacility("South")
	require.NoError(t, err)
	for _, u := range []struct {
		user     **models.User
		username string
		role     models.UserRole
		facility uint
		docID    string
	}{
		{&w.resident, "jdoe", models.Student, w.north.ID, "1001"},
		{&w.mmajor, "mmajor", models.Student, w.north.ID, "1002"},
		{&w.newbie, "newbie", models.Student, w.north.ID, "1003"},
		{&w.southResident, "southres", models.Student, w.south.ID, "1004"},
		{&w.facAdmin, "facadmin", models.FacilityAdmin, w.north.ID, ""},
		{&w.deptAdmin, "deptadmin", models.DepartmentAdmin, w.north.ID, ""},
	} {
		*u.user, err = env.CreateTestUser(u.username, u.role, u.facility, u.docID)
		require.NoError(t, err)
	}

	day := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	const kolibriKey = "MDEyMzQ1Njc4OWFiY2RlZsFxCXqsaoGNjd91872zRlhh9w==" // "admin:kolibri-pass"
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE users SET name_first = 'John', name_last = 'Doe' WHERE id = ?`, []any{w.resident.ID}},
		{`UPDATE users SET name_first = 'Mary', name_last = 'Major' WHERE id = ?`, []any{w.mmajor.ID}},
		{`INSERT INTO provider_platforms (id, type, name, account_id, access_key, base_url, state, external_auth_provider_id, created_at, updated_at) VALUES
			(1, 'kolibri', 'Kolibri', 'kolibri-facility', ?, ?, 'enabled', '', ?, ?),
			(2, 'brightspace', 'Brightspace', 'bs-account', 'client-id;refresh-token', 'https://brightspace.example', 'enabled', 'bs-auth', ?, ?),
			(3, 'canvas_cloud', 'Old Canvas', 'canvas-account', 'canvas-token', 'https://canvas.example', 'archived', '', ?, ?)`,
			[]any{kolibriKey, fake.URL, day, day, day, day, day, day}},
		{`INSERT INTO oidc_clients (id, provider_platform_id, client_id, client_name, client_secret, redirect_uris, scopes, created_at, updated_at)
			VALUES (1, 2, 'bs-client', 'Brightspace', 'bs-secret', 'https://brightspace.example/callback', 'openid offline profile email', ?, ?)`, []any{day, day}},
		{`INSERT INTO provider_user_mappings (user_id, provider_platform_id, external_user_id, external_username, authentication_provider_status, created_at, updated_at)
			VALUES (?, 1, 'k-1001', 'john.doe', 'none', ?, ?)`, []any{w.resident.ID, day, day}},
		{`INSERT INTO courses (id, provider_platform_id, name, description, external_id, thumbnail_url, type, outcome_types, external_url, total_progress_milestones, created_at, updated_at) VALUES
			(1, 1, 'Algebra Basics', 'Numbers and equations', 'k-course-1', '/thumbs/algebra.png', 'fixed_enrollment', 'grade,certificate', ?, 4, ?, ?),
			(2, 1, 'Biology', NULL, 'k-course-2', NULL, 'open_enrollment', NULL, NULL, 2, ?, ?)`,
			[]any{fake.URL + "/learn/algebra", day, day, day.Add(time.Hour), day.Add(time.Hour)}},
		{`INSERT INTO user_enrollments (user_id, course_id, external_id, created_at, updated_at) VALUES (?, 1, 'k-enroll-1', ?, ?)`,
			[]any{w.resident.ID, day, day}},
		{`INSERT INTO milestones (user_id, course_id, external_id, type, is_completed, created_at, updated_at) VALUES
			(?, 1, 'k-milestone-1', 'quiz_submission', true, ?, ?)`, []any{w.resident.ID, day, day}},
		{`INSERT INTO outcomes (type, course_id, user_id, value, created_at, updated_at) VALUES
			('grade', 1, ?, 'A', ?, ?), ('certificate', 1, ?, 'cert-1', ?, ?)`,
			[]any{w.resident.ID, day, day, w.resident.ID, day, day}},
		{`INSERT INTO activities (course_id, user_id, type, total_time, time_delta, external_id, created_at, updated_at) VALUES
			(1, ?, 'interaction', 600, 600, 'k-activity-1', ?, ?),
			(1, ?, 'completion', 900, 300, 'k-activity-2', ?, ?)`,
			[]any{w.resident.ID, day, day, w.resident.ID, day.Add(24 * time.Hour), day.Add(24 * time.Hour)}},
		{`INSERT INTO user_course_activity_totals (user_id, course_id, total_time, last_ts) VALUES (?, 1, 900, ?)`,
			[]any{w.resident.ID, day.Add(24 * time.Hour)}},
	} {
		require.NoError(t, env.DB.Exec(stmt.sql, stmt.args...).Error, stmt.sql)
	}
	return w
}

func TestProviderPlatformRoutes(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	fake := newFakeExternal(t)
	w := seedProviderPlatforms(t, env, fake)
	client := noRedirects(env)

	resident, facAdmin := w.claims(w.resident), w.claims(w.facAdmin)
	user := func(format string, u *models.User) string { return fmt.Sprintf(format, u.ID) }
	activityRange := "start_date=2026-01-01&end_date=2026-01-10"

	// The cases run in order against one database, so later reads show the
	// earlier writes. mask lists JSON fields that change every run.
	tests := []struct {
		name   string
		claims *handlers.Claims
		method string
		path   string
		body   any
		status int
		mask   []string
	}{
		// Provider platforms
		{"platforms", facAdmin, http.MethodGet, "/api/provider-platforms", nil, http.StatusOK, nil},
		{"platforms_oidc_enabled", facAdmin, http.MethodGet, "/api/provider-platforms?only=oidc_enabled", nil, http.StatusOK, nil},
		{"platform_by_id", facAdmin, http.MethodGet, "/api/provider-platforms/1", nil, http.StatusOK, nil},
		{"platform_by_id_bad_id", facAdmin, http.MethodGet, "/api/provider-platforms/abc", nil, http.StatusBadRequest, nil},
		{"platform_by_id_missing", facAdmin, http.MethodGet, "/api/provider-platforms/999", nil, http.StatusOK, nil},
		{"platform_create", facAdmin, http.MethodPost, "/api/provider-platforms",
			map[string]any{"type": "canvas_cloud", "name": "New Canvas", "account_id": "1", "access_key": "new-token", "base_url": " https://new-canvas.example ", "state": "disabled"},
			http.StatusCreated, []string{"access_key"}},
		{"platform_create_http", facAdmin, http.MethodPost, "/api/provider-platforms",
			map[string]any{"type": "canvas_cloud", "name": "Plain", "base_url": "http://plain.example"}, http.StatusBadRequest, nil},
		{"platform_create_brightspace", facAdmin, http.MethodPost, "/api/provider-platforms",
			map[string]any{"type": "brightspace", "name": "BS", "account_id": "id", "access_key": "secret", "base_url": "https://bs.example"},
			http.StatusInternalServerError, nil},
		{"platform_update", facAdmin, http.MethodPatch, "/api/provider-platforms/3", map[string]any{"name": "Older Canvas"}, http.StatusOK, nil},
		{"platform_update_http", facAdmin, http.MethodPatch, "/api/provider-platforms/3", map[string]any{"base_url": "http://plain.example"}, http.StatusBadRequest, nil},
		// Brightspace needs the OAuth state bucket, which the tests don't run.
		{"platform_refresh", facAdmin, http.MethodGet, "/api/provider-platforms/2/refresh", nil, http.StatusInternalServerError, nil},
		{"platform_callback_no_state", facAdmin, http.MethodGet, "/api/provider-platforms/callback", nil, http.StatusTemporaryRedirect, nil},
		{"platform_delete", facAdmin, http.MethodDelete, "/api/provider-platforms/4", nil, http.StatusNoContent, nil},
		{"platforms_after_changes", facAdmin, http.MethodGet, "/api/provider-platforms", nil, http.StatusOK, nil},

		// Logins and mappings
		{"logins", facAdmin, http.MethodGet, user("/api/users/%d/logins", w.resident), nil, http.StatusOK, nil},
		{"login_create", facAdmin, http.MethodPost, user("/api/users/%d/logins", w.southResident),
			map[string]any{"user_id": w.southResident.ID, "provider_platform_id": 1, "external_user_id": "k-3001", "external_username": "south.res"},
			http.StatusCreated, nil},
		{"logins_after_create", facAdmin, http.MethodGet, user("/api/users/%d/logins", w.southResident), nil, http.StatusOK, nil},
		{"login_delete", facAdmin, http.MethodDelete, user("/api/users/%d/logins/1", w.southResident), nil, http.StatusNoContent, nil},
		{"logins_after_delete", facAdmin, http.MethodGet, user("/api/users/%d/logins", w.southResident), nil, http.StatusOK, nil},
		{"mapped_users", facAdmin, http.MethodGet, "/api/provider-platforms/1/mapped-users", nil, http.StatusOK, nil},
		// A core route: the residents an admin can still map.
		{"users_only_unmapped", facAdmin, http.MethodGet, "/api/users?include=only_unmapped&provider_id=1", nil, http.StatusOK, nil},
		{"map_user", facAdmin, http.MethodPost, user("/api/provider-platforms/1/map-user/%d", w.newbie),
			map[string]any{"external_user_id": "k-4001", "external_username": "new.bie"}, http.StatusCreated, nil},
		{"map_user_bad_body", facAdmin, http.MethodPost, user("/api/provider-platforms/1/map-user/%d", w.newbie), "not an object", http.StatusBadRequest, nil},
		{"create_user_account", facAdmin, http.MethodPost, user("/api/provider-platforms/1/create-user/%d", w.southResident), nil, http.StatusCreated, nil},
		{"user_account", facAdmin, http.MethodPost, user("/api/provider-platforms/1/user-accounts/%d", w.southResident), nil, http.StatusCreated, nil},
		{"users_import", facAdmin, http.MethodPost, "/api/provider-platforms/1/users/import",
			map[string]any{"users": []providerUser{
				{Username: "pat.lee", NameFirst: "Pat", NameLast: "Lee", Email: "pat@example.com", ExternalUserID: "k-5001", ExternalUsername: "pat.lee"},
				{Username: "jdoe", NameFirst: "Duplicate", NameLast: "Name", ExternalUserID: "k-5002"},
			}}, http.StatusOK, []string{"temp_password"}},
		{"users_import_bad_body", facAdmin, http.MethodPost, "/api/provider-platforms/1/users/import", "not an object", http.StatusBadRequest, nil},

		// Actions: the worker's provider users and matching them to residents
		{"action_get_users", facAdmin, http.MethodGet, "/api/actions/provider-platforms/1/get-users", nil, http.StatusOK, nil},
		{"action_get_users_search", facAdmin, http.MethodGet, "/api/actions/provider-platforms/1/get-users?search=Jane", nil, http.StatusOK, nil},
		{"action_get_users_bad_id", facAdmin, http.MethodGet, "/api/actions/provider-platforms/abc/get-users", nil, http.StatusBadRequest, nil},
		{"action_match_users", facAdmin, http.MethodGet, "/api/actions/provider-platforms/1/match-users", nil, http.StatusOK, nil},
		{"action_apply_matches", facAdmin, http.MethodPost, "/api/actions/provider-platforms/1/apply-matches",
			map[string]any{
				"confirmed": []map[string]any{{"canvas_user": unmappedProviderUsers[0], "unlocked_user_id": w.mmajor.ID}},
				"to_create": []providerUser{unmappedProviderUsers[1]},
			}, http.StatusOK, nil},
		{"action_apply_matches_other_facility", facAdmin, http.MethodPost, "/api/actions/provider-platforms/1/apply-matches",
			map[string]any{"confirmed": []map[string]any{{"canvas_user": unmappedProviderUsers[0], "unlocked_user_id": w.southResident.ID}}},
			http.StatusOK, nil},
		{"action_import_users", facAdmin, http.MethodPost, "/api/actions/provider-platforms/1/import-users", nil, http.StatusOK, nil},
		{"mapped_users_after_actions", facAdmin, http.MethodGet, "/api/provider-platforms/1/mapped-users?per_page=50", nil, http.StatusOK, nil},

		// OIDC clients
		{"oidc_clients", facAdmin, http.MethodGet, "/api/oidc/clients", nil, http.StatusOK, nil},
		{"oidc_client", facAdmin, http.MethodGet, "/api/oidc/clients/1", nil, http.StatusOK, nil},
		{"oidc_register", facAdmin, http.MethodPost, "/api/oidc/clients", map[string]any{"provider_platform_id": 1}, http.StatusCreated, nil},
		{"oidc_register_again", facAdmin, http.MethodPost, "/api/oidc/clients", map[string]any{"provider_platform_id": 1}, http.StatusBadRequest, nil},
		{"oidc_recreate", facAdmin, http.MethodPost, "/api/oidc/clients/1/recreate", nil, http.StatusCreated, nil},
		{"oidc_recreate_bad_id", facAdmin, http.MethodPost, "/api/oidc/clients/abc/recreate", nil, http.StatusBadRequest, nil},
		{"oidc_clients_after_changes", facAdmin, http.MethodGet, "/api/oidc/clients", nil, http.StatusOK, nil},
		{"platforms_oidc_enabled_after_changes", facAdmin, http.MethodGet, "/api/provider-platforms?only=oidc_enabled", nil, http.StatusOK, nil},

		// Courses, outcomes and activity
		{"courses", facAdmin, http.MethodGet, "/api/courses", nil, http.StatusOK, nil},
		{"courses_search", facAdmin, http.MethodGet, "/api/courses?search=bio", nil, http.StatusOK, nil},
		{"course_by_id", resident, http.MethodGet, "/api/courses/1", nil, http.StatusOK, nil},
		{"course_by_id_bad_id", resident, http.MethodGet, "/api/courses/abc", nil, http.StatusBadRequest, nil},
		{"course_activity", facAdmin, http.MethodGet, "/api/courses/1/activity", nil, http.StatusOK, nil},
		{"outcomes", resident, http.MethodGet, user("/api/users/%d/outcomes", w.resident), nil, http.StatusOK, nil},
		{"outcomes_by_type", resident, http.MethodGet, user("/api/users/%d/outcomes?type=certificate", w.resident), nil, http.StatusOK, nil},
		{"daily_activity", resident, http.MethodGet, user("/api/users/%d/daily-activity?", w.resident) + activityRange, nil, http.StatusOK, nil},
		{"daily_activity_bad_date", resident, http.MethodGet, user("/api/users/%d/daily-activity?start_date=soon", w.resident), nil, http.StatusBadRequest, nil},
		{"catalog", resident, http.MethodGet, user("/api/users/%d/catalog", w.resident), nil, http.StatusOK, nil},
		{"catalog_search", resident, http.MethodGet, user("/api/users/%d/catalog?search=bio", w.resident), nil, http.StatusOK, nil},
		{"user_courses", resident, http.MethodGet, user("/api/users/%d/courses", w.resident), nil, http.StatusOK, nil},

		// Core routes that reach into Provider Platforms
		{"user_create_with_provider", facAdmin, http.MethodPost, "/api/users",
			map[string]any{
				"user":               map[string]any{"username": "kimuser", "name_first": "Kim", "name_last": "User", "role": "student", "doc_id": "2001"},
				"provider_platforms": []int{1},
			}, http.StatusCreated, []string{"temp_password"}},
		{"user_delete", facAdmin, http.MethodDelete, user("/api/users/%d", w.newbie), nil, http.StatusNoContent, nil},
		{"logins_after_user_delete", facAdmin, http.MethodGet, user("/api/users/%d/logins", w.newbie), nil, http.StatusOK, nil},
		{"mapped_users_after_core_changes", facAdmin, http.MethodGet, "/api/provider-platforms/1/mapped-users?per_page=50", nil, http.StatusOK, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := NewRequest[any](client, t, tt.method, tt.path, tt.body).
				WithTestClaims(tt.claims).AsRaw().Do().
				ExpectStatus(tt.status)
			body := strings.ReplaceAll(resp.rawBody, fake.URL, "http://fake.test")
			expectGolden(t, "provider_platforms", tt.name, maskFields(t, body, tt.mask...))
		})
	}

	// platform_create masks the access key because it's encrypted with a
	// random IV, so check here that it isn't stored as sent.
	var stored string
	require.NoError(t, env.DB.Raw(`SELECT access_key FROM provider_platforms WHERE name = 'New Canvas'`).Scan(&stored).Error)
	require.NotEmpty(t, stored)
	require.NotEqual(t, "new-token", stored)
}

// TestProviderPlatformRouteAccess pins each route's access level and feature
// flag, the part of a route most easily lost in a move.
func TestProviderPlatformRouteAccess(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	fake := newFakeExternal(t)
	w := seedProviderPlatforms(t, env, fake)
	client := noRedirects(env)

	const (
		anyUser = iota
		admin
		// facilityAdminOfUser is admins of the facility of the user in the
		// path, and department and system admins.
		facilityAdminOfUser
		// selfOrAdmin is the user in the path, admins of that user's facility,
		// and department and system admins.
		selfOrAdmin
	)
	// pathFor builds a route's path for the user in it.
	type pathFor func(u *models.User) string
	fixed := func(path string) pathFor { return func(*models.User) string { return path } }
	forUser := func(format string) pathFor { return func(u *models.User) string { return fmt.Sprintf(format, u.ID) } }

	routes := []struct {
		method string
		path   pathFor
		access int
	}{
		{http.MethodGet, fixed("/api/provider-platforms"), admin},
		{http.MethodGet, fixed("/api/provider-platforms/1"), admin},
		{http.MethodPost, fixed("/api/provider-platforms"), admin},
		{http.MethodGet, fixed("/api/provider-platforms/callback"), admin},
		{http.MethodGet, fixed("/api/provider-platforms/1/refresh"), admin},
		{http.MethodPatch, fixed("/api/provider-platforms/1"), admin},
		{http.MethodDelete, fixed("/api/provider-platforms/1"), admin},
		{http.MethodPost, forUser("/api/provider-platforms/1/map-user/%d"), facilityAdminOfUser},
		{http.MethodPost, fixed("/api/provider-platforms/1/users/import"), admin},
		{http.MethodPost, fixed("/api/provider-platforms/1/create-user/1"), admin},
		{http.MethodGet, fixed("/api/users/1/logins"), admin},
		{http.MethodPost, fixed("/api/users/1/logins"), admin},
		{http.MethodPost, fixed("/api/provider-platforms/1/user-accounts/1"), admin},
		{http.MethodDelete, fixed("/api/users/1/logins/1"), admin},
		{http.MethodGet, fixed("/api/provider-platforms/1/mapped-users"), admin},
		{http.MethodGet, fixed("/api/actions/provider-platforms/1/get-users"), admin},
		{http.MethodPost, fixed("/api/actions/provider-platforms/1/import-users"), admin},
		{http.MethodGet, fixed("/api/actions/provider-platforms/1/match-users"), admin},
		{http.MethodPost, fixed("/api/actions/provider-platforms/1/apply-matches"), admin},
		{http.MethodGet, fixed("/api/oidc/clients"), admin},
		{http.MethodPost, fixed("/api/oidc/clients"), admin},
		{http.MethodGet, fixed("/api/oidc/clients/1"), admin},
		{http.MethodPost, fixed("/api/oidc/clients/1/recreate"), admin},
		{http.MethodGet, fixed("/api/courses"), admin},
		{http.MethodGet, fixed("/api/courses/1"), anyUser},
		{http.MethodGet, fixed("/api/courses/1/activity"), admin},
		{http.MethodGet, forUser("/api/users/%d/outcomes"), anyUser},
		{http.MethodGet, forUser("/api/users/%d/daily-activity"), selfOrAdmin},
		{http.MethodGet, forUser("/api/users/%d/catalog"), selfOrAdmin},
		{http.MethodGet, forUser("/api/users/%d/courses"), selfOrAdmin},
	}

	// passes asserts the caller gets past every access check. The body isn't
	// valid JSON, so a route that reads one stops there without side effects.
	passes := func(t *testing.T, method, path string, c *handlers.Claims) {
		t.Helper()
		resp := NewRequest[any](client, t, method, path, "not an object").
			WithTestClaims(c).AsRaw().Do()
		require.NotEqual(t, http.StatusUnauthorized, resp.resp.StatusCode, resp.rawBody)
	}
	notAllowed := func(t *testing.T, method, path string, c *handlers.Claims) {
		t.Helper()
		NewRequest[any](client, t, method, path, map[string]any{}).
			WithTestClaims(c).AsRaw().Do().
			ExpectStatus(http.StatusUnauthorized).
			ExpectRaw("User is not allowed to view this resource\n")
	}
	notAdmin := func(t *testing.T, method, path string, c *handlers.Claims) {
		t.Helper()
		NewRequest[any](client, t, method, path, map[string]any{}).
			WithTestClaims(c).Do().
			ExpectStatus(http.StatusUnauthorized).ExpectMessage("Unauthorized - not admin")
	}

	resident, southResident := w.claims(w.resident), w.claims(w.southResident)
	facAdmin, deptAdmin := w.claims(w.facAdmin), w.claims(w.deptAdmin)
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path(w.resident), func(t *testing.T) {
			// A department admin passes every role check, so a 401 here is
			// the feature check.
			NewRequest[any](client, t, rt.method, rt.path(w.resident), map[string]any{}).
				WithTestClaims(withFeatures(deptAdmin, models.ProviderAccess)).Do().
				ExpectStatus(http.StatusUnauthorized).ExpectMessage("Feature not enabled")

			switch rt.access {
			case anyUser:
				// Any user, even for another user's data.
				passes(t, rt.method, rt.path(w.resident), southResident)
			case admin:
				notAdmin(t, rt.method, rt.path(w.resident), resident)
			case facilityAdminOfUser:
				notAdmin(t, rt.method, rt.path(w.resident), resident)
				notAllowed(t, rt.method, rt.path(w.southResident), facAdmin)
				passes(t, rt.method, rt.path(w.resident), facAdmin)
				passes(t, rt.method, rt.path(w.southResident), deptAdmin)
			case selfOrAdmin:
				passes(t, rt.method, rt.path(w.resident), resident)
				notAllowed(t, rt.method, rt.path(w.mmajor), resident)
				notAllowed(t, rt.method, rt.path(w.southResident), resident)
				notAllowed(t, rt.method, rt.path(w.southResident), facAdmin)
				passes(t, rt.method, rt.path(w.resident), facAdmin)
				passes(t, rt.method, rt.path(w.southResident), deptAdmin)
			}
		})
	}
}

// maskFields replaces the named JSON fields, wherever they are, with
// "<masked>" unless they're empty. A body that isn't JSON is returned as is.
func maskFields(t *testing.T, body string, keys ...string) string {
	t.Helper()
	if len(keys) == 0 {
		return body
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return body
	}
	var mask func(any)
	mask = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				if s, ok := x.(string); ok && s != "" && slices.Contains(keys, k) {
					v[k] = "<masked>"
				} else {
					mask(x)
				}
			}
		case []any:
			for _, x := range v {
				mask(x)
			}
		}
	}
	mask(v)
	out, err := json.Marshal(v)
	require.NoError(t, err)
	return string(out)
}
