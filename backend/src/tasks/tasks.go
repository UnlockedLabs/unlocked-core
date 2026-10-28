package tasks

import (
	"os"
	"strconv"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
)

// Scheduler runs the feature modules' background jobs (module.Scheduler).
// Core has none of its own since EN-197.
type Scheduler struct {
	gocron.Scheduler
}

// Start schedules every feature module's background jobs, each module's on
// its own connection (moduleDBs). The scheduler process (`backend scheduler`)
// calls it, and so does the API server while ServerRunsJobs is true.
func Start(nats *nats.Conn, mods []module.Module, moduleDBs database.ModuleDBs) *Scheduler {
	gs, err := gocron.NewScheduler()
	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
		return nil
	}
	s := &Scheduler{gs}
	s.Start()
	for _, m := range mods {
		m.RegisterJobs(s, module.Deps{DB: moduleDBs.For(m.Name()), NATS: nats})
	}
	return s
}

// ServerRunsJobs reports whether the API server runs the background jobs
// itself. It does unless RUN_SCHEDULER is false, which is set wherever a
// separate scheduler process runs them instead; sites still on the old charts
// don't set it and keep today's behavior.
func ServerRunsJobs() bool {
	v := os.Getenv("RUN_SCHEDULER")
	if v == "" {
		return true
	}
	run, err := strconv.ParseBool(v)
	if err != nil {
		log.Errorf("RUN_SCHEDULER=%q isn't true or false; the server will run the background jobs", v)
		return true
	}
	return run
}

// Cron runs a feature module's task on a five-field cron schedule
// (module.Scheduler).
func (s *Scheduler) Cron(spec string, task func()) error {
	_, err := s.NewJob(gocron.CronJob(spec, false), gocron.NewTask(task))
	return err
}

// AtStart runs a feature module's task once, startDelay after the scheduler
// starts (module.Scheduler).
func (s *Scheduler) AtStart(task func()) error {
	start := gocron.OneTimeJobStartDateTime(time.Now().Add(startDelay))
	_, err := s.NewJob(gocron.OneTimeJob(start), gocron.NewTask(task))
	return err
}

// startDelay holds back the at-start jobs: the workers start alongside the
// scheduler and subscribe only once they've checked migrations, and a job
// published before they subscribe is lost.
const startDelay = 60 * time.Second

func (s *Scheduler) Stop() error {
	return s.StopJobs()
}
