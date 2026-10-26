package tasks

import (
	"context"
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/go-co-op/gocron/v2"
)

func TestServerRunsJobs(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "", want: true},
		{value: "true", want: true},
		{value: "false", want: false},
		{value: "0", want: false},
		{value: "not-a-bool", want: true},
	}
	for _, tt := range tests {
		t.Run("RUN_SCHEDULER="+tt.value, func(t *testing.T) {
			t.Setenv("RUN_SCHEDULER", tt.value)
			if got := ServerRunsJobs(); got != tt.want {
				t.Errorf("ServerRunsJobs() = %v, want %v", got, tt.want)
			}
		})
	}
}

// jobModule is a feature module that registers one job.
type jobModule struct {
	deps *module.Deps
}

func (jobModule) Name() string                        { return "jobmodule" }
func (jobModule) Register(module.Router, module.Deps) {}
func (jobModule) Migrations() fs.FS                   { return fstest.MapFS{} }

func (m jobModule) RegisterJobs(s module.Scheduler, deps module.Deps) {
	*m.deps = deps
	if err := s.Cron("0 3 * * *", func() {}); err != nil {
		panic(err)
	}
	if err := s.AtStart(func() {}); err != nil {
		panic(err)
	}
}

func TestStart(t *testing.T) {
	t.Setenv("MIDDLEWARE_CRON_SCHEDULE", "0 22 * * *")
	db := database.InitDB(context.Background(), true)
	// An LMS provider's jobs are Provider Platforms' own (EN-197), so core
	// doesn't schedule them.
	if err := db.Exec(`INSERT INTO provider_platforms (type, name, state) VALUES ('canvas_cloud', 'Canvas', 'enabled')`).Error; err != nil {
		t.Fatal(err)
	}

	var got module.Deps
	// dev skips the run-everything-at-start, which needs NATS.
	mods := []module.Module{jobModule{deps: &got}}
	s := Start(true, nil, db.DB, mods, database.OpenModuleDBs(db.DB, mods))
	t.Cleanup(func() { _ = s.Shutdown() })

	if got.DB != db.DB {
		t.Errorf("the module got DB %p, want the scheduler's %p", got.DB, db.DB)
	}

	// Core schedules only the class-activation job; the module adds its cron
	// and at-start jobs.
	var names []string
	if err := db.Model(&models.CronJob{}).Pluck("name", &names).Error; err != nil {
		t.Fatal(err)
	}
	wantNames := []string{string(models.ActivateScheduledClassesJob)}
	if !slices.Equal(names, wantNames) {
		t.Errorf("cron jobs = %v, want %v", names, wantNames)
	}
	if n := len(s.Jobs()); n != len(wantNames)+2 {
		t.Errorf("scheduled %d jobs, want %d (core's %d, the module's 2)", n, len(wantNames)+2, len(wantNames))
	}
}

// AtStart holds the task back for startDelay, so workers that start with the
// scheduler are subscribed before it publishes.
func TestAtStart(t *testing.T) {
	gs, err := gocron.NewScheduler()
	if err != nil {
		t.Fatal(err)
	}
	s := &Scheduler{Scheduler: gs}
	s.Start()
	t.Cleanup(func() { _ = s.Shutdown() })

	ran := make(chan struct{})
	before := time.Now()
	if err := s.AtStart(func() { close(ran) }); err != nil {
		t.Fatal(err)
	}
	jobs := s.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("%d jobs, want 1", len(jobs))
	}
	next, err := jobs[0].NextRun()
	if err != nil {
		t.Fatal(err)
	}
	if wait := next.Sub(before); wait < startDelay || wait > startDelay+5*time.Second {
		t.Errorf("the task runs in %v, want %v", wait, startDelay)
	}
	select {
	case <-ran:
		t.Error("the task ran right away, want it delayed")
	default:
	}
}
