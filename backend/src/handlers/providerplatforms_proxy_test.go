package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
