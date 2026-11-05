package handlers

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-contracts/providerplatforms"
	log "github.com/sirupsen/logrus"
)

// registerProviderPlatformsRoutes forwards Provider Platforms' /api routes to
// its service (EN-206). Core stays the public edge: each route goes through the
// same session, role, feature and user checks it had in core
// (providerplatforms.Routes), then the service gets the request with the
// service token and the caller's claims.
func (srv *Server) registerProviderPlatformsRoutes() {
	proxy, err := newProviderPlatformsProxy(registry.ProviderPlatformsURL(), registry.ProviderPlatformsToken())
	if err != nil {
		log.Fatalf("Provider Platforms URL: %v", err)
	}
	for _, rt := range providerPlatformsRoutes(proxy) {
		moduleRouter{srv}.Handle(rt)
	}
}

// providerPlatformsRoutes is the contracts' route table as core routes that
// all go to proxy.
func providerPlatformsRoutes(proxy http.Handler) []module.Route {
	routes := make([]module.Route, len(providerplatforms.Routes))
	for i, r := range providerplatforms.Routes {
		access := module.AnyUser
		if r.Access == providerplatforms.Admin {
			access = module.Admin
		}
		routes[i] = module.Route{
			Pattern:   r.Pattern,
			Handler:   proxy,
			Access:    access,
			UserParam: r.UserParam,
			Features:  []features.Access{r.Feature},
		}
	}
	return routes
}

// newProviderPlatformsProxy sends requests to the service at target. The
// browser's cookie and Authorization header stop here, and any claims header it
// sent is replaced with the claims core's session check put in the context.
func newProviderPlatformsProxy(target, token string) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", target, err)
	}
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(u)
			r.Out.Header.Del("Cookie")
			r.Out.Header.Del(identity.ClaimsHeader)
			r.Out.Header.Set("Authorization", "Bearer "+token)
			if claims, ok := identity.FromContext(r.In.Context()); ok {
				if err := identity.SetClaimsHeader(r.Out, claims); err != nil {
					log.Errorf("Provider Platforms proxy: claims for the service: %v", err)
				}
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Errorf("Provider Platforms service unreachable for %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, "provider platforms service unavailable", http.StatusBadGateway)
		},
	}, nil
}
