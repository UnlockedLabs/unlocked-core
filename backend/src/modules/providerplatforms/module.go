// Package providerplatforms is the Provider Platforms feature module: LMS
// connections (Canvas, Brightspace, Kolibri, Essential Education), the users
// linked to them, and their courses, outcomes and activity. Its code moved here
// from core in EN-193.
package providerplatforms

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
)

//go:embed migrations
var migrations embed.FS

// Module is the Provider Platforms module.
type Module struct{}

var (
	_ module.Module     = Module{}
	_ module.TestModels = Module{}
	_ module.Worker     = Module{}
)

func (Module) Name() string { return "providerplatforms" }

// server is what the routes share.
type server struct {
	accounts
	users module.Users
	live  module.LiveProviders
	// oauthState holds a Brightspace provider between the OAuth redirect and
	// its callback. It is nil without NATS (the tests).
	oauthState nats.KeyValue
}

// Register adds Provider Platforms' routes. A failure to set up NATS stops the
// server, as it did when core set it up.
func (Module) Register(r module.Router, deps module.Deps) {
	srv := &server{
		accounts: accounts{store: &store{db: deps.DB}, client: &http.Client{}},
		users:    deps.Users,
		live:     deps.LiveProviders,
	}
	if deps.NATS != nil {
		kv, err := oauthStateBucket(deps.NATS)
		if err != nil {
			log.Fatalf("Failed to setup JetStream KV store: %v", err)
		}
		srv.oauthState = kv
	}
	for _, rt := range srv.routes() {
		r.Handle(rt)
	}
}

// RegisterJobs schedules each active provider's LMS jobs for the worker
// (jobs.go).
func (Module) RegisterJobs(s module.Scheduler, deps module.Deps) {
	p := &jobPublisher{store: &store{db: deps.DB}, publish: deps.NATS.PublishMsg}
	scheduleJobs(s, p, os.Getenv("APP_ENV") == "dev")
}

// Work runs the LMS jobs and serves the provider user lists until ctx is
// canceled (worker.go).
func (Module) Work(ctx context.Context, deps module.Deps) error {
	w := &worker{store: &store{db: deps.DB}}
	return w.run(ctx, deps.NATS)
}

func (Module) Migrations() fs.FS { return migrations }

// oauthState is the NATS key-value bucket for Brightspace's OAuth state.
const oauthState = "oauth_state"

// oauthStateBucket opens the oauth_state bucket, creating it on first start.
func oauthStateBucket(nc *nats.Conn) (nats.KeyValue, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("initialize JetStream: %w", err)
	}
	kv, err := js.KeyValue(oauthState)
	if err == nil {
		return kv, nil
	}
	return js.CreateKeyValue(&nats.KeyValueConfig{Bucket: oauthState, History: 1, TTL: 10 * time.Minute})
}

// TestModels is Provider Platforms' tables, for core's SQLite test database.
func (Module) TestModels() []any {
	return []any{
		&ProviderPlatform{},
		&ProviderUserMapping{},
		&Course{},
		&Milestone{},
		&Outcome{},
		&Activity{},
		&OidcClient{},
		&UserEnrollment{},
		&UserCourseActivityTotals{},
		&cronJob{},
		&runnableTask{},
	}
}
