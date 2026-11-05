package handlers

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-contracts/providerplatforms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderPlatformsProxyForwardsWithServiceCredentials(t *testing.T) {
	var got *http.Request
	var gotBody string
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("X-From-Service", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("service body"))
	}))
	t.Cleanup(service.Close)
	proxy, err := newProviderPlatformsProxy(service.URL+"/pp", "s3cret")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/provider-platforms/7/users/import?x=1", strings.NewReader("payload"))
	req.Header.Set("Cookie", "ory_kratos_session=abc")
	req.Header.Set("Authorization", "Bearer the-browsers-own")
	req.Header.Set(identity.ClaimsHeader, `{"user_id":999}`)
	req = req.WithContext(identity.NewContext(req.Context(), &identity.Claims{UserID: 3, FacilityID: 2}))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTeapot, w.Code)
	assert.Equal(t, "service body", w.Body.String())
	assert.Equal(t, "yes", w.Header().Get("X-From-Service"))
	require.NotNil(t, got)
	assert.Equal(t, "/pp/api/provider-platforms/7/users/import", got.URL.Path)
	assert.Equal(t, "x=1", got.URL.RawQuery)
	assert.Equal(t, "payload", gotBody)
	assert.Equal(t, "Bearer s3cret", got.Header.Get("Authorization"))
	assert.Empty(t, got.Header.Get("Cookie"), "the browser's session isn't the service's business")
	claims, ok := identity.ClaimsFromRequest(got)
	require.True(t, ok)
	assert.Equal(t, uint(3), claims.UserID, "claims come from core's session check, not from the browser")
	assert.Equal(t, uint(2), claims.FacilityID)
}

func TestProviderPlatformsProxyServiceDown(t *testing.T) {
	service := httptest.NewServer(http.NotFoundHandler())
	url := service.URL
	service.Close()
	proxy, err := newProviderPlatformsProxy(url, "s3cret")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/provider-platforms", nil)
	req = req.WithContext(identity.NewContext(req.Context(), &identity.Claims{UserID: 1}))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.NotContains(t, w.Body.String(), "127.0.0.1", "the service's address stays inside")
}

// The service trusts core, so a caller core refuses must never reach it.
func TestProviderPlatformsProxyEnforcesAccessBeforeForwarding(t *testing.T) {
	var forwarded []string
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = append(forwarded, r.URL.Path)
	}))
	t.Cleanup(service.Close)
	proxy, err := newProviderPlatformsProxy(service.URL, "s3cret")
	require.NoError(t, err)
	srv := newTestingServer()
	for _, rt := range providerPlatformsRoutes(proxy) {
		moduleRouter{srv}.Handle(rt)
	}

	north, south := createTestFacility(t, srv, "North"), createTestFacility(t, srv, "South")
	resident := createTestUser(t, srv, "resident", models.Student, north)
	neighbor := createTestUser(t, srv, "neighbor", models.Student, north)
	southResident := createTestUser(t, srv, "southres", models.Student, south)
	as := func(u *models.User) Claims { return Claims{UserID: u.ID, Role: u.Role, FacilityID: u.FacilityID} }
	facAdmin := as(createTestUser(t, srv, "facadmin", models.FacilityAdmin, north))
	noFeature := as(resident)
	noFeature.FeatureAccess = []models.FeatureAccess{}

	tests := []struct {
		name   string
		path   string
		caller Claims
		want   int
	}{
		{"user reads their own activity", fmt.Sprintf("/api/users/%d/daily-activity", resident.ID), as(resident), http.StatusOK},
		{"user reads another user's activity", fmt.Sprintf("/api/users/%d/daily-activity", neighbor.ID), as(resident), http.StatusUnauthorized},
		{"user reads another user's courses", fmt.Sprintf("/api/users/%d/courses", neighbor.ID), as(resident), http.StatusUnauthorized},
		{"facility admin reads another facility's user", fmt.Sprintf("/api/users/%d/catalog", southResident.ID), facAdmin, http.StatusUnauthorized},
		{"facility admin reads their facility's user", fmt.Sprintf("/api/users/%d/catalog", resident.ID), facAdmin, http.StatusOK},
		{"user reads their own outcomes", fmt.Sprintf("/api/users/%d/outcomes", resident.ID), as(resident), http.StatusOK},
		{"user reads another user's outcomes", fmt.Sprintf("/api/users/%d/outcomes", neighbor.ID), as(resident), http.StatusUnauthorized},
		{"facility admin reads another facility's logins", fmt.Sprintf("/api/users/%d/logins", southResident.ID), facAdmin, http.StatusUnauthorized},
		{"facility admin reads their facility's logins", fmt.Sprintf("/api/users/%d/logins", resident.ID), facAdmin, http.StatusOK},
		{"user on an admin route", "/api/provider-platforms", as(resident), http.StatusUnauthorized},
		{"feature off", fmt.Sprintf("/api/users/%d/courses", resident.ID), noFeature, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarded = nil
			w := httptest.NewRecorder()
			srv.Mux.ServeHTTP(w, requestAs(t, tt.path, tt.caller))
			assert.Equal(t, tt.want, w.Code, w.Body.String())
			if tt.want == http.StatusOK {
				assert.Equal(t, []string{tt.path}, forwarded)
			} else {
				assert.Empty(t, forwarded, "a refused request must not reach the service")
			}
		})
	}
}

func TestProviderPlatformsRoutesFollowTheContractsTable(t *testing.T) {
	proxy := http.NotFoundHandler()
	routes := providerPlatformsRoutes(proxy)

	require.Len(t, routes, len(providerplatforms.Routes))
	for i, want := range providerplatforms.Routes {
		got := routes[i]
		assert.Equal(t, want.Pattern, got.Pattern)
		assert.Equal(t, want.UserParam, got.UserParam, want.Pattern)
		assert.Equal(t, []features.Access{want.Feature}, got.Features, want.Pattern)
		switch want.Access {
		case providerplatforms.Admin:
			assert.Equal(t, module.Admin, got.Access, want.Pattern)
		case providerplatforms.AnyUser:
			assert.Equal(t, module.AnyUser, got.Access, want.Pattern)
		default:
			t.Errorf("%s: unhandled access %q", want.Pattern, want.Access)
		}
	}
}
