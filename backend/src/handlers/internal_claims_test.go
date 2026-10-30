package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claimsWorld is a facility with its own feature override and three users
// with Kratos identities: a resident, a department admin and a deactivated
// resident.
type claimsWorld struct {
	facility                     uint
	resident, admin, deactivated *models.User
}

func seedClaimsWorld(t *testing.T, srv *Server) claimsWorld {
	t.Helper()
	srv.features = []models.FeatureAccess{models.OpenContentAccess, models.HelpfulLinksAccess, models.ProgramAccess}
	w := claimsWorld{facility: createTestFacility(t, srv, "Claims North")}
	args := models.QueryContext{Ctx: context.Background()}
	require.NoError(t, srv.Db.UpsertFacilityFeatureFlag(&args, w.facility, models.LearningRecordAccess, true, srv.features))
	newUser := func(username string, role models.UserRole) *models.User {
		u := createTestUser(t, srv, username, role, w.facility)
		kratosID := "kratos-" + username
		require.NoError(t, srv.Db.Model(u).Update("kratos_id", kratosID).Error)
		u.KratosID = &kratosID
		return u
	}
	w.resident = newUser("claimsresident", models.Student)
	w.admin = newUser("claimsadmin", models.DepartmentAdmin)
	w.deactivated = newUser("claimsgone", models.Student)
	require.NoError(t, srv.Db.Model(w.deactivated).Update("deactivated_at", time.Now()).Error)
	return w
}

func TestHandleInternalClaims(t *testing.T) {
	srv := newTestingServer()
	srv.registerInternalRoutes()
	w := seedClaimsWorld(t, srv)

	tests := []struct {
		name       string
		query      string
		wantStatus int
		want       *identity.Claims
		// The message in core's JSON error, which SessionChecker needs to tell
		// an unknown or deactivated user from a missing route.
		wantMessage string
	}{
		{
			name:       "a resident gets their facility's features",
			query:      "kratos_id=kratos-claimsresident",
			wantStatus: http.StatusOK,
			want: &identity.Claims{
				UserID: w.resident.ID, Username: "claimsresident", Role: models.Student,
				FacilityID: w.facility, FacilityName: "Claims North", TimeZone: "America/Chicago",
				FeatureAccess: []models.FeatureAccess{models.LearningRecordAccess, models.OpenContentAccess, models.HelpfulLinksAccess, models.ProgramAccess},
			},
		},
		{
			name:       "an admin gets their role",
			query:      "kratos_id=kratos-claimsadmin",
			wantStatus: http.StatusOK,
			want: &identity.Claims{
				UserID: w.admin.ID, Username: "claimsadmin", Role: models.DepartmentAdmin,
				FacilityID: w.facility, FacilityName: "Claims North", TimeZone: "America/Chicago",
				FeatureAccess: []models.FeatureAccess{models.LearningRecordAccess, models.OpenContentAccess, models.HelpfulLinksAccess, models.ProgramAccess},
			},
		},
		{
			name:        "a deactivated user is refused",
			query:       "kratos_id=kratos-claimsgone",
			wantStatus:  http.StatusForbidden,
			wantMessage: "account deactivated",
		},
		{
			name:        "an unknown identity is not found",
			query:       "kratos_id=kratos-nobody",
			wantStatus:  http.StatusNotFound,
			wantMessage: "user not found",
		},
		{
			name:       "a missing kratos_id is rejected",
			query:      "",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/v1/claims?"+tt.query, nil))

			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			if tt.wantMessage != "" {
				var resp models.Resource[any]
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
				assert.Equal(t, tt.wantMessage, resp.Message)
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			var resp models.Resource[identity.Claims]
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.ElementsMatch(t, tt.want.FeatureAccess, resp.Data.FeatureAccess)
			resp.Data.FeatureAccess, tt.want.FeatureAccess = nil, nil
			assert.Equal(t, *tt.want, resp.Data)
		})
	}
}

// For the same session, a product service using unlocked-contracts'
// identity.SessionChecker gets exactly the identity.Claims core's own login
// check hands its modules, and turns away the same users.
func TestSessionCheckerMatchesCoreLoginCheck(t *testing.T) {
	srv := newTestingServer()
	srv.registerInternalRoutes()
	w := seedClaimsWorld(t, srv)
	core := httptest.NewServer(srv.Mux)
	t.Cleanup(core.Close)

	// A fake Kratos whose session cookie value is the identity's id.
	kratos := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("ory_kratos_session")
		if err != nil || r.URL.Path != "/sessions/whoami" {
			rw.WriteHeader(http.StatusUnauthorized)
			return
		}
		var role string
		for _, u := range []*models.User{w.resident, w.admin, w.deactivated} {
			if *u.KratosID == c.Value {
				role = string(u.Role)
			}
		}
		_, _ = fmt.Fprintf(rw, `{"id":"session-%s","active":true,"identity":{"id":%q,"traits":{"password_reset":false,"role":%q}}}`,
			c.Value, c.Value, role)
	}))
	t.Cleanup(kratos.Close)
	t.Setenv("KRATOS_PUBLIC_URL", kratos.URL)
	srv.Client = kratos.Client()

	sessions := identity.NewSessionChecker(kratos.URL, core.URL, nil)

	tests := []struct {
		name     string
		kratosID string
		loggedIn bool
	}{
		{"a resident", *w.resident.KratosID, true},
		{"a department admin", *w.admin.KratosID, true},
		{"a deactivated user", *w.deactivated.KratosID, false},
		{"an identity core doesn't know", "kratos-nobody", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/things", nil)
			req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: tt.kratosID})

			coreClaims, _, err := srv.validateOrySession(req)
			require.Equal(t, tt.loggedIn, err == nil, "core's login check: %v", err)

			var got *identity.Claims
			rec := httptest.NewRecorder()
			sessions.Require(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, _ = identity.FromContext(r.Context())
			})).ServeHTTP(rec, req)

			if !tt.loggedIn {
				assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
				assert.Nil(t, got)
				return
			}
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, coreClaims.identityClaims(), got)

			coreRec := httptest.NewRecorder()
			require.NoError(t, srv.ensureCSRFToken(coreRec, req))
			assert.Equal(t, csrfCookie(t, coreRec), csrfCookie(t, rec), "the csrf_token cookie differs from core's")
		})
	}

	// Core's mux answers a route it doesn't have with a plain 404, which must
	// not read as "no such user" and log everyone out.
	t.Run("a core without the claims route", func(t *testing.T) {
		bare := httptest.NewServer(newTestingServer().Mux)
		t.Cleanup(bare.Close)
		req := httptest.NewRequest(http.MethodGet, "/api/things", nil)
		req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: *w.resident.KratosID})
		rec := httptest.NewRecorder()
		identity.NewSessionChecker(kratos.URL, bare.URL, nil).Require(http.NotFoundHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	})
}

// csrfCookie is the csrf_token cookie a response set, without its random value.
func csrfCookie(t *testing.T, rec *httptest.ResponseRecorder) http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf_token" {
			c.Value, c.Raw = "", ""
			return *c
		}
	}
	t.Fatal("no csrf_token cookie was set")
	return http.Cookie{}
}
