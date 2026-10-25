package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	ory "github.com/ory/kratos-client-go"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A module route's access level must get the same checks as core's own
// routes, through the real middleware.
func TestModuleRouterAccess(t *testing.T) {
	srv := newTestingServer()
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	router := moduleRouter{srv}
	levels := map[module.AccessLevel]string{
		module.AnyUser:     "/api/test-module/any-user",
		module.Admin:       "/api/test-module/admin",
		module.DeptAdmin:   "/api/test-module/dept-admin",
		module.SystemAdmin: "/api/test-module/system-admin",
	}
	for level, path := range levels {
		router.Handle(module.Route{Pattern: "GET " + path, Handler: ok, Access: level})
	}
	router.Handle(module.Route{Pattern: "GET /api/test-module/feature", Handler: ok, Features: []models.FeatureAccess{models.OpenContentAccess}})

	allowed, denied := http.StatusOK, http.StatusUnauthorized
	tests := []struct {
		role models.UserRole
		want map[module.AccessLevel]int
	}{
		{models.Student, map[module.AccessLevel]int{module.AnyUser: allowed, module.Admin: denied, module.DeptAdmin: denied, module.SystemAdmin: denied}},
		{models.FacilityAdmin, map[module.AccessLevel]int{module.AnyUser: allowed, module.Admin: allowed, module.DeptAdmin: denied, module.SystemAdmin: denied}},
		{models.DepartmentAdmin, map[module.AccessLevel]int{module.AnyUser: allowed, module.Admin: allowed, module.DeptAdmin: allowed, module.SystemAdmin: denied}},
		{models.SystemAdmin, map[module.AccessLevel]int{module.AnyUser: allowed, module.Admin: allowed, module.DeptAdmin: allowed, module.SystemAdmin: allowed}},
	}
	for _, tt := range tests {
		for level, path := range levels {
			t.Run(fmt.Sprintf("%s on %s", tt.role, path), func(t *testing.T) {
				w := httptest.NewRecorder()
				srv.Mux.ServeHTTP(w, requestAs(t, path, Claims{UserID: 1, FacilityID: 1, Role: tt.role}))
				assert.Equal(t, tt.want[level], w.Code, w.Body.String())
			})
		}
	}

	t.Run("a route's features are still checked", func(t *testing.T) {
		w := httptest.NewRecorder()
		srv.Mux.ServeHTTP(w, requestAs(t, "/api/test-module/feature", Claims{UserID: 1, FacilityID: 1, Role: models.SystemAdmin, FeatureAccess: []models.FeatureAccess{}}))
		assert.Equal(t, denied, w.Code, w.Body.String())
	})
}

// A route with a UserParam gets the same check as core's routes for the user
// in their path: the user themself (AnyUser only) and the admins who can see
// that user.
func TestModuleRouterUserParam(t *testing.T) {
	srv := newTestingServer()
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	router := moduleRouter{srv}
	router.Handle(module.Route{Pattern: "GET /api/test-module/users/{id}/self", Handler: ok, UserParam: "id"})
	router.Handle(module.Route{Pattern: "GET /api/test-module/users/{user_id}/admin", Handler: ok, Access: module.Admin, UserParam: "user_id"})

	north, south := createTestFacility(t, srv, "North"), createTestFacility(t, srv, "South")
	resident := createTestUser(t, srv, "resident", models.Student, north)
	neighbor := createTestUser(t, srv, "neighbor", models.Student, north)
	southResident := createTestUser(t, srv, "southres", models.Student, south)
	as := func(u *models.User) Claims { return Claims{UserID: u.ID, Role: u.Role, FacilityID: u.FacilityID} }
	facAdmin := as(createTestUser(t, srv, "facadmin", models.FacilityAdmin, north))
	deptAdmin := as(createTestUser(t, srv, "deptadmin", models.DepartmentAdmin, north))

	allowed, denied := http.StatusOK, http.StatusUnauthorized
	tests := []struct {
		name   string
		route  string
		caller Claims
		user   *models.User
		want   int
	}{
		{"user themself", "self", as(resident), resident, allowed},
		{"another user, same facility", "self", as(resident), neighbor, denied},
		{"another user, other facility", "self", as(resident), southResident, denied},
		{"facility admin, own facility", "self", facAdmin, resident, allowed},
		{"facility admin, other facility", "self", facAdmin, southResident, denied},
		{"department admin, any facility", "self", deptAdmin, southResident, allowed},
		{"admin route, user themself", "admin", as(resident), resident, denied},
		{"admin route, facility admin, own facility", "admin", facAdmin, resident, allowed},
		{"admin route, facility admin, other facility", "admin", facAdmin, southResident, denied},
		{"admin route, department admin, any facility", "admin", deptAdmin, southResident, allowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			path := fmt.Sprintf("/api/test-module/users/%d/%s", tt.user.ID, tt.route)
			srv.Mux.ServeHTTP(w, requestAs(t, path, tt.caller))
			assert.Equal(t, tt.want, w.Code, w.Body.String())
		})
	}
}

// A UserParam that can't be checked stops startup.
func TestModuleRouterUserParamPanics(t *testing.T) {
	ok := http.NotFoundHandler()
	tests := []struct {
		name  string
		route module.Route
	}{
		{"department admins", module.Route{Pattern: "GET /api/test-module/{id}", Handler: ok, Access: module.DeptAdmin, UserParam: "id"}},
		{"system admins", module.Route{Pattern: "GET /api/test-module/{id}", Handler: ok, Access: module.SystemAdmin, UserParam: "id"}},
		{"not in the path", module.Route{Pattern: "GET /api/test-module/{user_id}", Handler: ok, UserParam: "id"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := moduleRouter{newTestingServer()}
			assert.Panics(t, func() { router.Handle(tt.route) })
		})
	}
}

// A route's Allow sees the request and the caller, and the caller must pass it
// as well as the route's access level, UserParam and features.
func TestModuleRouterAllow(t *testing.T) {
	srv := newTestingServer()
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	north, south := createTestFacility(t, srv, "North"), createTestFacility(t, srv, "South")
	allow := func(r *http.Request, c *identity.Claims) bool {
		return r.PathValue("class") == "open" && c.FacilityID == north
	}
	router := moduleRouter{srv}
	router.Handle(module.Route{Pattern: "GET /api/test-module/classes/{class}", Handler: ok, Allow: allow})
	router.Handle(module.Route{Pattern: "GET /api/test-module/users/{id}/classes/{class}", Handler: ok, UserParam: "id", Allow: allow})
	router.Handle(module.Route{Pattern: "GET /api/test-module/dept-admin/classes/{class}", Handler: ok, Access: module.DeptAdmin, Allow: allow})
	router.Handle(module.Route{Pattern: "GET /api/test-module/feature/classes/{class}", Handler: ok, Allow: allow, Features: []models.FeatureAccess{models.OpenContentAccess}})

	resident := createTestUser(t, srv, "resident", models.Student, north)
	neighbor := createTestUser(t, srv, "neighbor", models.Student, north)
	southResident := createTestUser(t, srv, "southres", models.Student, south)
	as := func(u *models.User) Claims { return Claims{UserID: u.ID, Role: u.Role, FacilityID: u.FacilityID} }
	facAdmin := as(createTestUser(t, srv, "facadmin", models.FacilityAdmin, north))
	deptAdmin := as(createTestUser(t, srv, "deptadmin", models.DepartmentAdmin, north))
	// The testing server reads nil FeatureAccess as every feature.
	withFeatures := func(c Claims, fs ...models.FeatureAccess) Claims {
		c.FeatureAccess = append([]models.FeatureAccess{}, fs...)
		return c
	}

	allowed, denied := http.StatusOK, http.StatusUnauthorized
	tests := []struct {
		name   string
		path   string
		caller Claims
		want   int
	}{
		{"allowed", "/api/test-module/classes/open", as(resident), allowed},
		{"denied by the path", "/api/test-module/classes/closed", as(resident), denied},
		{"denied by the caller", "/api/test-module/classes/open", as(southResident), denied},
		{"user param and allow pass", fmt.Sprintf("/api/test-module/users/%d/classes/open", resident.ID), as(resident), allowed},
		{"user param fails", fmt.Sprintf("/api/test-module/users/%d/classes/open", neighbor.ID), as(resident), denied},
		{"user param passes, allow fails", fmt.Sprintf("/api/test-module/users/%d/classes/closed", resident.ID), as(resident), denied},
		{"department admin and allow pass", "/api/test-module/dept-admin/classes/open", deptAdmin, allowed},
		{"access level fails", "/api/test-module/dept-admin/classes/open", facAdmin, denied},
		{"access level passes, allow fails", "/api/test-module/dept-admin/classes/closed", deptAdmin, denied},
		{"feature and allow pass", "/api/test-module/feature/classes/open", withFeatures(as(resident), models.OpenContentAccess), allowed},
		{"feature off", "/api/test-module/feature/classes/open", withFeatures(as(resident)), denied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.Mux.ServeHTTP(w, requestAs(t, tt.path, tt.caller))
			assert.Equal(t, tt.want, w.Code, w.Body.String())
		})
	}

	t.Run("denied with core's response", func(t *testing.T) {
		w := httptest.NewRecorder()
		srv.Mux.ServeHTTP(w, requestAs(t, "/api/test-module/classes/closed", as(resident)))
		assert.Equal(t, denied, w.Code)
		assert.Equal(t, "User is not allowed to view this resource\n", w.Body.String())
	})
}

// An access level core doesn't know stops startup rather than leaving the
// route open.
func TestModuleRouterUnknownAccessPanics(t *testing.T) {
	router := moduleRouter{newTestingServer()}
	assert.Panics(t, func() {
		router.Handle(module.Route{Pattern: "GET /api/test-module/unknown", Handler: http.NotFoundHandler(), Access: module.AccessLevel(99)})
	})
}

// requestAs builds a GET with the claims the testing server's auth middleware
// reads instead of a Kratos session.
func requestAs(t *testing.T, path string, claims Claims) *http.Request {
	t.Helper()
	body, err := json.Marshal(claims)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("X-Test-Claims", string(body))
	return r
}

// A module handler sees who is calling as identity.Claims, without core's
// Kratos and password-reset details.
func TestModuleRouterClaims(t *testing.T) {
	srv := newTestingServer()
	var got *identity.Claims
	moduleRouter{srv}.Handle(module.Route{Pattern: "GET /api/test-module/claims", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = identity.FromContext(r.Context())
	})})

	caller := Claims{
		UserID: 7, Username: "jdoe", Email: "jdoe@example.com", Role: models.Student, FacilityID: 2,
		FacilityName: "North", KratosID: "k-1", FeatureAccess: []models.FeatureAccess{models.OpenContentAccess},
		SessionID: "s-1", DocID: "1001", TimeZone: "America/Chicago",
	}
	srv.Mux.ServeHTTP(httptest.NewRecorder(), requestAs(t, "/api/test-module/claims", caller))

	want := &identity.Claims{
		UserID: 7, Username: "jdoe", Role: models.Student, FacilityID: 2, FacilityName: "North",
		FeatureAccess: []models.FeatureAccess{models.OpenContentAccess}, SessionID: "s-1", TimeZone: "America/Chicago",
	}
	assert.Equal(t, want, got)
}

// A module's log fields and audit action land on the same admin audit log
// line as a core handler's log.add and log.auditDetails, and the module sees
// core's fields, such as the admin, for its own log lines.
func TestModuleRouterLogFields(t *testing.T) {
	srv := newTestingServer()
	var coreFields map[string]any
	moduleRouter{srv}.Handle(module.Route{Pattern: "PUT /api/test-module/audit", Access: module.Admin, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		coreFields = module.LogFields(r.Context())
		module.LogField(r.Context(), "video_id", 9)
		module.AuditAction(r.Context(), "video_featured")
	})})

	oldHooks := logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))
	defer logrus.StandardLogger().ReplaceHooks(oldHooks)
	hook := logtest.NewGlobal()

	r := requestAs(t, "/api/test-module/audit", Claims{UserID: 3, Username: "facadmin", Role: models.FacilityAdmin, FacilityID: 2})
	r.Method = http.MethodPut
	r.Header.Set("X-CSRF-Token", "token")
	r.AddCookie(&http.Cookie{Name: "csrf_token", Value: "token"})
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var audit *logrus.Entry
	for _, e := range hook.AllEntries() {
		if e.Data["audit"] == "admin_action" {
			audit = e
		}
	}
	require.NotNil(t, audit, "no admin audit log line")
	assert.Equal(t, "video_featured", audit.Data["action"])
	assert.Equal(t, 9, audit.Data["video_id"])
	assert.Equal(t, uint(3), audit.Data["admin_id"])
	assert.Equal(t, uint(3), coreFields["admin_id"])
	assert.Equal(t, "facadmin", coreFields["username"])
}

// Module messages reach the user's open tab as the websocket's own message
// shape, and are dropped when the server has no websocket.
func TestWsNotifier(t *testing.T) {
	msg := module.Message{Event: "visits", ActivityID: 42, Text: "hi"}

	t.Run("no websocket", func(t *testing.T) {
		assert.NotPanics(t, func() { wsNotifier{newTestingServer()}.Notify(7, msg) })
	})

	t.Run("open tab", func(t *testing.T) {
		srv := newTestingServer()
		srv.wsClient = newClientManager()
		client := &WsClient{UserID: 7, sendChan: make(chan []byte, 1)}
		srv.wsClient.clients[7] = client

		wsNotifier{srv}.Notify(7, msg)

		assert.JSONEq(t, `{"event_type":"visits","user_id":7,"session_id":"","msg":{"activity_id":42,"msg":"hi"}}`, string(<-client.sendChan))
		// The websocket ends this activity when the tab closes.
		assert.Equal(t, int64(42), client.OpenContentActivityID)
	})
}

func TestModuleDeps(t *testing.T) {
	deps := newTestingServer().moduleDeps()
	assert.NotNil(t, deps.Notify)
	assert.NotNil(t, deps.Users)
	require.NotNil(t, deps.LiveProviders)
	// The testing server has no Canvas cache, so there is nothing to clear.
	assert.NotPanics(t, func() { deps.LiveProviders.MappingsChanged(1, 1) })
	assert.NotNil(t, deps.HTTP)
	assert.NotNil(t, deps.Features)
}

// A module request goes out with the client the server has when it is sent,
// so a client set after startup, such as a test's fake, is the one used.
func TestHTTPClient(t *testing.T) {
	srv := newTestingServer()
	client := srv.moduleDeps().HTTP
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer ts.Close()
	// Only the fake's own client trusts its certificate.
	srv.Client = ts.Client()

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusTeapot, resp.StatusCode)
}

// A module sees a facility's features as core resolves them: the facility's
// own settings over the statewide ones, without sub-features of a feature
// that is off.
func TestFacilityFeatures(t *testing.T) {
	srv := newTestingServer()
	srv.features = []models.FeatureAccess{models.OpenContentAccess, models.HelpfulLinksAccess, models.ProviderAccess}
	features := srv.moduleDeps().Features

	tests := []struct {
		name      string
		overrides map[models.FeatureAccess]bool
		want      []models.FeatureAccess
	}{
		{"statewide", nil, srv.features},
		{"turned on at the facility", map[models.FeatureAccess]bool{models.ProgramAccess: true}, append([]models.FeatureAccess{models.ProgramAccess}, srv.features...)},
		{"turned off at the facility", map[models.FeatureAccess]bool{models.ProviderAccess: false}, []models.FeatureAccess{models.OpenContentAccess, models.HelpfulLinksAccess}},
		{"parent turned off", map[models.FeatureAccess]bool{models.OpenContentAccess: false}, []models.FeatureAccess{models.ProviderAccess}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facility := createTestFacility(t, srv, tt.name)
			args := models.QueryContext{Ctx: context.Background()}
			for feature, enabled := range tt.overrides {
				require.NoError(t, srv.Db.UpsertFacilityFeatureFlag(&args, facility, feature, enabled, srv.features))
			}
			got, err := features.FacilityFeatures(context.Background(), facility)
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

// Only a provider that lists its own users (Essential Ed) answers; the rest
// fall back to provider-middleware.
func TestLiveProvidersUsers(t *testing.T) {
	srv := newTestingServer()
	status := http.StatusOK
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(envelope([]map[string]interface{}{
			{"id": float64(4329946), "first_name": "Jane", "last_name": "Smith", "username": "jsmith", "email_address": "jane@example.com"},
		}, true))
	}))
	defer ts.Close()
	srv.Client = ts.Client()
	const essentialEd, kolibri = 1, 2
	require.NoError(t, srv.Db.Exec(`INSERT INTO provider_platforms (id, type, name, account_id, access_key, base_url, state) VALUES
		(?, 'essential_ed', 'Essential Ed', '303', 'token', ?, 'enabled'), (?, 'kolibri', 'Kolibri', '', '', ?, 'enabled')`,
		essentialEd, ts.URL, kolibri, ts.URL).Error)
	live := liveProviders{srv}

	t.Run("essential ed lists its users", func(t *testing.T) {
		users, ok, err := live.Users(context.Background(), essentialEd)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, []module.ProviderUser{{
			Username: "jsmith", NameFirst: "Jane", NameLast: "Smith", Email: "jane@example.com",
			ExternalUserID: "4329946", ExternalUsername: "jsmith",
		}}, users)
	})

	t.Run("kolibri goes through provider-middleware", func(t *testing.T) {
		users, ok, err := live.Users(context.Background(), kolibri)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, users)
	})

	t.Run("a failed listing is an error", func(t *testing.T) {
		status = http.StatusBadGateway
		_, ok, err := live.Users(context.Background(), essentialEd)
		assert.Error(t, err)
		assert.True(t, ok)
	})
}

func TestModuleUsers(t *testing.T) {
	srv := newTestingServer()
	users := moduleUsers{srv}
	north := createTestFacility(t, srv, "North")
	admin := createTestUser(t, srv, "facadmin", models.FacilityAdmin, north)
	ctx := identity.NewContext(context.Background(), &identity.Claims{UserID: admin.ID})

	created, err := users.Create(ctx, module.NewUser{
		Username: "jdoe", NameFirst: "John", NameLast: "Doe", Email: "jdoe@example.com", FacilityID: north, Role: models.Student,
	})
	require.NoError(t, err)
	want := module.User{ID: created.ID, Username: "jdoe", NameFirst: "John", NameLast: "Doe", Email: "jdoe@example.com", FacilityID: north}
	assert.Equal(t, want, created)

	t.Run("create records the caller as the creator", func(t *testing.T) {
		stored, err := srv.Db.GetUserByID(created.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.CreateUserID)
		assert.Equal(t, admin.ID, *stored.CreateUserID)
		assert.Equal(t, models.Student, stored.Role)
	})

	t.Run("create rejects an invalid user", func(t *testing.T) {
		_, err := users.Create(ctx, module.NewUser{Username: "no role", FacilityID: north})
		assert.Error(t, err)
	})

	t.Run("get", func(t *testing.T) {
		got, err := users.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("get a missing user", func(t *testing.T) {
		_, err := users.Get(ctx, 9999)
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	})

	t.Run("create login", func(t *testing.T) {
		kratos := fakeKratos(t, srv, http.StatusCreated)
		password, err := users.CreateLogin(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, password, kratos.password(t))
		stored, err := srv.Db.GetUserByID(created.ID)
		require.NoError(t, err)
		assert.Equal(t, "kratos-1", models.FormatNullableString(stored.KratosID))
	})

	t.Run("create login fails when kratos does", func(t *testing.T) {
		fakeKratos(t, srv, http.StatusInternalServerError)
		password, err := users.CreateLogin(ctx, created.ID)
		assert.Error(t, err)
		assert.Empty(t, password)
	})

	t.Run("delete", func(t *testing.T) {
		require.NoError(t, users.Delete(ctx, created.ID))
		_, err := users.Get(ctx, created.ID)
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
		assert.Error(t, users.Delete(ctx, created.ID), "already deleted")
	})
}

// kratosFake is a Kratos admin API that answers identity creation with
// createStatus and records the password it is given.
type kratosFake struct {
	mu   sync.Mutex
	body map[string]any
}

func (k *kratosFake) password(t *testing.T) string {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	creds, _ := k.body["credentials"].(map[string]any)
	pw, _ := creds["password"].(map[string]any)
	config, _ := pw["config"].(map[string]any)
	password, _ := config["password"].(string)
	require.NotEmpty(t, password, "kratos was never given a password")
	return password
}

// fakeKratos points srv at a fake Kratos for the rest of the test.
func fakeKratos(t *testing.T, srv *Server, createStatus int) *kratosFake {
	t.Helper()
	k := &kratosFake{}
	identity := `{"id":"kratos-1","schema_id":"default","schema_url":"http://kratos.test/schema","traits":{}}`
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/identities", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(createStatus)
		_, _ = w.Write([]byte(identity))
	})
	mux.HandleFunc("GET /admin/identities/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(identity))
	})
	mux.HandleFunc("PUT /admin/identities/{id}", func(w http.ResponseWriter, r *http.Request) {
		k.mu.Lock()
		defer k.mu.Unlock()
		_ = json.NewDecoder(r.Body).Decode(&k.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(identity))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("KRATOS_ADMIN_URL", ts.URL)
	cfg := ory.NewConfiguration()
	cfg.Servers = ory.ServerConfigurations{{URL: ts.URL}}
	srv.OryClient = ory.NewAPIClient(cfg)
	srv.Client = ts.Client()
	return k
}

func createTestFacility(t *testing.T, srv *Server, name string) uint {
	t.Helper()
	f := &models.Facility{Name: name, Timezone: "America/Chicago"}
	require.NoError(t, srv.Db.CreateFacility(f))
	return f.ID
}

func createTestUser(t *testing.T, srv *Server, username string, role models.UserRole, facilityID uint) *models.User {
	t.Helper()
	u := &models.User{Username: username, NameFirst: "Test", NameLast: "User", Email: username + "@example.com", Role: role, FacilityID: facilityID}
	require.NoError(t, srv.Db.CreateUser(u))
	return u
}
