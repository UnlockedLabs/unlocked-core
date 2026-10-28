package providerplatforms

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/jobs"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeScheduler records what a module schedules, in order.
type fakeScheduler struct {
	specs   []string
	cron    []func()
	atStart []func()
}

// Cron rejects an empty spec, as the real scheduler does.
func (s *fakeScheduler) Cron(spec string, task func()) error {
	if spec == "" {
		return errors.New("empty cron spec")
	}
	s.specs = append(s.specs, spec)
	s.cron = append(s.cron, task)
	return nil
}

func (s *fakeScheduler) AtStart(task func()) error {
	s.atStart = append(s.atStart, task)
	return nil
}

// published records the messages a jobPublisher publishes.
type published struct {
	msgs []*nats.Msg
	err  error
}

func (p *published) publish(msg *nats.Msg) error {
	p.msgs = append(p.msgs, msg)
	return p.err
}

func (p *published) subjects() []string {
	var subjects []string
	for _, m := range p.msgs {
		subjects = append(subjects, m.Subject)
	}
	return subjects
}

// Every active provider runs the three LMS jobs on MIDDLEWARE_CRON_SCHEDULE
// and once at start, except Brightspace, which runs weekly on Thursday, one
// job an hour, and not at start.
func TestScheduleJobs(t *testing.T) {
	tests := []struct {
		name        string
		schedule    string
		dev         bool
		wantSpecs   []string
		wantAtStart []string
	}{
		{
			name:     "outside dev",
			schedule: "0 22 * * *",
			wantSpecs: []string{
				"0 22 * * *", "0 22 * * *", "0 22 * * *",
				"0 4 * * 4", "0 5 * * 4", "0 6 * * 4",
			},
			wantAtStart: []string{"tasks.get_courses", "tasks.get_milestones", "tasks.get_activity"},
		},
		{
			name:     "dev",
			schedule: "0 22 * * *",
			dev:      true,
			wantSpecs: []string{
				"0 22 * * *", "0 22 * * *", "0 22 * * *",
				"0 4 * * 4", "0 5 * * 4", "0 6 * * 4",
			},
		},
		{
			// A job whose schedule can't be set still runs at start, and
			// doesn't take a Brightspace hour.
			name:        "bad schedule",
			schedule:    "",
			wantSpecs:   []string{"0 1 * * 4", "0 2 * * 4", "0 3 * * 4"},
			wantAtStart: []string{"tasks.get_courses", "tasks.get_milestones", "tasks.get_activity"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MIDDLEWARE_CRON_SCHEDULE", tt.schedule)
			db := newTestDB(t)
			canvas := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "canvas"})
			brightspace := createProvider(t, db, ProviderPlatform{Type: Brightspace, Name: "brightspace"})
			createProvider(t, db, ProviderPlatform{Type: Kolibri, Name: "off", State: Disabled})
			s := &fakeScheduler{}
			pub := &published{}

			scheduleJobs(s, &jobPublisher{store: &store{db: db}, publish: pub.publish}, tt.dev)

			assert.Equal(t, tt.wantSpecs, s.specs)
			for _, run := range s.atStart {
				run()
			}
			assert.Equal(t, tt.wantAtStart, pub.subjects())

			// The scheduled runs publish for their own provider.
			pub.msgs = nil
			for _, run := range s.cron {
				run()
			}
			var providers []float64
			for _, m := range pub.msgs {
				var body map[string]any
				require.NoError(t, json.Unmarshal(m.Data, &body))
				providers = append(providers, body["provider_platform_id"].(float64))
			}
			wantProviders := []float64{float64(brightspace.ID), float64(brightspace.ID), float64(brightspace.ID)}
			if tt.schedule != "" {
				wantProviders = append([]float64{float64(canvas.ID), float64(canvas.ID), float64(canvas.ID)}, wantProviders...)
			}
			assert.Equal(t, wantProviders, providers)
		})
	}
}

// Scheduling creates every job's rows before any run, so runs that fire
// together don't each create them.
func TestScheduleJobsCreatesTasks(t *testing.T) {
	t.Setenv("MIDDLEWARE_CRON_SCHEDULE", "0 22 * * *")
	db := newTestDB(t)
	createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "one"})
	createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "two"})
	createProvider(t, db, ProviderPlatform{Type: Kolibri, Name: "off", State: Disabled})
	s := &fakeScheduler{}
	pub := &published{}

	scheduleJobs(s, &jobPublisher{store: &store{db: db}, publish: pub.publish}, false)

	assert.Empty(t, pub.msgs, "nothing has run yet")
	countRows := func() (cronJobs, tasks int64) {
		require.NoError(t, db.Model(&cronJob{}).Count(&cronJobs).Error)
		require.NoError(t, db.Model(&runnableTask{}).Count(&tasks).Error)
		return cronJobs, tasks
	}
	cronJobs, tasks := countRows()
	assert.Equal(t, int64(3), cronJobs, "one per job")
	assert.Equal(t, int64(6), tasks, "one per job and active provider")

	for _, run := range append(s.atStart, s.cron...) {
		run()
	}
	cronJobs, tasks = countRows()
	assert.Equal(t, int64(3), cronJobs)
	assert.Equal(t, int64(6), tasks)
}

// A run creates the job's rows the first time, marks the task running and
// publishes the message core's scheduler sent, with the task's last_run as
// it is at that run.
func TestJobPublisherRun(t *testing.T) {
	t.Setenv("MIDDLEWARE_CRON_SCHEDULE", "0 22 * * *")
	db := newTestDB(t)
	p := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "canvas"})
	pub := &published{}
	jp := &jobPublisher{store: &store{db: db}, publish: pub.publish}

	jp.run(context.Background(), jobs.GetMilestones, p.ID)

	var cj cronJob
	require.NoError(t, db.First(&cj, "name = ?", "get_milestones").Error)
	assert.NotEmpty(t, cj.ID)
	assert.Equal(t, providerPlatformJob, cj.Category)
	assert.Equal(t, "0 22 * * *", cj.Schedule)
	var task runnableTask
	require.NoError(t, db.First(&task, "job_id = ? AND provider_platform_id = ?", cj.ID, p.ID).Error)
	assert.Equal(t, taskRunning, task.Status)
	require.Len(t, pub.msgs, 1)
	assert.Equal(t, "tasks.get_milestones", pub.msgs[0].Subject)
	assert.JSONEq(t, `{"job_type":"get_milestones","job_id":"`+cj.ID+`","provider_platform_id":`+strconv.Itoa(int(p.ID))+`,"last_run":"0001-01-01T00:00:00Z"}`, string(pub.msgs[0].Data))

	// The worker finishes the job; the next run sends the new last_run and
	// reuses the rows.
	done := time.Date(2026, 3, 2, 22, 0, 0, 0, time.UTC)
	require.NoError(t, db.Model(&task).Updates(map[string]any{"status": taskPending, "last_run": done}).Error)
	jp.run(context.Background(), jobs.GetMilestones, p.ID)

	require.Len(t, pub.msgs, 2)
	var body map[string]any
	require.NoError(t, json.Unmarshal(pub.msgs[1].Data, &body))
	assert.Equal(t, "2026-03-02T22:00:00Z", body["last_run"])
	var rows int64
	require.NoError(t, db.Model(&cronJob{}).Count(&rows).Error)
	assert.Equal(t, int64(1), rows)
	require.NoError(t, db.Model(&runnableTask{}).Count(&rows).Error)
	assert.Equal(t, int64(1), rows)
}

// Each provider has its own task for a job, with its own last_run.
func TestJobPublisherRunTaskPerProvider(t *testing.T) {
	db := newTestDB(t)
	p1 := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "one"})
	p2 := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "two"})
	_, task1 := createTask(t, db, jobs.GetCourses, p1.ID, lastRun)
	pub := &published{}
	jp := &jobPublisher{store: &store{db: db}, publish: pub.publish}

	jp.run(context.Background(), jobs.GetCourses, p2.ID)

	var tasks []runnableTask
	require.NoError(t, db.Order("id").Find(&tasks).Error)
	require.Len(t, tasks, 2)
	assert.Equal(t, task1.ID, tasks[0].ID)
	assert.Equal(t, p2.ID, *tasks[1].ProviderPlatformID)
	assert.Equal(t, taskRunning, tasks[0].Status, "the other provider's task is left alone")
	var body map[string]any
	require.NoError(t, json.Unmarshal(pub.msgs[0].Data, &body))
	assert.Equal(t, "0001-01-01T00:00:00Z", body["last_run"])
}

// An existing cron_jobs row is used as it is, whatever schedule it saved.
func TestJobPublisherRunExistingRows(t *testing.T) {
	t.Setenv("MIDDLEWARE_CRON_SCHEDULE", "0 22 * * *")
	db := newTestDB(t)
	p := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "canvas"})
	cj, task := createTask(t, db, jobs.GetCourses, p.ID, lastRun)
	require.NoError(t, db.Model(cj).Update("schedule", "0 3 * * *").Error)
	pub := &published{}

	(&jobPublisher{store: &store{db: db}, publish: pub.publish}).run(context.Background(), jobs.GetCourses, p.ID)

	require.Len(t, pub.msgs, 1)
	var body map[string]any
	require.NoError(t, json.Unmarshal(pub.msgs[0].Data, &body))
	assert.Equal(t, cj.ID, body["job_id"])
	assert.Equal(t, "2026-03-01T00:00:00Z", body["last_run"])
	assert.Equal(t, taskRunning, reloadTask(t, &worker{store: &store{db: db}}, task.ID).Status)
	var saved cronJob
	require.NoError(t, db.First(&saved, "id = ?", cj.ID).Error)
	assert.Equal(t, "0 3 * * *", saved.Schedule)
}

// A job that can't be published leaves its task running, as before EN-197.
func TestJobPublisherRunPublishFails(t *testing.T) {
	db := newTestDB(t)
	p := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "canvas"})
	_, task := createTask(t, db, jobs.GetCourses, p.ID, lastRun)
	require.NoError(t, db.Model(task).Update("status", taskPending).Error)
	pub := &published{err: errors.New("nats down")}

	(&jobPublisher{store: &store{db: db}, publish: pub.publish}).run(context.Background(), jobs.GetCourses, p.ID)

	assert.Equal(t, taskRunning, reloadTask(t, &worker{store: &store{db: db}}, task.ID).Status)
}

func TestWorkerSubscriptions(t *testing.T) {
	var got []string
	for _, j := range (&worker{}).subscriptions() {
		got = append(got, j.job.PubName())
	}
	assert.Equal(t, []string{"tasks.get_courses", "tasks.get_milestones", "tasks.get_activity"}, got)
}
