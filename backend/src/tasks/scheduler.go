package tasks

import (
	"encoding/json"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
)

func (s *Scheduler) runTask(task *models.RunnableTask) {
	// publish the task to the nats server, update the satus of the task to 'running'
	task.Parameters["last_run"] = task.LastRun
	task.Parameters["job_id"] = task.JobID
	jobType := task.Parameters["job_type"].(string)
	params, err := json.Marshal(task.Parameters)
	if err != nil {
		log.Errorf("failed to marshal params: %v", err)
		return
	}
	task.Status = models.StatusRunning
	if err := s.db.Model(&models.RunnableTask{}).Where("id = ?", task.ID).Update("status", models.StatusRunning).Error; err != nil {
		log.Errorf("failed to update task status: %v", err)
		return
	}
	msg := nats.NewMsg(models.JobType(jobType).PubName())
	msg.Data = params
	if err := s.nats.PublishMsg(msg); err != nil {
		log.Errorf("failed to publish job: %v", err)
		return
	}
	log.Info("Published job: ", jobType)
}

// generateTasks builds core's tasks. The LMS jobs are Provider Platforms'
// own since EN-197 (its RegisterJobs).
func (s *Scheduler) generateTasks() ([]models.RunnableTask, error) {
	allTasks := make([]models.RunnableTask, 0, 10)
	if systemTasks, err := s.generateSystemTasks(); err == nil {
		allTasks = append(allTasks, systemTasks...)
	} else {
		log.Println("Failed to generate system tasks")
	}
	return allTasks, nil
}

// generateSystemTasks builds the platform-wide batch tasks that are not tied to
// any provider (e.g. activating classes whose scheduled start date has arrived).
func (s *Scheduler) generateSystemTasks() ([]models.RunnableTask, error) {
	systemTasks := make([]models.RunnableTask, 0, len(models.AllSystemJobs))
	for _, jobType := range models.AllSystemJobs {
		created, err := s.createIfNotExists(jobType)
		if err != nil {
			log.Errorf("failed to create system job %s: %v", jobType, err)
			return nil, err
		}
		task := models.RunnableTask{JobID: created.ID, Status: models.StatusPending}
		if err := s.intoSystemTask(created, &task); err != nil {
			log.Errorf("failed to create task for system job %s: %v", jobType, err)
			return nil, err
		}
		systemTasks = append(systemTasks, task)
	}
	return systemTasks, nil
}

// intoSystemTask finds or creates the single provider-less RunnableTask for a
// system job and prepares its parameters for publishing.
func (s *Scheduler) intoSystemTask(cj *models.CronJob, task *models.RunnableTask) error {
	if task.ID == 0 {
		if err := s.db.Model(&models.RunnableTask{}).
			Where("job_id = ? AND provider_platform_id IS NULL AND open_content_provider_id IS NULL", cj.ID).
			FirstOrCreate(&task).Error; err != nil {
			log.Errorf("failed to create task for job: %v. error: %v", cj.Name, err)
			return err
		}
		if err := s.db.Model(&models.RunnableTask{}).Preload("Job").First(&task, task.ID).Error; err != nil {
			log.Errorf("failed to reload task for job: %v. error: %v", cj.Name, err)
			return err
		}
	}
	task.Job = cj
	task.Prepare(nil)
	return nil
}

func (s *Scheduler) createIfNotExists(cj models.JobType) (*models.CronJob, error) {
	job := models.CronJob{Name: string(cj)}
	if err := s.db.Model(&models.CronJob{}).Where("name = ?", cj).FirstOrCreate(&job).Error; err != nil {
		log.Errorf("failed to find or create job: %v", err)
		return nil, err
	}
	log.Infof("CronJob %s has ID: %s", job.Name, job.ID)
	return &job, nil
}
