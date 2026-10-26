package providerplatforms

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/jobs"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newTestDB returns an empty in-memory database with Provider Platforms'
// tables and a minimal users table.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	t.Setenv("APP_KEY", "test-app-key")
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(Module{}.TestModels()...))
	require.NoError(t, db.Exec(`CREATE TABLE users (id integer PRIMARY KEY, username text, role text)`).Error)
	return db
}

func createProvider(t *testing.T, db *gorm.DB, p ProviderPlatform) *ProviderPlatform {
	t.Helper()
	if p.State == "" {
		p.State = Enabled
	}
	require.NoError(t, db.Create(&p).Error)
	return &p
}

// createResident adds a user with a mapping to the provider and returns the
// user's id.
func createResident(t *testing.T, db *gorm.DB, providerID uint, id uint, role, externalID string) uint {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO users (id, username, role) VALUES (?, ?, ?)`, id, fmt.Sprintf("user%d", id), role).Error)
	require.NoError(t, db.Create(&ProviderUserMapping{
		UserID:             id,
		ProviderPlatformID: providerID,
		ExternalUserID:     externalID,
		ExternalUsername:   "ext" + externalID,
	}).Error)
	return id
}

// createTask adds the cron_jobs and runnable_tasks rows for a provider job,
// marked running, as the scheduler leaves them when it publishes the job.
func createTask(t *testing.T, db *gorm.DB, job jobs.Type, providerID uint, lastRun time.Time) (*cronJob, *runnableTask) {
	t.Helper()
	cj := cronJob{ID: uuid.NewString(), Name: string(job), Category: providerPlatformJob, Schedule: "0 22 * * *"}
	require.NoError(t, db.Create(&cj).Error)
	task := runnableTask{JobID: cj.ID, ProviderPlatformID: &providerID, Status: taskRunning, LastRun: lastRun}
	require.NoError(t, db.Create(&task).Error)
	return &cj, &task
}

// taskMsg is the message the scheduler publishes for the task (jobMsg).
func taskMsg(t *testing.T, job jobs.Type, task *runnableTask) *nats.Msg {
	t.Helper()
	msg, err := jobMsg(job, task, *task.ProviderPlatformID)
	require.NoError(t, err)
	return msg
}

// fakeCore stands in for the server's POST /upload, which stores thumbnails.
type fakeCore struct {
	*httptest.Server
	mu      sync.Mutex
	uploads []string
}

// newFakeCore starts a fakeCore and points APP_URL at it.
func newFakeCore(t *testing.T) *fakeCore {
	t.Helper()
	fc := &fakeCore{}
	fc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/upload" {
			http.NotFound(w, r)
			return
		}
		_, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fc.mu.Lock()
		fc.uploads = append(fc.uploads, header.Filename)
		fc.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":    map[string]string{"url": "/api/photos/" + header.Filename},
			"message": "uploaded",
		})
	}))
	t.Cleanup(fc.Close)
	t.Setenv("APP_URL", fc.URL)
	return fc
}

func (fc *fakeCore) Uploads() []string {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return append([]string(nil), fc.uploads...)
}

// procedureCall is one CALL statement, with its arguments after the user
// and course. The last argument, the time of the call, is left out.
type procedureCall struct {
	Procedure string
	Args      []any
}

// recordCalls records the stored procedure calls made through db. SQLite has
// no procedures, so the calls themselves fail and are logged.
func recordCalls(t *testing.T, db *gorm.DB) *[]procedureCall {
	t.Helper()
	calls := &[]procedureCall{}
	require.NoError(t, db.Callback().Raw().Before("gorm:raw").Register("test:calls", func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if !strings.HasPrefix(sql, "CALL ") {
			return
		}
		vars := tx.Statement.Vars
		*calls = append(*calls, procedureCall{
			Procedure: strings.TrimPrefix(sql[:strings.Index(sql, "(")], "CALL "),
			Args:      vars[:len(vars)-1],
		})
	}))
	return calls
}

// routeHosts sends every request made through http.DefaultTransport for one
// of hosts to target instead, for code that calls fixed URLs.
func routeHosts(t *testing.T, target string, hosts ...string) {
	t.Helper()
	to, err := url.Parse(target)
	require.NoError(t, err)
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if slices.Contains(hosts, r.URL.Host) {
			r = r.Clone(r.Context())
			r.URL.Scheme, r.URL.Host = to.Scheme, to.Host
		}
		return orig.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = orig })
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
