// Package registry is the one list of feature modules. The server and the
// migrations CLI both use it, so adding a module means adding it here only.
// It also builds the Provider Platforms client, which the server and the
// scheduler share.
package registry

import (
	"net/http"
	"os"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/learningrecord"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/UnlockedLabs/unlocked-contracts/providerplatforms"
)

// All returns every feature module, in the order their routes and jobs are
// registered and their migrations run. Provider Platforms and Programs are
// their own services since EN-206 and EN-207, so they aren't here.
func All() []module.Module {
	return []module.Module{
		learningrecord.Module{},
	}
}

// ProviderPlatformsURL is where the Provider Platforms service is:
// PROVIDER_PLATFORMS_URL, else the service's name in the cluster.
func ProviderPlatformsURL() string {
	if url := os.Getenv("PROVIDER_PLATFORMS_URL"); url != "" {
		return url
	}
	return "http://provider-platforms:8080"
}

// ProviderPlatformsToken is the site's secret core and the Provider Platforms
// service call each other with.
func ProviderPlatformsToken() string { return os.Getenv("PROVIDER_PLATFORMS_TOKEN") }

// ProviderPlatforms is the Provider Platforms service's answers to core, asked
// over HTTP.
func ProviderPlatforms() providerplatforms.ProviderPlatforms {
	return providerplatforms.NewClient(ProviderPlatformsURL(), ProviderPlatformsToken(), http.DefaultClient)
}
