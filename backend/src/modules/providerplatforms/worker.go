package providerplatforms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/jobs"

	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// The worker (`backend worker providerplatforms`) runs the LMS jobs that
// RegisterJobs publishes (jobs.go), and serves the provider user lists that
// the routes ask for (providerservice.go). It took them over from
// provider-middleware in EN-197.

const (
	// queueGroup makes each job run on one worker, however many are running.
	queueGroup = "providerplatforms"
	jobTimeout = 30 * time.Minute
	// rateLimitWait is how long the milestones job waits after each course, so
	// the provider doesn't rate-limit it.
	rateLimitWait = 5 * time.Second
	// usersAddr is where the worker serves the provider user lists. The server
	// reaches it at PROVIDER_SERVICE_URL.
	usersAddr = ":8081"
)

// lmsService is one provider's connection: Canvas, Brightspace or Kolibri.
type lmsService interface {
	GetUsers(st *store) ([]ImportUser, error)
	ImportCourses(st *store) error
	ImportMilestones(coursePair map[string]any, mappings []map[string]any, st *store, lastRun time.Time) error
	ImportActivityForCourse(coursePair map[string]any, st *store) error
	GetJobParams() map[string]any
}

type worker struct {
	store *store
}

// run subscribes to the LMS jobs and serves the user lists until ctx is
// canceled. Canceling ctx also stops the jobs that are running.
func (w *worker) run(ctx context.Context, nc *nats.Conn) error {
	for _, j := range w.subscriptions() {
		_, err := nc.QueueSubscribe(j.job.PubName(), queueGroup, func(msg *nats.Msg) {
			ctx, cancel := context.WithTimeout(ctx, jobTimeout)
			defer cancel()
			j.run(ctx, msg)
		})
		if err != nil {
			return fmt.Errorf("subscribe to %s: %w", j.job.PubName(), err)
		}
	}
	srv := &http.Server{Addr: usersAddr, Handler: w.routes(), ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.ListenAndServe() }()
	select {
	case err := <-served:
		return fmt.Errorf("serve the provider user lists: %w", err)
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

// workerJob is one job the worker runs.
type workerJob struct {
	job jobs.Type
	run func(ctx context.Context, msg *nats.Msg)
}

func (w *worker) subscriptions() []workerJob {
	return []workerJob{
		{jobs.GetCourses, w.handleCourses},
		{jobs.GetMilestones, w.handleMilestonesForCourseUser},
		{jobs.GetActivity, w.handleActivityForCourse},
	}
}

func (w *worker) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/users", w.handleUsers)
	mux.HandleFunc("GET /api/users/all", w.handleAllUsers)
	return mux
}

// handleCourses imports a provider's courses.
func (w *worker) handleCourses(ctx context.Context, msg *nats.Msg) {
	service, err := w.initProviderPlatformService(ctx, msg)
	if err != nil {
		log.WithFields(log.Fields{"error": err.Error()}).Error("Failed to initialize service")
		return
	}
	params := service.GetJobParams()
	jobId := params["job_id"].(string)
	providerPlatformId := int(params["provider_platform_id"].(float64))
	success := service.ImportCourses(w.store) == nil
	w.cleanupJob(ctx, providerPlatformId, jobId, success)
}

// handleUsers is GET /api/users?id=<provider id>: the provider's users that
// aren't linked to an Unlocked user yet. Only the server can reach it, so it
// doesn't check who's asking.
func (w *worker) handleUsers(rw http.ResponseWriter, r *http.Request) {
	fields := log.Fields{"handler": "handleUsers"}
	service, err := w.initServiceFromRequest(r.Context(), r)
	if err != nil {
		fields["error"] = err.Error()
		log.WithFields(fields).Error("Failed to initialize service")
		// Without a status the caller gets 200 and an empty body, which reads as
		// "this provider has no users" instead of "this provider is unsupported".
		http.Error(rw, "Failed to initialize provider service", providerServiceStatus(err))
		return
	}
	users, err := service.GetUsers(w.store)
	if err != nil {
		log.WithFields(fields).Errorln("Failed to retrieve users")
		http.Error(rw, "Failed to retrieve users", http.StatusInternalServerError)
		return
	}
	responseData, err := json.Marshal(&users)
	if err != nil {
		log.WithFields(fields).Errorln("Failed to marshal users")
		http.Error(rw, "Failed to encode response", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	if _, err = rw.Write(responseData); err != nil {
		http.Error(rw, "Failed to write response", http.StatusInternalServerError)
		return
	}
}

// handleAllUsers returns all Canvas users including those already mapped,
// so the backend can resolve canvas display names for the mapped-users page.
func (w *worker) handleAllUsers(rw http.ResponseWriter, r *http.Request) {
	fields := log.Fields{"handler": "handleAllUsers"}
	service, err := w.initServiceFromRequest(r.Context(), r)
	if err != nil {
		fields["error"] = err.Error()
		log.WithFields(fields).Error("Failed to initialize service")
		http.Error(rw, "Failed to initialize service", providerServiceStatus(err))
		return
	}
	canvas, ok := service.(*CanvasService)
	if !ok {
		// Non-Canvas providers don't need this endpoint; return empty list.
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte("[]"))
		return
	}
	users, err := canvas.GetAllUsers()
	if err != nil {
		log.WithFields(fields).Errorln("Failed to retrieve all users")
		http.Error(rw, "Failed to retrieve users", http.StatusInternalServerError)
		return
	}
	responseData, err := json.Marshal(&users)
	if err != nil {
		log.WithFields(fields).Errorln("Failed to marshal users")
		http.Error(rw, "Failed to encode response", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	if _, err = rw.Write(responseData); err != nil {
		http.Error(rw, "Failed to write response", http.StatusInternalServerError)
	}
}

// handleMilestonesForCourseUser imports the milestones of the provider's
// students in each of its courses since the task's last run.
func (w *worker) handleMilestonesForCourseUser(ctx context.Context, msg *nats.Msg) {
	service, err := w.initProviderPlatformService(ctx, msg)
	if err != nil {
		log.WithFields(log.Fields{"error": err.Error()}).Error("Failed to initialize service")
		return
	}
	log.Println("initiating GetMilestonesForCourseUser milestones")
	params := service.GetJobParams()
	jobId := params["job_id"].(string)
	providerPlatformId := int(params["provider_platform_id"].(float64))
	usersMap, err := w.lookupUserMapping(params)
	if err != nil {
		w.cleanupJob(ctx, providerPlatformId, jobId, false)
	}
	coursesMap, err := w.lookupCoursesMapping(providerPlatformId)
	if err != nil {
		w.cleanupJob(ctx, providerPlatformId, jobId, false)
	}
	success := true
	lastRunStr := params["last_run"].(string)
	lastRun, err := time.Parse(time.RFC3339, lastRunStr)
	if err != nil {
		w.cleanupJob(ctx, providerPlatformId, jobId, false)
		return
	}
	for _, course := range coursesMap {
		select {
		case <-ctx.Done():
			log.Println("context cancelled for getMilestones")
			return
		default:
			err = service.ImportMilestones(course, usersMap, w.store, lastRun)
			time.Sleep(rateLimitWait)
			if err != nil {
				success = false
				log.Errorf("Failed to retrieve milestones: %v", err)
				continue
			}
		}
	}
	w.cleanupJob(ctx, providerPlatformId, jobId, success)
}

// handleActivityForCourse imports the activity in each of the provider's
// courses.
func (w *worker) handleActivityForCourse(ctx context.Context, msg *nats.Msg) {
	service, err := w.initProviderPlatformService(ctx, msg)
	if err != nil {
		log.WithFields(log.Fields{"error": err.Error()}).Error("Failed to initialize service")
		return
	}
	success := true
	params := service.GetJobParams()
	jobId := params["job_id"].(string)
	providerPlatformId := int(params["provider_platform_id"].(float64))
	courses, err := w.lookupCoursesMapping(providerPlatformId)
	if err != nil {
		w.cleanupJob(ctx, providerPlatformId, jobId, false)
		return
	}
	for _, course := range courses {
		select {
		case <-ctx.Done():
			log.Println("context cancelled for getActivity")
			return
		default:
			err = service.ImportActivityForCourse(course, w.store)
			if err != nil {
				success = false
				log.Errorf("failed to get course activity: %v", err)
				continue
			}
		}
	}
	w.cleanupJob(ctx, providerPlatformId, jobId, success)
}

// The two ways initServiceFromRequest can fail on the caller's account rather
// than ours. Everything else it returns -- a database failure, a provider whose
// own auth setup is broken -- is our problem, not a bad request.
var (
	errInvalidProviderID   = errors.New("invalid provider id")
	errUnsupportedProvider = errors.New("unsupported provider type")
)

// providerServiceStatus maps an initServiceFromRequest error onto a response
// code, so the caller can tell "you asked wrong" from "we are broken".
func providerServiceStatus(err error) int {
	switch {
	case errors.Is(err, errInvalidProviderID), errors.Is(err, errUnsupportedProvider):
		return http.StatusBadRequest
	case errors.Is(err, gorm.ErrRecordNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func (w *worker) initServiceFromRequest(ctx context.Context, r *http.Request) (lmsService, error) {
	id, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil {
		log.Printf("Error: %v", err)
		return nil, fmt.Errorf("%w: %v", errInvalidProviderID, err)
	}
	provider, err := w.store.getProviderPlatform(ctx, id)
	if err != nil {
		log.Println("Failed to find provider")
		return nil, err
	}
	switch provider.Type {
	case Kolibri:
		return NewKolibriService(provider, nil), nil
	case CanvasCloud, CanvasOSS:
		return newCanvasService(provider, nil), nil
	case Brightspace:
		return newBrightspaceService(provider, w.store, nil)
	}
	return nil, fmt.Errorf("%w: %s", errUnsupportedProvider, provider.Type)
}

func (w *worker) initProviderPlatformService(ctx context.Context, msg *nats.Msg) (lmsService, error) {
	var body map[string]any
	if err := json.Unmarshal(msg.Data, &body); err != nil {
		log.Errorf("failed to unmarshal message: %v", err)
		return nil, fmt.Errorf("failed to unmarshal message: %v", err)
	}
	providerId, ok := body["provider_platform_id"].(float64)
	if !ok {
		return nil, fmt.Errorf("failed to parse provider_platform_id: %v", body["provider_platform_id"])
	}
	jobId, ok := body["job_id"].(string)
	if !ok {
		return nil, fmt.Errorf("failed to parse job_id: %v", body["job_id"])
	}
	// prior to here, we are unable to cleanup the job
	provider, err := w.store.getProviderPlatform(ctx, int(providerId))
	if err != nil {
		log.Errorf("error looking up provider platform: %v", err)
		w.cleanupJob(ctx, int(providerId), jobId, false)
		return nil, fmt.Errorf("failed to find provider: %v", err)
	}
	switch provider.Type {
	case Kolibri:
		return NewKolibriService(provider, body), nil
	case CanvasCloud, CanvasOSS:
		return newCanvasService(provider, body), nil
	case Brightspace:
		return newBrightspaceService(provider, w.store, body)
	}
	return nil, fmt.Errorf("unsupported provider type: %s", provider.Type)
}

// cleanupJob marks the provider's task for the job pending again. On success
// it also moves the task's last_run to now, so the next run picks up from
// here; after a failure the next run tries again from the same point.
func (w *worker) cleanupJob(ctx context.Context, providerID int, jobId string, success bool) {
	log.Infof("job %s succeeded?: %v \n cleaning up task", jobId, success)
	task, err := w.store.getTask(ctx, providerID, jobId)
	if err != nil {
		log.Errorf("failed to fetch task: %v", err)
		return
	}
	task.Status = taskPending
	if success {
		task.LastRun = time.Now()
	}
	if err := w.store.saveTask(ctx, task); err != nil {
		log.Errorf("failed to update task: %v", err)
		return
	}
}

func (w *worker) lookupUserMapping(params map[string]any) ([]map[string]any, error) {
	providerPlatformId := int(params["provider_platform_id"].(float64))
	users, err := w.store.getStudentMappings(providerPlatformId)
	if err != nil {
		log.Errorf("failed to fetch users: %v", err)
	}
	return users, nil
}

// dbUint reads a numeric column out of a map scanned from the database. gorm types map
// values off the model schema when the column name matches a field (so a uint column
// arrives as uint) and falls back to the driver's int64 when it does not, which is what
// an aliased column like "id as course_id" gets. Both shapes reach these services, so
// assert against neither.
func dbUint(row map[string]any, key string) (uint, bool) {
	switch n := row[key].(type) {
	case uint:
		return n, true
	case int64:
		return uint(n), true
	case int:
		return uint(n), true
	case float64:
		return uint(n), true
	default:
		return 0, false
	}
}

func (w *worker) lookupCoursesMapping(provId int) ([]map[string]any, error) {
	courses, err := w.store.getCourseMappings(provId)
	if err != nil {
		log.Errorf("failed to fetch courses: %v", err)
	}
	return courses, nil
}

func parseDate(dateToParse, pattern string) *time.Time {
	var (
		returnDt time.Time
		err      error
	)
	if dateToParse == "" {
		return &returnDt
	}
	returnDt, err = time.Parse(pattern, dateToParse)
	if err != nil {
		log.Errorf("error parsing date %s using pattern %s, error is: %v", dateToParse, pattern, err)
		return &returnDt
	}
	return &returnDt
}
