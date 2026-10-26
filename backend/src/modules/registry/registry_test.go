package registry_test

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"

	_ "github.com/glebarez/go-sqlite"
	"github.com/pressly/goose/v3"
)

func TestAllModules(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range registry.All() {
		name := m.Name()
		if seen[name] {
			t.Errorf("module %q registered twice", name)
		}
		seen[name] = true
		if _, err := fs.Stat(m.Migrations(), "migrations"); err != nil {
			t.Errorf("module %q has no migrations folder: %v", name, err)
		}
	}
	if len(seen) != 4 {
		t.Errorf("got %d modules, want 4 (EN-186)", len(seen))
	}
}

// TestAllModulesMigrate runs each module's migrations on SQLite. A module
// with its own schema (module.Schema) has Postgres-only migrations, which
// SQLite can't run, so for it this checks only that they load. They're run on
// Postgres by hand until CI has a Postgres job.
func TestAllModulesMigrate(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var mods []module.Module
	for _, m := range registry.All() {
		if _, ok := m.(module.Schema); !ok {
			mods = append(mods, m)
			continue
		}
		fsys, err := fs.Sub(m.Migrations(), "migrations")
		if err != nil {
			t.Fatal(err)
		}
		p, err := goose.NewProvider(goose.DialectPostgres, db, fsys,
			goose.WithTableName(module.LedgerTable(m.Name())), goose.WithDisableGlobalRegistry(true))
		if err != nil {
			t.Errorf("module %s migrations don't load: %v", m.Name(), err)
			continue
		}
		if len(p.ListSources()) == 0 {
			t.Errorf("module %s has its own schema but no migration to create it", m.Name())
		}
	}
	if err := module.Migrate(context.Background(), db, goose.DialectSQLite3, mods); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
}
