package models

import (
	"os"
	"slices"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/jobs"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type (
	CronJob struct {
		ID        string    `gorm:"primaryKey" json:"id"`
		Name      string    `gorm:"size 60" json:"name"`
		Schedule  string    `gorm:"size 60" json:"schedule"`
		Category  int       `json:"category"`
		CreatedAt time.Time `json:"created_at"`

		Tasks []RunnableTask `gorm:"foreignKey:JobID;references:ID" json:"-"`
	}
	JobType   = jobs.Type
	JobStatus string
)

func (CronJob) TableName() string { return "cron_jobs" }

func (cj *CronJob) BeforeCreate(tx *gorm.DB) error {
	if len(cj.ID) == 0 {
		cj.ID = uuid.NewString()
	}
	if slices.Contains(AllDefaultProviderJobs, JobType(cj.Name)) {
		cj.Category = ProviderPlatformJob
	} else if slices.Contains(AllSystemJobs, JobType(cj.Name)) {
		cj.Category = SystemJob
	}
	switch cj.Name {
	case string(ActivateScheduledClassesJob):
		cj.Schedule = EveryMorningAt5AM
	default:
		cj.Schedule = os.Getenv("MIDDLEWARE_CRON_SCHEDULE")
	}
	return nil
}

// RunnableTask is one scheduled run of a core job. OpenContentProviderID is
// only set on old rows: Open Content stopped using runnable_tasks in EN-196,
// and the column goes in a later migration.
type RunnableTask struct {
	ID                    uint           `gorm:"primaryKey" json:"id"`
	JobID                 string         `gorm:"size 50" json:"job_id"`
	Parameters            map[string]any `gorm:"-" json:"parameters"`
	LastRun               time.Time      `json:"last_run"`
	ProviderPlatformID    *uint          `json:"provider_platform_id"`
	OpenContentProviderID *uint          `json:"open_content_provider_id"`
	Status                JobStatus      `json:"status"`

	Job *CronJob `gorm:"foreignKey:JobID" json:"-"`
}

func (task *RunnableTask) Prepare(provId *uint) {
	if task.Parameters == nil {
		task.Parameters = make(map[string]any)
	}
	if task.Job != nil {
		task.Parameters["job_type"] = task.Job.Name
		task.Parameters["job_id"] = task.Job.ID
	}
	if provId != nil {
		if slices.Contains(AllDefaultProviderJobs, JobType(task.Job.Name)) || task.Job.Category == ProviderPlatformJob {
			task.Parameters["provider_platform_id"] = provId
		}
	}
}

func (RunnableTask) TableName() string { return "runnable_tasks" }

const (
	// Category 2 was Open Content's jobs, which it now schedules itself
	// (EN-196). Old rows still have it.
	ProviderPlatformJob = 1
	SystemJob           = 3

	GetMilestonesJob = jobs.GetMilestones
	GetCoursesJob    = jobs.GetCourses
	GetActivityJob   = jobs.GetActivity

	ActivateScheduledClassesJob           = jobs.ActivateScheduledClasses
	EverySundayAt8PM            string    = "0 20 * * 6"
	EveryMorningAt5AM           string    = "0 5 * * *"
	StatusPending               JobStatus = "pending"
	StatusRunning               JobStatus = "running"
)

var AllDefaultProviderJobs = []JobType{GetCoursesJob, GetMilestonesJob, GetActivityJob}
var AllSystemJobs = []JobType{ActivateScheduledClassesJob}
