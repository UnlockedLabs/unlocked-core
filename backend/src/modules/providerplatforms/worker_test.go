package providerplatforms

import (
	"context"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/jobs"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var lastRun = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

func reloadTask(t *testing.T, w *worker, id uint) runnableTask {
	t.Helper()
	var task runnableTask
	require.NoError(t, w.db.First(&task, id).Error)
	return task
}

// A job marks its task pending when it ends, and moves last_run to now only
// when it succeeded, so a failed run is retried from the same point.
func TestCleanupJob(t *testing.T) {
	db := newTestDB(t)
	w := &worker{db: db}
	p1 := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "one"})
	p2 := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "two"})
	cj, task1 := createTask(t, db, jobs.GetCourses, p1.ID, lastRun)
	task2 := runnableTask{JobID: cj.ID, ProviderPlatformID: &p2.ID, Status: taskRunning, LastRun: lastRun}
	require.NoError(t, db.Create(&task2).Error)
	w.cleanupJob(context.Background(), int(p2.ID), cj.ID, false)
	got := reloadTask(t, w, task2.ID)
	assert.Equal(t, taskPending, got.Status)
	assert.True(t, got.LastRun.Equal(lastRun))

	before := time.Now()
	w.cleanupJob(context.Background(), int(p2.ID), cj.ID, true)
	got = reloadTask(t, w, task2.ID)
	assert.Equal(t, taskPending, got.Status)
	assert.False(t, got.LastRun.Before(before))

	other := reloadTask(t, w, task1.ID)
	assert.Equal(t, taskRunning, other.Status, "another provider's task is left alone")
	assert.True(t, other.LastRun.Equal(lastRun))
}

// A message the jobs can't use is logged and dropped. Once the provider id
// and job id are read, a missing provider also releases the task.
func TestInitProviderPlatformServiceBadMessages(t *testing.T) {
	db := newTestDB(t)
	w := &worker{db: db}
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
			_, err := w.initProviderPlatformService(context.Background(), &nats.Msg{Data: []byte(tt.data)})
			assert.EqualError(t, err, tt.want)
		})
	}

	t.Run("unknown provider", func(t *testing.T) {
		missing := uint(42)
		_, task := createTask(t, db, jobs.GetCourses, missing, lastRun)
		_, err := w.initProviderPlatformService(context.Background(), taskMsg(t, jobs.GetCourses, task))
		assert.EqualError(t, err, "failed to find provider: record not found")
		got := reloadTask(t, w, task.ID)
		assert.Equal(t, taskPending, got.Status)
		assert.True(t, got.LastRun.Equal(lastRun))
	})

	t.Run("unsupported type", func(t *testing.T) {
		p := createProvider(t, db, ProviderPlatform{Type: "moodle", Name: "m"})
		_, task := createTask(t, db, jobs.GetCourses, p.ID, lastRun)
		_, err := w.initProviderPlatformService(context.Background(), taskMsg(t, jobs.GetCourses, task))
		assert.EqualError(t, err, "unsupported provider type: moodle")
	})
}

func TestHandleCourses(t *testing.T) {
	db := newTestDB(t)
	w := &worker{db: db}
	fc := newFakeCanvas(t)
	fc.courses = []map[string]any{{"id": float64(101), "name": "Algebra", "course_code": "ALG-1"}}
	p := createCanvasProvider(t, db, fc)
	_, task := createTask(t, db, jobs.GetCourses, p.ID, lastRun)

	w.handleCourses(context.Background(), taskMsg(t, jobs.GetCourses, task))

	var names []string
	require.NoError(t, db.Model(&Course{}).Pluck("name", &names).Error)
	assert.Equal(t, []string{"Algebra"}, names)
	got := reloadTask(t, w, task.ID)
	assert.Equal(t, taskPending, got.Status)
	assert.True(t, got.LastRun.After(lastRun))
}

// A failed import releases the task without moving last_run.
func TestHandleCoursesFailure(t *testing.T) {
	db := newTestDB(t)
	w := &worker{db: db}
	fc := newFakeCanvas(t)
	fc.Close()
	p := createCanvasProvider(t, db, fc)
	_, task := createTask(t, db, jobs.GetCourses, p.ID, lastRun)

	w.handleCourses(context.Background(), taskMsg(t, jobs.GetCourses, task))

	got := reloadTask(t, w, task.ID)
	assert.Equal(t, taskPending, got.Status)
	assert.True(t, got.LastRun.Equal(lastRun))
}

// The milestones job reads milestones since the task's last_run for the
// provider's students only, and waits rateLimitWait after each course.
func TestHandleMilestones(t *testing.T) {
	db := newTestDB(t)
	w := &worker{db: db}
	fc := newFakeCanvas(t)
	fc.submissions = []map[string]any{
		{"id": float64(1), "user_id": float64(21), "workflow_state": "submitted", "submitted_at": "2026-03-02T00:00:00Z"},
		{"id": float64(2), "user_id": float64(23), "workflow_state": "submitted", "submitted_at": "2026-03-02T00:00:00Z"},
	}
	p := createCanvasProvider(t, db, fc)
	other := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "other"})
	createResident(t, db, p.ID, 1, "student", "21")
	createResident(t, db, p.ID, 2, "admin", "22")
	createResident(t, db, other.ID, 3, "student", "23")
	require.NoError(t, db.Create(&Course{ProviderPlatformID: p.ID, ExternalID: "101"}).Error)
	_, task := createTask(t, db, jobs.GetMilestones, p.ID, lastRun)

	start := time.Now()
	w.handleMilestonesForCourseUser(context.Background(), taskMsg(t, jobs.GetMilestones, task))
	assert.GreaterOrEqual(t, time.Since(start), rateLimitWait)

	var milestones []Milestone
	require.NoError(t, db.Find(&milestones).Error)
	require.Len(t, milestones, 1)
	assert.Equal(t, uint(1), milestones[0].UserID)
	assert.Contains(t, fc.Requests(), "GET /api/v1/courses/101/students/submissions?per_page=100&submitted_since=2026-03-01T00%3A00%3A00Z&user_ids%5B%5D=21 auth=Bearer token-123")
	got := reloadTask(t, w, task.ID)
	assert.Equal(t, taskPending, got.Status)
	assert.True(t, got.LastRun.After(lastRun))
}

// A canceled job stops between courses and leaves its task running.
func TestHandleMilestonesCanceled(t *testing.T) {
	db := newTestDB(t)
	w := &worker{db: db}
	fc := newFakeCanvas(t)
	p := createCanvasProvider(t, db, fc)
	require.NoError(t, db.Create(&Course{ProviderPlatformID: p.ID, ExternalID: "101"}).Error)
	_, task := createTask(t, db, jobs.GetMilestones, p.ID, lastRun)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w.handleMilestonesForCourseUser(ctx, taskMsg(t, jobs.GetMilestones, task))

	assert.Empty(t, fc.Requests())
	assert.Equal(t, taskRunning, reloadTask(t, w, task.ID).Status)
}

// Kolibri and Brightspace import milestones for the user mappings as the
// milestones job looks them up, where user_id arrives as uint (see dbUint).
// A mapping whose user_id they can't read is skipped.
func TestImportMilestonesMappedStudent(t *testing.T) {
	t.Run("kolibri", func(t *testing.T) {
		db := newTestDB(t)
		w := &worker{db: db}
		ks, kdb := newTestKolibriService(t, db)
		createResident(t, db, ks.ProviderPlatformID, 1, "student", "k1")
		require.NoError(t, kdb.Exec(`INSERT INTO kolibriauth_facilityuser VALUES ('k1', 'A', 'a', 'facility-1')`).Error)
		require.NoError(t, kdb.Exec(`INSERT INTO content_channelmetadata VALUES ('ch', 'Khan', '', '', 1, 'ch')`).Error)
		require.NoError(t, kdb.Exec(`INSERT INTO logger_contentsessionlog VALUES ('s1', 'ch', 'k1', '2026-03-01')`).Error)
		users, err := w.lookupUserMapping(map[string]any{"provider_platform_id": float64(ks.ProviderPlatformID)})
		require.NoError(t, err)
		require.IsType(t, uint(0), users[0]["user_id"])
		users = append(users, map[string]any{"user_id": "bad", "external_user_id": "k2"})

		course := map[string]any{"course_id": int64(5), "external_course_id": "ch"}
		require.NoError(t, ks.ImportMilestones(course, users, db, lastRun))

		var enrolled []uint
		require.NoError(t, db.Model(&UserEnrollment{}).Pluck("user_id", &enrolled).Error)
		assert.Equal(t, []uint{1}, enrolled)
	})

	t.Run("brightspace", func(t *testing.T) {
		db := newTestDB(t)
		w := &worker{db: db}
		useTempDir(t)
		fb := newFakeBrightspace(t)
		fb.datasets["User Enrollments"] = "OrgUnitId,UserId\n10,b1\n"
		fb.datasets["Assignment Submissions"] = "DropboxId,OrgUnitId,SubmitterId,IsGraded\n"
		fb.datasets["Quiz Attempts"] = "AttemptId,UserId,OrgUnitId,IsGraded,IsDeleted\n"
		p := createBrightspaceProvider(t, db, fb)
		createResident(t, db, p.ID, 1, "student", "b1")
		users, err := w.lookupUserMapping(map[string]any{"provider_platform_id": float64(p.ID)})
		require.NoError(t, err)
		require.IsType(t, uint(0), users[0]["user_id"])
		users = append(users, map[string]any{"user_id": "bad", "external_user_id": "b2"})

		course := map[string]any{"course_id": int64(5), "external_course_id": "10"}
		require.NoError(t, newTestBrightspaceService(t, db, p).ImportMilestones(course, users, db, lastRun))

		var enrolled []uint
		require.NoError(t, db.Model(&UserEnrollment{}).Pluck("user_id", &enrolled).Error)
		assert.Equal(t, []uint{1}, enrolled)
	})
}

// The activity job runs every course of the provider. A failed procedure
// call (as on SQLite) is logged and doesn't fail the job.
func TestHandleActivity(t *testing.T) {
	db := newTestDB(t)
	w := &worker{db: db}
	fc := newFakeCanvas(t)
	fc.enrollments = []map[string]any{{"user_id": float64(21), "total_activity_time": float64(600)}}
	p := createCanvasProvider(t, db, fc)
	createResident(t, db, p.ID, 1, "student", "21")
	require.NoError(t, db.Create(&Course{ProviderPlatformID: p.ID, ExternalID: "101"}).Error)
	require.NoError(t, db.Create(&Course{ProviderPlatformID: p.ID, ExternalID: "102"}).Error)
	_, task := createTask(t, db, jobs.GetActivity, p.ID, lastRun)
	calls := recordCalls(t, db)

	w.handleActivityForCourse(context.Background(), taskMsg(t, jobs.GetActivity, task))

	var enrollments []UserEnrollment
	require.NoError(t, db.Order("course_id").Find(&enrollments).Error)
	require.Len(t, enrollments, 2)
	assert.Equal(t, []procedureCall{
		{"insert_daily_activity_canvas", []any{uint(1), uint(1), "course_interaction", uint(600), "101"}},
		{"insert_daily_activity_canvas", []any{uint(1), uint(2), "course_interaction", uint(600), "102"}},
	}, *calls)
	got := reloadTask(t, w, task.ID)
	assert.Equal(t, taskPending, got.Status)
	assert.True(t, got.LastRun.After(lastRun))
}

// A course that fails doesn't stop the others, but the run doesn't count:
// last_run stays where it was.
func TestHandleActivityFailure(t *testing.T) {
	db := newTestDB(t)
	w := &worker{db: db}
	fc := newFakeCanvas(t)
	fc.Close()
	p := createCanvasProvider(t, db, fc)
	require.NoError(t, db.Create(&Course{ProviderPlatformID: p.ID, ExternalID: "101"}).Error)
	_, task := createTask(t, db, jobs.GetActivity, p.ID, lastRun)

	w.handleActivityForCourse(context.Background(), taskMsg(t, jobs.GetActivity, task))

	got := reloadTask(t, w, task.ID)
	assert.Equal(t, taskPending, got.Status)
	assert.True(t, got.LastRun.Equal(lastRun))
}

func TestLookupMappings(t *testing.T) {
	db := newTestDB(t)
	w := &worker{db: db}
	p := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "p"})
	other := createProvider(t, db, ProviderPlatform{Type: CanvasCloud, Name: "o"})
	createResident(t, db, p.ID, 1, "student", "a")
	createResident(t, db, p.ID, 2, "facility_admin", "b")
	createResident(t, db, other.ID, 3, "student", "c")
	require.NoError(t, db.Create(&Course{ProviderPlatformID: p.ID, ExternalID: "x"}).Error)
	require.NoError(t, db.Create(&Course{ProviderPlatformID: other.ID, ExternalID: "y"}).Error)

	users, err := w.lookupUserMapping(map[string]any{"provider_platform_id": float64(p.ID)})
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"user_id": uint(1), "external_user_id": "a"}}, users)

	courses, err := w.lookupCoursesMapping(int(p.ID))
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
