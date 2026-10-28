package tasks

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"

	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type Scheduler struct {
	gocron.Scheduler
	nats *nats.Conn
	db   *gorm.DB
}

func InitScheduling(dev bool, nats *nats.Conn, db *gorm.DB) *Scheduler {
	scheduler, err := gocron.NewScheduler()
	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
		return nil
	}
	runner := Scheduler{scheduler, nats, db}
	tasks, err := runner.generateTasks()
	if err != nil {
		log.Fatalf("failed to generate tasks: %v", err)
		return nil
	}
	hour := 1
	for _, task := range tasks {
		if task.Job == nil {
			log.Errorf("Task %v has no job", task.ID)
			continue
		}
		_, err := runner.NewJob(gocron.CronJob(getCronSchedule(&task, hour), false), gocron.NewTask(runner.runTask, &task))
		if err != nil {
			log.Errorf("Failed to create job: %v", err)
			continue
		}
		hour++
		if hour > 23 {
			hour = 1
		}
	}
	if !dev {
		runner.execute()
	}
	runner.Start()
	return &runner
}

// Start schedules every background job: core's and each feature module's, each
// module's on its own connection (moduleDBs). The scheduler process (`backend
// scheduler`) calls it, and so does the API server while ServerRunsJobs is
// true.
func Start(dev bool, nats *nats.Conn, db *gorm.DB, mods []module.Module, moduleDBs database.ModuleDBs) *Scheduler {
	s := InitScheduling(dev, nats, db)
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

func (s *Scheduler) execute() {
	tasks, err := s.generateTasks()
	if err != nil {
		log.Errorf("failed to generate tasks: %v", err)
		return
	}
	log.Infof("Generated %v tasks", tasks)
	for _, task := range tasks {
		if task.ProviderType == ppapi.Brightspace {
			continue
		}
		log.Infof("Running task: %v", task.Job.Name)
		s.runTask(&task)
	}
}

func getCronSchedule(task *models.RunnableTask, hour int) string {
	if task.ProviderType == ppapi.Brightspace {
		return fmt.Sprintf("0 %d * * 4", hour)
	} else {
		return task.Job.Schedule
	}
}
