// Package registry is the one list of feature modules. The server and the
// migrations CLI both use it, so adding a module means adding it here only.
// It also hands core each module's api, so core never imports a module's
// insides.
package registry

import (
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/learningrecord"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/opencontent"
	ocapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/opencontent/api"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"
	programsapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs/api"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms"
	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"

	"gorm.io/gorm"
)

// All returns every feature module, in the order their routes and jobs are
// registered and their migrations run. Provider Platforms comes before
// Programs, because Programs uses its classes (EN-186).
func All() []module.Module {
	return []module.Module{
		providerplatforms.Module{},
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

// ProviderPlatforms is Provider Platforms' answers to core, reading db: the
// module's own connection (database.ModuleDBs.For), or core's transaction
// while it runs as the module (database.AsModule).
func ProviderPlatforms(db *gorm.DB) ppapi.ProviderPlatforms {
	return providerplatforms.NewAPI(db)
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
// own connection, for Provider Platforms' Deps.LiveProviders.
func LiveProviders(deps module.Deps) module.LiveProviders {
	m := programsModule()
	deps.DB = deps.ModuleDBs(m.Name())
	return m.LiveProviders(deps)
}

// programsModule is the Programs module, which asks Provider Platforms' api
// about providers, on Provider Platforms' own connection.
func programsModule() programs.Module {
	return programs.Module{ProviderPlatforms: func(deps module.Deps) ppapi.ProviderPlatforms {
		return providerplatforms.NewAPI(deps.ModuleDBs(providerplatforms.Module{}.Name()))
	}}
}
