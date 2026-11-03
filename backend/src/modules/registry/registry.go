// Package registry is the one list of feature modules. The server and the
// migrations CLI both use it, so adding a module means adding it here only.
// It also hands core each module's api, so core never imports a module's
// insides.
package registry

import (
	"net/http"
	"os"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/learningrecord"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/opencontent"
	ocapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/opencontent/api"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"
	programsapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs/api"

	"github.com/UnlockedLabs/unlocked-contracts/coreapi"
	"github.com/UnlockedLabs/unlocked-contracts/providerplatforms"
	"gorm.io/gorm"
)

// All returns every feature module, in the order their routes and jobs are
// registered and their migrations run. Provider Platforms is its own service
// since EN-206, so it isn't here.
func All() []module.Module {
	return []module.Module{
		opencontent.Module{},
		programsModule(),
		learningrecord.Module{},
	}
}

// OpenContent is Open Content's answers to core, on the connection db gives
// the Open Content module (database.ModuleDBs.For).
func OpenContent(db func(name string) *gorm.DB) ocapi.OpenContent {
	return opencontent.NewAPI(db(opencontent.Module{}.Name()))
}

// ProviderPlatformsURL is where the Provider Platforms service is, such as
// "http://provider-platforms:8080".
func ProviderPlatformsURL() string { return os.Getenv("PROVIDER_PLATFORMS_URL") }

// ServiceToken is the secret core and the product services send each other.
func ServiceToken() string { return os.Getenv("SERVICE_TOKEN") }

// ProviderPlatforms is the Provider Platforms service's answers to core, asked
// over HTTP.
func ProviderPlatforms() providerplatforms.ProviderPlatforms {
	return providerplatforms.NewClient(ProviderPlatformsURL(), ServiceToken(), http.DefaultClient)
}

// Programs is Programs' answers to core, on the connection db gives the
// Programs module (database.ModuleDBs.For).
func Programs(db func(name string) *gorm.DB) programsapi.Programs {
	return programs.NewAPI(db(programs.Module{}.Name()))
}

// ProgramsInTx runs fn with Programs' answers inside core's transaction tx, as
// Programs' own connection runs (module.RunAs), so its writes commit or roll
// back with core's.
func ProgramsInTx(tx *gorm.DB, fn func(programsapi.Programs) error) error {
	return module.RunAs(tx, programsModule(), func(tx *gorm.DB) error {
		return fn(programs.NewAPI(tx))
	})
}

// LiveProviders is Programs' Canvas and Essential Education code, on Programs'
// own connection, for the Provider Platforms service to call.
func LiveProviders(deps module.Deps) coreapi.LiveProviders {
	m := programsModule()
	deps.DB = deps.ModuleDBs(m.Name())
	return m.LiveProviders(deps)
}

// programsModule is the Programs module, which asks the Provider Platforms
// service about providers.
func programsModule() programs.Module {
	return programs.Module{ProviderPlatforms: func(module.Deps) providerplatforms.ProviderPlatforms {
		return ProviderPlatforms()
	}}
}
