package main

import (
	"context"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var lastRun = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

func reloadTask(t *testing.T, sh *ServiceHandler, id uint) models.RunnableTask {
	t.Helper()
	var task models.RunnableTask
	require.NoError(t, sh.db.First(&task, id).Error)
	return task
}

// A job marks its task pending when it ends, and moves last_run to now only
// when it succeeded, so a failed run is retried from the same point.
func TestCleanupJob(t *testing.T) {
	db := newTestDB(t)
	sh := newHandler(db)
	p1 := createProvider(t, db, providerplatforms.ProviderPlatform{Type: providerplatforms.CanvasCloud, Name: "one"})
	p2 := createProvider(t, db, providerplatforms.ProviderPlatform{Type: providerplatforms.CanvasCloud, Name: "two"})
	cj, task1 := createTask(t, db, models.GetCoursesJob, p1.ID, lastRun)
	task2 := models.RunnableTask{JobID: cj.ID, ProviderPlatformID: &p2.ID, Status: models.StatusRunning, LastRun: lastRun}
	require.NoError(t, db.Create(&task2).Error)
	id2 := int(p2.ID)

	sh.cleanupJob(context.Background(), &id2, cj.ID, false)
	got := reloadTask(t, sh, task2.ID)
	assert.Equal(t, models.StatusPending, got.Status)
	assert.True(t, got.LastRun.Equal(lastRun))

	before := time.Now()
	sh.cleanupJob(context.Background(), &id2, cj.ID, true)
	got = reloadTask(t, sh, task2.ID)
	assert.Equal(t, models.StatusPending, got.Status)
	assert.False(t, got.LastRun.Before(before))

	other := reloadTask(t, sh, task1.ID)
	assert.Equal(t, models.StatusRunning, other.Status, "another provider's task is left alone")
	assert.True(t, other.LastRun.Equal(lastRun))
}

// Without a provider (the class-activation job), the task is found by its
// job alone.
func TestCleanupJobWithoutProvider(t *testing.T) {
	db := newTestDB(t)
	sh := newHandler(db)
	cj := models.CronJob{Name: string(models.ActivateScheduledClassesJob)}
	require.NoError(t, db.Create(&cj).Error)
	task := models.RunnableTask{JobID: cj.ID, Status: models.StatusRunning}
	require.NoError(t, db.Create(&task).Error)

	sh.cleanupJob(context.Background(), nil, cj.ID, true)
	got := reloadTask(t, sh, task.ID)
	assert.Equal(t, models.StatusPending, got.Status)
	assert.False(t, got.LastRun.IsZero())
}

// A message the jobs can't use is logged and dropped. Once the provider id
// and job id are read, a missing provider also releases the task.
func TestInitProviderPlatformServiceBadMessages(t *testing.T) {
	db := newTestDB(t)
	sh := newHandler(db)
	tests := []struct {
		name string
		data string
		want string
	}{
		{"not JSON", `{`, "failed to unmarshal message: unexpected end of JSON input"},
		{"no provider", `{"job_id":"j"}`, "failed to parse provider_platform_id: <nil>"},
		{"no job", `{"provider_platform_id":1}`, "failed to parse job_id: <nil>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sh.initProviderPlatformService(context.Background(), &nats.Msg{Data: []byte(tt.data)})
			assert.EqualError(t, err, tt.want)
		})
	}

	t.Run("unknown provider", func(t *testing.T) {
		missing := uint(42)
		cj, task := createTask(t, db, models.GetCoursesJob, missing, lastRun)
		_, err := sh.initProviderPlatformService(context.Background(), jobMsg(t, models.GetCoursesJob, cj.ID, missing, lastRun))
		assert.EqualError(t, err, "failed to find provider: record not found")
		got := reloadTask(t, sh, task.ID)
		assert.Equal(t, models.StatusPending, got.Status)
		assert.True(t, got.LastRun.Equal(lastRun))
	})

	t.Run("unsupported type", func(t *testing.T) {
		p := createProvider(t, db, providerplatforms.ProviderPlatform{Type: "moodle", Name: "m"})
		_, err := sh.initProviderPlatformService(context.Background(), jobMsg(t, models.GetCoursesJob, "j", p.ID, lastRun))
		assert.EqualError(t, err, "unsupported provider type: moodle")
	})
}

func TestHandleCourses(t *testing.T) {
	db := newTestDB(t)
	sh := newHandler(db)
	fc := newFakeCanvas(t)
	fc.courses = []map[string]any{{"id": float64(101), "name": "Algebra", "course_code": "ALG-1"}}
	p := createCanvasProvider(t, db, fc)
	cj, task := createTask(t, db, models.GetCoursesJob, p.ID, lastRun)

	sh.handleCourses(context.Background(), jobMsg(t, models.GetCoursesJob, cj.ID, p.ID, lastRun))

	var names []string
	require.NoError(t, db.Model(&providerplatforms.Course{}).Pluck("name", &names).Error)
	assert.Equal(t, []string{"Algebra"}, names)
	got := reloadTask(t, sh, task.ID)
	assert.Equal(t, models.StatusPending, got.Status)
	assert.True(t, got.LastRun.After(lastRun))
}

// A failed import releases the task without moving last_run.
func TestHandleCoursesFailure(t *testing.T) {
	db := newTestDB(t)
	sh := newHandler(db)
	fc := newFakeCanvas(t)
	fc.Close()
	p := createCanvasProvider(t, db, fc)
	cj, task := createTask(t, db, models.GetCoursesJob, p.ID, lastRun)

	sh.handleCourses(context.Background(), jobMsg(t, models.GetCoursesJob, cj.ID, p.ID, lastRun))

	got := reloadTask(t, sh, task.ID)
	assert.Equal(t, models.StatusPending, got.Status)
	assert.True(t, got.LastRun.Equal(lastRun))
}

// The milestones job reads milestones since the task's last_run for the
// provider's students only, and waits TIMEOUT_WAIT seconds after each course.
func TestHandleMilestones(t *testing.T) {
	db := newTestDB(t)
	sh := newHandler(db)
	fc := newFakeCanvas(t)
	fc.submissions = []map[string]any{
		{"id": float64(1), "user_id": float64(21), "workflow_state": "submitted", "submitted_at": "2026-03-02T00:00:00Z"},
		{"id": float64(2), "user_id": float64(23), "workflow_state": "submitted", "submitted_at": "2026-03-02T00:00:00Z"},
	}
	p := createCanvasProvider(t, db, fc)
	other := createProvider(t, db, providerplatforms.ProviderPlatform{Type: providerplatforms.CanvasCloud, Name: "other"})
	createResident(t, db, p.ID, 1, "student", "21")
	createResident(t, db, p.ID, 2, "admin", "22")
	createResident(t, db, other.ID, 3, "student", "23")
	require.NoError(t, db.Create(&providerplatforms.Course{ProviderPlatformID: p.ID, ExternalID: "101"}).Error)
	cj, task := createTask(t, db, models.GetMilestonesJob, p.ID, lastRun)

	start := time.Now()
	sh.handleMilestonesForCourseUser(context.Background(), jobMsg(t, models.GetMilestonesJob, cj.ID, p.ID, lastRun))
	assert.GreaterOrEqual(t, time.Since(start), TIMEOUT_WAIT*time.Second)

	var milestones []providerplatforms.Milestone
	require.NoError(t, db.Find(&milestones).Error)
	require.Len(t, milestones, 1)
	assert.Equal(t, uint(1), milestones[0].UserID)
	assert.Contains(t, fc.Requests(), "GET /api/v1/courses/101/students/submissions?per_page=100&submitted_since=2026-03-01T00%3A00%3A00Z&user_ids%5B%5D=21 auth=Bearer token-123")
	got := reloadTask(t, sh, task.ID)
	assert.Equal(t, models.StatusPending, got.Status)
	assert.True(t, got.LastRun.After(lastRun))
}

// A canceled job stops between courses and leaves its task running.
func TestHandleMilestonesCanceled(t *testing.T) {
	db := newTestDB(t)
	sh := newHandler(db)
	fc := newFakeCanvas(t)
	p := createCanvasProvider(t, db, fc)
	require.NoError(t, db.Create(&providerplatforms.Course{ProviderPlatformID: p.ID, ExternalID: "101"}).Error)
	cj, task := createTask(t, db, models.GetMilestonesJob, p.ID, lastRun)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sh.handleMilestonesForCourseUser(ctx, jobMsg(t, models.GetMilestonesJob, cj.ID, p.ID, lastRun))

	assert.Empty(t, fc.Requests())
	assert.Equal(t, models.StatusRunning, reloadTask(t, sh, task.ID).Status)
}

// Kolibri and Brightspace import milestones for the user mappings as the
// milestones job looks them up, where user_id arrives as uint (see dbUint).
// A mapping whose user_id they can't read is skipped.
func TestImportMilestonesMappedStudent(t *testing.T) {
	t.Run("kolibri", func(t *testing.T) {
		db := newTestDB(t)
		sh := newHandler(db)
		ks, kdb := newTestKolibriService(t, db)
		createResident(t, db, ks.ProviderPlatformID, 1, "student", "k1")
		require.NoError(t, kdb.Exec(`INSERT INTO kolibriauth_facilityuser VALUES ('k1', 'A', 'a', 'facility-1')`).Error)
		require.NoError(t, kdb.Exec(`INSERT INTO content_channelmetadata VALUES ('ch', 'Khan', '', '', 1, 'ch')`).Error)
		require.NoError(t, kdb.Exec(`INSERT INTO logger_contentsessionlog VALUES ('s1', 'ch', 'k1', '2026-03-01')`).Error)
		users, err := sh.lookupUserMapping(map[string]any{"provider_platform_id": float64(ks.ProviderPlatformID)})
		require.NoError(t, err)
		require.IsType(t, uint(0), users[0]["user_id"])
		users = append(users, map[string]any{"user_id": "bad", "external_user_id": "k2"})

		course := map[string]any{"course_id": int64(5), "external_course_id": "ch"}
		require.NoError(t, ks.ImportMilestones(course, users, db, lastRun))

		var enrolled []uint
		require.NoError(t, db.Model(&providerplatforms.UserEnrollment{}).Pluck("user_id", &enrolled).Error)
		assert.Equal(t, []uint{1}, enrolled)
	})

	t.Run("brightspace", func(t *testing.T) {
		db := newTestDB(t)
		sh := newHandler(db)
		useTempDir(t)
		fb := newFakeBrightspace(t)
		fb.datasets["User Enrollments"] = "OrgUnitId,UserId\n10,b1\n"
		fb.datasets["Assignment Submissions"] = "DropboxId,OrgUnitId,SubmitterId,IsGraded\n"
		fb.datasets["Quiz Attempts"] = "AttemptId,UserId,OrgUnitId,IsGraded,IsDeleted\n"
		p := createBrightspaceProvider(t, db, fb)
		createResident(t, db, p.ID, 1, "student", "b1")
		users, err := sh.lookupUserMapping(map[string]any{"provider_platform_id": float64(p.ID)})
		require.NoError(t, err)
		require.IsType(t, uint(0), users[0]["user_id"])
		users = append(users, map[string]any{"user_id": "bad", "external_user_id": "b2"})

		course := map[string]any{"course_id": int64(5), "external_course_id": "10"}
		require.NoError(t, newTestBrightspaceService(t, db, p).ImportMilestones(course, users, db, lastRun))

		var enrolled []uint
		require.NoError(t, db.Model(&providerplatforms.UserEnrollment{}).Pluck("user_id", &enrolled).Error)
		assert.Equal(t, []uint{1}, enrolled)
	})
}

// The activity job runs every course of the provider. A failed procedure
// call (as on SQLite) is logged and doesn't fail the job.
func TestHandleActivity(t *testing.T) {
	db := newTestDB(t)
	sh := newHandler(db)
	fc := newFakeCanvas(t)
	fc.enrollments = []map[string]any{{"user_id": float64(21), "total_activity_time": float64(600)}}
	p := createCanvasProvider(t, db, fc)
	createResident(t, db, p.ID, 1, "student", "21")
	require.NoError(t, db.Create(&providerplatforms.Course{ProviderPlatformID: p.ID, ExternalID: "101"}).Error)
	require.NoError(t, db.Create(&providerplatforms.Course{ProviderPlatformID: p.ID, ExternalID: "102"}).Error)
	cj, task := createTask(t, db, models.GetActivityJob, p.ID, lastRun)
	calls := recordCalls(t, db)

	sh.handleAcitivityForCourse(context.Background(), jobMsg(t, models.GetActivityJob, cj.ID, p.ID, lastRun))

	var enrollments []providerplatforms.UserEnrollment
	require.NoError(t, db.Order("course_id").Find(&enrollments).Error)
	require.Len(t, enrollments, 2)
	assert.Equal(t, []procedureCall{
		{"insert_daily_activity_canvas", []any{uint(1), uint(1), "course_interaction", uint(600), "101"}},
		{"insert_daily_activity_canvas", []any{uint(1), uint(2), "course_interaction", uint(600), "102"}},
	}, *calls)
	got := reloadTask(t, sh, task.ID)
	assert.Equal(t, models.StatusPending, got.Status)
	assert.True(t, got.LastRun.After(lastRun))
}

// A course that fails doesn't stop the others, but the run doesn't count:
// last_run stays where it was.
func TestHandleActivityFailure(t *testing.T) {
	db := newTestDB(t)
	sh := newHandler(db)
	fc := newFakeCanvas(t)
	fc.Close()
	p := createCanvasProvider(t, db, fc)
	require.NoError(t, db.Create(&providerplatforms.Course{ProviderPlatformID: p.ID, ExternalID: "101"}).Error)
	cj, task := createTask(t, db, models.GetActivityJob, p.ID, lastRun)

	sh.handleAcitivityForCourse(context.Background(), jobMsg(t, models.GetActivityJob, cj.ID, p.ID, lastRun))

	got := reloadTask(t, sh, task.ID)
	assert.Equal(t, models.StatusPending, got.Status)
	assert.True(t, got.LastRun.Equal(lastRun))
}

func TestLookupMappings(t *testing.T) {
	db := newTestDB(t)
	sh := newHandler(db)
	p := createProvider(t, db, providerplatforms.ProviderPlatform{Type: providerplatforms.CanvasCloud, Name: "p"})
	other := createProvider(t, db, providerplatforms.ProviderPlatform{Type: providerplatforms.CanvasCloud, Name: "o"})
	createResident(t, db, p.ID, 1, "student", "a")
	createResident(t, db, p.ID, 2, "facility_admin", "b")
	createResident(t, db, other.ID, 3, "student", "c")
	require.NoError(t, db.Create(&providerplatforms.Course{ProviderPlatformID: p.ID, ExternalID: "x"}).Error)
	require.NoError(t, db.Create(&providerplatforms.Course{ProviderPlatformID: other.ID, ExternalID: "y"}).Error)

	users, err := sh.lookupUserMapping(map[string]any{"provider_platform_id": float64(p.ID)})
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"user_id": uint(1), "external_user_id": "a"}}, users)

	courses, err := sh.lookupCoursesMapping(int(p.ID))
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"course_id": int64(1), "external_course_id": "x"}}, courses)
}

func TestDBUint(t *testing.T) {
	tests := []struct {
		value  any
		want   uint
		wantOK bool
	}{
		{uint(3), 3, true},
		{int64(4), 4, true},
		{5, 5, true},
		{float64(6), 6, true},
		{"7", 0, false},
		{nil, 0, false},
	}
	for _, tt := range tests {
		got, ok := dbUint(map[string]any{"id": tt.value}, "id")
		assert.Equal(t, tt.want, got, "%#v", tt.value)
		assert.Equal(t, tt.wantOK, ok, "%#v", tt.value)
	}
}

// parseDate never returns nil: an empty or bad date is the zero time.
func TestParseDate(t *testing.T) {
	const pattern = "2006-01-02T15:04:05Z"
	assert.Equal(t, time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC), *parseDate("2026-01-05T08:00:00Z", pattern))
	assert.True(t, parseDate("", pattern).IsZero())
	assert.True(t, parseDate("Jan 5", pattern).IsZero())
}
