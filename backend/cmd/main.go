package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
	server "github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"
	"github.com/UnlockedLabs/unlocked-core/backend/src/tasks"

	"github.com/joho/godotenv"
	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Info("no .env file found, using default env variables")
	}
	env := os.Getenv("APP_ENV")
	testing := (env == "testing")
	initLogging()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go cancelOnSignal(shutdown, cancel)

	// `backend scheduler` runs the background jobs and `backend worker
	// <module>` runs a feature module's worker; anything else (including the
	// "./backend" argument the charts pass) runs the API server.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "scheduler":
			runScheduler(ctx)
			return
		case "worker":
			name := ""
			if len(os.Args) > 2 {
				name = os.Args[2]
			}
			runWorker(ctx, name)
			return
		}
	}
	srv := server.NewServer(testing, ctx)
	if srv == nil {
		log.Info("Server stopped before migrations finished")
		return
	}
	srv.ListenAndServe(ctx)
}

func cancelOnSignal(shutdown <-chan os.Signal, cancel context.CancelFunc) {
	sig := <-shutdown
	log.Infof("Received signal: %v. Initiating shutdown...", sig)
	cancel()
}

// runScheduler runs every feature module's background jobs on their
// schedules; a job that needs a worker is published to NATS for it. It runs
// until ctx is canceled. It doesn't migrate: it waits until the database
// is migrated.
func runScheduler(ctx context.Context) {
	mods := registry.All()
	nc, db, ok := connectMigrated(ctx)
	defer nc.Close()
	if !ok {
		log.Info("Scheduler stopped before migrations finished")
		return
	}
	s := tasks.Start(nc, mods, database.OpenModuleDBs(db, mods))
	log.Info("Scheduler started")
	<-ctx.Done()
	if err := s.Stop(); err != nil {
		log.Errorf("Error stopping scheduler: %v", err)
	}
	log.Info("Scheduler stopped")
}

// runWorker runs the named feature module's worker until ctx is canceled.
// Like the scheduler, it waits until the database is migrated.
func runWorker(ctx context.Context, name string) {
	mods := registry.All()
	w, err := findWorker(mods, name)
	if err != nil {
		log.Fatal(err)
	}
	nc, db, ok := connectMigrated(ctx)
	defer nc.Close()
	if !ok {
		log.Infof("Worker %s stopped before migrations finished", name)
		return
	}
	log.Infof("Worker %s started", name)
	if err := w.Work(ctx, module.Deps{DB: database.OpenModuleDBs(db, mods).For(name), NATS: nc}); err != nil {
		log.Errorf("Worker %s failed: %v", name, err)
		return
	}
	log.Infof("Worker %s stopped", name)
}

// findWorker returns the worker of the module called name.
func findWorker(mods []module.Module, name string) (module.Worker, error) {
	for _, m := range mods {
		if m.Name() != name {
			continue
		}
		w, ok := m.(module.Worker)
		if !ok {
			return nil, fmt.Errorf("module %q has no worker", name)
		}
		return w, nil
	}
	return nil, fmt.Errorf("no module named %q", name)
}

// connectMigrated connects to NATS and the database, then waits until the
// database is migrated (database.WaitForMigrations). ok is false if ctx is
// canceled first.
func connectMigrated(ctx context.Context) (nc *nats.Conn, db *gorm.DB, ok bool) {
	nc, err := server.ConnectNATS()
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	db = database.Connect().DB
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Failed to get database connection: %v", err)
	}
	return nc, db, database.WaitForMigrations(ctx, sqlDB)
}

func initLogging() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(parseLogLevel())
}

func parseLogLevel() log.Level {
	level := os.Getenv("LOG_LEVEL")
	switch level {
	case "":
		return log.InfoLevel
	default:
		level, err := log.ParseLevel(level)
		if err != nil {
			log.Errorf("Error parsing log level: %v", err)
			level = log.InfoLevel
		}
		return level
	}
}
