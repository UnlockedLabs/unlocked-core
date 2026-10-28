package database

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type plainModule struct{ name string }

func (m plainModule) Name() string                               { return m.name }
func (m plainModule) Register(module.Router, module.Deps)        {}
func (m plainModule) RegisterJobs(module.Scheduler, module.Deps) {}
func (m plainModule) Migrations() fs.FS                          { return fstest.MapFS{} }

type seedCall struct {
	module     string
	userID     uint
	facilityID uint
}

// seedingModule has test models and default rows. It records each Seed call.
type seedingModule struct {
	plainModule
	tables []any
	calls  *[]seedCall
	err    error
}

func (m seedingModule) TestModels() []any { return m.tables }

func (m seedingModule) Seed(ctx context.Context, _ *gorm.DB, facilityID uint) error {
	c, _ := identity.FromContext(ctx)
	*m.calls = append(*m.calls, seedCall{m.name, c.UserID, facilityID})
	return m.err
}

type tableA struct{}
type tableB struct{}

func TestModuleTestModels(t *testing.T) {
	mods := []module.Module{
		seedingModule{plainModule: plainModule{"first"}, tables: []any{&tableA{}}},
		plainModule{"none"},
		seedingModule{plainModule: plainModule{"second"}, tables: []any{&tableB{}}},
	}
	assert.Equal(t, []any{&tableA{}, &tableB{}}, moduleTestModels(mods))
	assert.Empty(t, moduleTestModels([]module.Module{plainModule{"none"}}))
}

func TestSeedModules(t *testing.T) {
	ctx := identity.NewContext(context.Background(), &identity.Claims{UserID: 99999})

	t.Run("seeds each module with default rows, in order, as the user in ctx", func(t *testing.T) {
		var calls []seedCall
		mods := []module.Module{
			seedingModule{plainModule: plainModule{"first"}, calls: &calls},
			plainModule{"none"},
			seedingModule{plainModule: plainModule{"second"}, calls: &calls},
		}
		require.NoError(t, seedModules(ctx, ModuleDBs{}, 7, mods))
		assert.Equal(t, []seedCall{{"first", 99999, 7}, {"second", 99999, 7}}, calls)
	})

	t.Run("stops at the first module that fails", func(t *testing.T) {
		var calls []seedCall
		mods := []module.Module{
			seedingModule{plainModule: plainModule{"broken"}, calls: &calls, err: errors.New("boom")},
			seedingModule{plainModule: plainModule{"after"}, calls: &calls},
		}
		err := seedModules(ctx, ModuleDBs{}, 7, mods)
		require.ErrorContains(t, err, "seed module broken: boom")
		assert.Equal(t, []seedCall{{"broken", 99999, 7}}, calls)
	})
}

// schemaModule keeps its tables in their own schema.
type schemaModule struct{ plainModule }

func (schemaModule) Schema() string { return "own" }

// The SQLite tests have no schemas, so every module, even one with its own
// schema, shares core's connection there.
func TestModuleDBsShareCoreOnSQLite(t *testing.T) {
	core := InitDB(context.Background(), true).DB
	dbs := OpenModuleDBs(core, []module.Module{plainModule{"plain"}, schemaModule{plainModule{"withschema"}}})
	for _, name := range []string{"plain", "withschema", "missing"} {
		assert.Same(t, core, dbs.For(name), name)
	}
}
