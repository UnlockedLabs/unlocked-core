package providerplatforms

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/UnlockedLabs/unlocked-contracts/jobs"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
)

// The LMS jobs Provider Platforms publishes for its worker (worker.go). Every
// active provider runs each of them. Core's scheduler published them until
// EN-197.

var lmsJobs = []jobs.Type{jobs.GetCourses, jobs.GetMilestones, jobs.GetActivity}

// scheduleJobs puts each active provider's LMS jobs on their cron schedule
// and, outside dev, also runs them once at start. Brightspace runs weekly
// instead, and not at start. The providers are read once, so a provider
// added later is picked up at the next restart.
//
// It creates the jobs' cron_jobs and runnable_tasks rows first, one at a
// time: the runs fire together, and nothing stops two of them creating the
// same row.
func scheduleJobs(s module.Scheduler, p *jobPublisher, dev bool) {
	providers, err := p.store.getAllActiveProviderPlatforms()
	if err != nil {
		log.Errorf("Failed to schedule the LMS jobs: %v", err)
		return
	}
	// hour spreads the Brightspace runs across Thursday, one job an hour.
	hour := 1
	for _, provider := range providers {
		for _, job := range lmsJobs {
			if _, err := p.store.getOrCreateTask(context.Background(), newCronJob(job), provider.ID); err != nil {
				log.Errorf("Failed to create the task for %s and provider %d: %v", job, provider.ID, err)
			}
			schedule := os.Getenv("MIDDLEWARE_CRON_SCHEDULE")
			if provider.Type == Brightspace {
				schedule = fmt.Sprintf("0 %d * * 4", hour)
			}
			run := func() { p.run(context.Background(), job, provider.ID) }
			// A bad schedule doesn't stop the run at start, as before EN-197.
			if err := s.Cron(schedule, run); err != nil {
				log.Errorf("Failed to schedule %s for provider %d: %v", job, provider.ID, err)
			} else {
				hour++
				if hour > 23 {
					hour = 1
				}
			}
			if dev || provider.Type == Brightspace {
				continue
			}
			if err := s.AtStart(run); err != nil {
				log.Errorf("Failed to schedule %s for provider %d at start: %v", job, provider.ID, err)
			}
		}
	}
}

// jobPublisher publishes an LMS job for a provider and keeps its
// runnable_tasks row.
type jobPublisher struct {
	store   *store
	publish func(*nats.Msg) error
}

// run marks the provider's task for job running and publishes the job with
// the task's last successful run. The worker marks the task pending again
// when the job ends.
func (p *jobPublisher) run(ctx context.Context, job jobs.Type, providerID uint) {
	task, err := p.store.getOrCreateTask(ctx, newCronJob(job), providerID)
	if err != nil {
		log.Errorf("failed to find the task for %s and provider %d: %v", job, providerID, err)
		return
	}
	msg, err := jobMsg(job, task, providerID)
	if err != nil {
		log.Errorf("failed to marshal params: %v", err)
		return
	}
	if err := p.store.setTaskStatus(ctx, task.ID, taskRunning); err != nil {
		log.Errorf("failed to update task status: %v", err)
		return
	}
	if err := p.publish(msg); err != nil {
		log.Errorf("failed to publish job: %v", err)
		return
	}
	log.Info("Published job: ", job)
}

// newCronJob is job's cron_jobs row, for creating it the first time.
func newCronJob(job jobs.Type) cronJob {
	return cronJob{
		ID:       uuid.NewString(),
		Name:     string(job),
		Category: providerPlatformJob,
		Schedule: os.Getenv("MIDDLEWARE_CRON_SCHEDULE"),
	}
}

// jobMsg asks the worker to run job for a provider: the same message core's
// scheduler sent.
func jobMsg(job jobs.Type, task *runnableTask, providerID uint) (*nats.Msg, error) {
	body := map[string]any{
		"job_type":             job,
		"job_id":               task.JobID,
		"provider_platform_id": providerID,
		"last_run":             task.LastRun,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	msg := nats.NewMsg(job.PubName())
	msg.Data = data
	return msg, nil
}
