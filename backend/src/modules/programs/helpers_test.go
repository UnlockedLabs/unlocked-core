package programs

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTestDB returns an empty in-memory database with Programs' tables and the
// rows of core's tables that Programs reads and writes.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	core := []any{&Facility{}, &User{}, &UserAccountHistory{}}
	if err := db.AutoMigrate(append(core, Module{}.TestModels()...)...); err != nil {
		t.Fatal(err)
	}
	return db
}

// newTestServer returns a Programs server on an empty test database.
func newTestServer(t *testing.T) *server {
	t.Helper()
	return &server{store: &Store{newTestDB(t)}}
}
