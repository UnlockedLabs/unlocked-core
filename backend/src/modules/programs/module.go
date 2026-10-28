// Package programs is the Programs feature module: programs, classes,
// enrollments, events and attendance, and the Canvas and Essential Education
// classes shown alongside them. Its code moved here from core in EN-194.
package programs

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"

	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

//go:embed migrations
var migrations embed.FS

// Module is the Programs module. The registry gives it Provider Platforms'
// api, which it asks about providers and the users linked to them.
type Module struct {
	ProviderPlatforms func(db *gorm.DB) ppapi.ProviderPlatforms
}

var (
	_ module.Module     = Module{}
	_ module.TestModels = Module{}
)

func (Module) Name() string { return "programs" }

// server is what the routes share.
type server struct {
	store             *Store
	Client            module.HTTPClient
	providerPlatforms ppapi.ProviderPlatforms
	facilityFeatures  module.Features
	reports           module.Reports
	// buckets cache the Canvas programs, classes and events. They are nil
	// without NATS (the tests).
	buckets map[string]nats.KeyValue
	// canvasInflight holds the Canvas cache keys being warmed, so each is
	// fetched once at a time.
	canvasInflight sync.Map
}

// newServer is the Programs server for deps. A failure to set up NATS stops
// the server, as it did when core set it up.
func (m Module) newServer(deps module.Deps) *server {
	srv := &server{
		store:             &Store{deps.DB},
		Client:            deps.HTTP,
		providerPlatforms: m.ProviderPlatforms(deps.DB),
		facilityFeatures:  deps.Features,
		reports:           deps.Reports,
	}
	if deps.NATS != nil {
		buckets, err := canvasBuckets(deps.NATS)
		if err != nil {
			log.Fatalf("Failed to setup JetStream KV store: %v", err)
		}
		srv.buckets = buckets
	}
	return srv
}

// Register adds the Programs routes.
func (m Module) Register(r module.Router, deps module.Deps) {
	srv := m.newServer(deps)
	for _, rt := range srv.routes() {
		r.Handle(rt)
	}
}

// RegisterJobs schedules class activation (jobs.go).
func (Module) RegisterJobs(s module.Scheduler, deps module.Deps) {
	scheduleJobs(s, &Store{deps.DB}, os.Getenv("APP_ENV") == "dev")
}

func (Module) Migrations() fs.FS { return migrations }

// LiveProviders is the Canvas and Essential Education code Provider Platforms
// needs (module.Deps.LiveProviders), until Provider Platforms fetches and
// caches the providers' classes itself.
func (m Module) LiveProviders(deps module.Deps) module.LiveProviders {
	return liveProviders{m.newServer(deps)}
}

// The NATS key-value buckets for the Canvas caches.
const (
	CanvasPrograms = "canvas_programs"
	CanvasClasses  = "canvas_classes"
	CanvasEvents   = "canvas_events"
)

// canvasBuckets opens the Canvas cache buckets, creating them on first start.
func canvasBuckets(nc *nats.Conn) (map[string]nats.KeyValue, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("initialize JetStream: %w", err)
	}
	buckets := map[string]nats.KeyValue{}
	for _, bucket := range []string{CanvasPrograms, CanvasClasses, CanvasEvents} {
		kv, err := js.KeyValue(bucket)
		if err != nil {
			kv, err = js.CreateKeyValue(&nats.KeyValueConfig{Bucket: bucket, History: 1, TTL: 5 * time.Minute})
			if err != nil {
				return nil, fmt.Errorf("create bucket %s: %w", bucket, err)
			}
		}
		buckets[bucket] = kv
	}
	return buckets, nil
}

// TestModels is Programs' tables, for core's SQLite test database. A class
// comes before its cohorts, so the cohort's foreign key has a table to point
// at.
func (Module) TestModels() []any {
	return []any{
		&Program{},
		&ProgramCreditType{},
		&ProgramType{},
		&ProgramClass{},
		&ProgramClassCreditType{},
		&ProgramClassCohort{},
		&FacilitiesPrograms{},
		&ProgramClassEvent{},
		&ProgramClassEnrollment{},
		&ClassCompletion{},
		&ProgramClassEventOverride{},
		&ProgramClassEventAttendance{},
		&ChangeLogEntry{},
		&ProgramClassesHistory{},
		&Room{},
	}
}

// liveProviders answers Provider Platforms' questions about Essential
// Education's users and Canvas's cached programs.
type liveProviders struct{ srv *server }

func (l liveProviders) Users(ctx context.Context, providerID uint) ([]module.ProviderUser, bool, error) {
	provider, err := l.srv.providerByID(ctx, providerID)
	if err != nil {
		return nil, false, err
	}
	lister, ok := l.srv.liveUserListerFor(provider)
	if !ok {
		return nil, false, nil
	}
	users, err := lister.ListUsers(ctx)
	if err != nil {
		return nil, true, err
	}
	return users, true, nil
}

func (l liveProviders) MappingsChanged(providerID, userID uint) {
	l.srv.invalidateCanvasProgramCache(providerID, int(userID))
}
