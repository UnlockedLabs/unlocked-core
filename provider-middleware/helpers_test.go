package main

import (
	"context"
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

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms"

	"github.com/glebarez/sqlite"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newTestDB returns an empty in-memory database with Provider Platforms'
// tables, core's job tables and a minimal users table.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	t.Setenv("APP_KEY", "test-app-key")
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(providerplatforms.Module{}.TestModels()...))
	require.NoError(t, db.AutoMigrate(&models.CronJob{}, &models.RunnableTask{}))
	require.NoError(t, db.Exec(`CREATE TABLE users (id integer PRIMARY KEY, username text, role text)`).Error)
	return db
}

func newHandler(db *gorm.DB) *ServiceHandler {
	return &ServiceHandler{db: db, ctx: context.Background()}
}

func createProvider(t *testing.T, db *gorm.DB, p providerplatforms.ProviderPlatform) *providerplatforms.ProviderPlatform {
	t.Helper()
	if p.State == "" {
		p.State = providerplatforms.Enabled
	}
	require.NoError(t, db.Create(&p).Error)
	return &p
}

// createResident adds a user with a mapping to the provider and returns the
// user's id.
func createResident(t *testing.T, db *gorm.DB, providerID uint, id uint, role, externalID string) uint {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO users (id, username, role) VALUES (?, ?, ?)`, id, fmt.Sprintf("user%d", id), role).Error)
	require.NoError(t, db.Create(&providerplatforms.ProviderUserMapping{
		UserID:             id,
		ProviderPlatformID: providerID,
		ExternalUserID:     externalID,
		ExternalUsername:   "ext" + externalID,
	}).Error)
	return id
}

// createTask adds the runnable_tasks row for a provider job, as core's
// scheduler does, marked running.
func createTask(t *testing.T, db *gorm.DB, job models.JobType, providerID uint, lastRun time.Time) (*models.CronJob, *models.RunnableTask) {
	t.Helper()
	t.Setenv("MIDDLEWARE_CRON_SCHEDULE", "0 22 * * *")
	cj := models.CronJob{Name: string(job)}
	require.NoError(t, db.Create(&cj).Error)
	task := models.RunnableTask{JobID: cj.ID, ProviderPlatformID: &providerID, Status: models.StatusRunning, LastRun: lastRun}
	require.NoError(t, db.Create(&task).Error)
	return &cj, &task
}

// jobMsg is the message core's scheduler publishes for a provider job
// (tasks.runTask).
func jobMsg(t *testing.T, job models.JobType, jobID string, providerID uint, lastRun time.Time) *nats.Msg {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"job_type":             string(job),
		"job_id":               jobID,
		"provider_platform_id": &providerID,
		"last_run":             lastRun,
	})
	require.NoError(t, err)
	return &nats.Msg{Subject: job.PubName(), Data: data}
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
