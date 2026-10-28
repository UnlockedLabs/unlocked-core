package database

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"

	_ "github.com/glebarez/go-sqlite"
	"github.com/pressly/goose/v3"
)

func TestCoreMigrationsPending(t *testing.T) {
	tests := []struct {
		name    string
		applied []int64 // versions the ledger records; nil means no ledger table
		want    bool
		wantErr bool
	}{
		{name: "nothing pending at the latest version", applied: []int64{1, 2}, want: false},
		{name: "pending while the database is behind", applied: []int64{1}, want: true},
		{name: "no ledger yet is an error, not a new ledger", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", "file::memory:")
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = db.Close() })
			if tt.applied != nil {
				if _, err := db.Exec(`CREATE TABLE goose_db_version (version_id INTEGER, is_applied BOOLEAN)`); err != nil {
					t.Fatal(err)
				}
				for _, v := range tt.applied {
					if _, err := db.Exec(`INSERT INTO goose_db_version VALUES (?, true)`, v); err != nil {
						t.Fatal(err)
					}
				}
			}

			got, err := CoreMigrationsPending(context.Background(), db, 2)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CoreMigrationsPending() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("CoreMigrationsPending() = %v, want %v", got, tt.want)
			}
			var tables int
			if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'goose_db_version'`).Scan(&tables); err != nil {
				t.Fatal(err)
			}
			if tt.applied == nil && tables != 0 {
				t.Error("CoreMigrationsPending created the ledger")
			}
		})
	}
}

func TestLatestCoreMigration(t *testing.T) {
	tests := []struct {
		name    string
		files   []string // nil means no folder at all
		want    int64
		wantErr string
	}{
		{name: "the newest migration", files: []string{"00001_first.sql", "00002_second.sql"}, want: 2},
		{name: "an empty folder is a config error", files: []string{}, wantErr: "no migration files found"},
		{name: "a missing folder is a config error", wantErr: "list core migrations"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "migrations")
			if tt.files != nil {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range tt.files {
				body := "-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n"
				if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("MIGRATION_DIR", dir)

			got, err := LatestCoreMigration()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LatestCoreMigration() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LatestCoreMigration() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("LatestCoreMigration() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWaitForMigrations(t *testing.T) {
	tests := []struct {
		name    string
		applied []int64 // versions the ledger records; nil means no ledger table
		cancel  bool
		want    bool
	}{
		{name: "returns at once when the database is migrated", applied: []int64{1, 2}, want: true},
		{name: "a canceled wait stops while the database is behind", applied: []int64{1}, cancel: true, want: false},
		{name: "a canceled wait stops before the first migration run", cancel: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "migrations")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, f := range []string{"00001_first.sql", "00002_second.sql"} {
				body := "-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n"
				if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("MIGRATION_DIR", dir)

			db, err := sql.Open("sqlite", "file::memory:")
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = db.Close() })
			if tt.applied != nil {
				if _, err := db.Exec(`CREATE TABLE goose_db_version (version_id INTEGER, is_applied BOOLEAN)`); err != nil {
					t.Fatal(err)
				}
				for _, v := range tt.applied {
					if _, err := db.Exec(`INSERT INTO goose_db_version VALUES (?, true)`, v); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tt.want {
				markModulesMigrated(t, db)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			done := make(chan bool, 1)
			go func() { done <- WaitForMigrations(ctx, db) }()
			select {
			case got := <-done:
				if got != tt.want {
					t.Errorf("WaitForMigrations() = %v, want %v", got, tt.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("WaitForMigrations() kept waiting")
			}
		})
	}
}

// markModulesMigrated records every module's migrations as applied in its
// ledger, as the migration runner does.
func markModulesMigrated(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, m := range registry.All() {
		files, err := fs.Glob(m.Migrations(), "migrations/*.sql")
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			continue
		}
		ledger := module.LedgerTable(m.Name())
		if _, err := db.Exec(`CREATE TABLE ` + ledger + ` (version_id INTEGER, is_applied BOOLEAN)`); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			version, err := goose.NumericComponent(path.Base(f))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO `+ledger+` VALUES (?, true)`, version); err != nil {
				t.Fatal(err)
			}
		}
	}
}
