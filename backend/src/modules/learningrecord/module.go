// Package learningrecord is the Learning Record feature module: learning
// record entries. Its code moved here from core in EN-195.
package learningrecord

import (
	"embed"
	"io/fs"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
)

//go:embed migrations
var migrations embed.FS

// Module is the Learning Record module.
type Module struct{}

var (
	_ module.Module     = Module{}
	_ module.TestModels = Module{}
)

func (Module) Name() string { return "learningrecord" }

// server is what the routes share.
type server struct {
	store *Store
}

// Register adds Learning Record's routes.
func (Module) Register(r module.Router, deps module.Deps) {
	srv := &server{store: &Store{deps.DB}}
	for _, rt := range srv.routes() {
		r.Handle(rt)
	}
}

func (Module) RegisterJobs(module.Scheduler, module.Deps) {}

func (Module) Migrations() fs.FS { return migrations }

// TestModels is Learning Record's table, for core's SQLite test database.
func (Module) TestModels() []any {
	return []any{&LearningRecordEntry{}}
}
