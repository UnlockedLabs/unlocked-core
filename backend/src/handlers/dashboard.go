package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	ocapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/opencontent/api"

	"github.com/nats-io/nats.go"
)

func (srv *Server) registerDashboardRoutes() []routeDef {
	resolver := UserRoleResolver("id")
	return []routeDef{
		newAdminRoute("GET /api/department-metrics", srv.handleDepartmentMetrics),
		newAdminRoute("GET /api/department-metrics/login-trend", srv.handleDepartmentLoginTrend),
		newAdminRoute("GET /api/department-metrics/facility-comparison", srv.handleFacilityEngagementComparison),
		newAdminRoute("GET /api/department-metrics/knowledge-center", srv.handleKnowledgeCenterMetrics),
		newAdminRoute("GET /api/department-metrics/programs/engagement-overview", srv.handleProgramEngagementOverview),
		newAdminRoute("GET /api/department-metrics/programs/second-enrollment", srv.handleSecondProgramEnrollment),
		newAdminRoute("GET /api/department-metrics/programs/completion-matrix", srv.handleProgramCompletionMatrix),
		newAdminRoute("GET /api/department-metrics/programs/load-distribution", srv.handleProgramLoadDistribution),
		newAdminRoute("GET /api/department-metrics/programs/enrollment-by-type", srv.handleEnrollmentByProgramType),
		newAdminRoute("GET /api/dashboard/class-metrics", srv.handleClassDashboardMetrics),
		newAdminRoute("GET /api/dashboard/facility-health", srv.handleFacilityHealthSummary),
		newAdminRoute("GET /api/users/{id}/admin-layer2", srv.handleAdminLayer2),
		validatedRoute("GET /api/users/{id}/profile", srv.handleResidentProfile, resolver),
	}
}

func (srv *Server) handleResidentProfile(w http.ResponseWriter, r *http.Request, log sLog) error {
	userID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	user, err := srv.Db.GetUserByID(uint(userID))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil {
		days = 30
	}
	loginData, err := srv.Db.GetUserSessionEngagement(userID, days)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	activityEngagement, err := srv.Db.GetUserEngagement(userID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	reading, err := srv.openContent.ActivityEngagement(r.Context(), uint(userID))
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting engagement_activity"))
	}
	activityEngagement.TotalActiveDaysMonthly = reading.TotalActiveDaysMonthly
	activityEngagement.TotalHoursActiveMonthly = reading.TotalHoursActiveMonthly
	activityEngagement.TotalHoursActiveWeekly = reading.TotalHoursActiveWeekly
	activityEngagement.TotalMinutesActiveWeekly = reading.TotalMinutesActiveWeekly
	activityEngagement.TotalHoursEngaged = reading.TotalHoursEngaged
	activityEngagement.TotalMinutesEngaged = reading.TotalMinutesEngaged
	args := srv.getQueryContext(r)
	topLibraries, err := srv.openContent.TopLibraries(args.Ctx, uint(userID), args.FacilityID)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting top 5 libraries"))
	}

	recentVideos, err := srv.openContent.RecentVideos(r.Context(), uint(userID))
	if err != nil {
		return newDatabaseServiceError(err)
	}

	response := struct {
		User               models.User `json:"user"`
		LoginEngagement    any         `json:"session_engagement"`
		ActivityEngagement any         `json:"activity_engagement"`
		TopLibraries       any         `json:"top_libraries"`
		RecentVideos       any         `json:"recent_videos"`
	}{
		User:               *user,
		LoginEngagement:    loginData,
		ActivityEngagement: activityEngagement,
		TopLibraries:       topLibraries,
		RecentVideos:       recentVideos,
	}

	return writeJsonResponse(w, http.StatusOK, response)
}

func (srv *Server) handleAdminLayer2(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := r.Context().Value(ClaimsKey).(*Claims)
	clearCache := r.URL.Query().Get("reset") == "true"
	facility := r.URL.Query().Get("facility")
	var facilityId *uint
	switch facility {
	case "all":
		facilityId = nil
	case "":
		facilityId = &claims.FacilityID
	default:
		var err error
		facilityIdInt, err := strconv.Atoi(facility)
		if err != nil {
			return newInternalServerServiceError(err, "Facility ID")
		}
		ref := uint(facilityIdInt)
		facilityId = &ref
	}
	key := fmt.Sprintf("admin-layer2-%d", facilityId)
	cached, err := srv.buckets[AdminLayer2].Get(key)
	if err != nil && errors.Is(err, nats.ErrKeyNotFound) || clearCache {
		newCacheData, err := srv.getLayer2Data(r, log)
		if err != nil {
			return newInternalServerServiceError(err, "Error getting admin layer 2 data")
		}
		cacheBytes, err := json.Marshal(newCacheData)
		if err != nil {
			return newMarshallingBodyServiceError(err)
		}
		_, err = srv.buckets[AdminLayer2].Put(key, cacheBytes)
		if err != nil {
			return newInternalServerServiceError(err, "Error caching admin layer 2 data")
		}
		return writeJsonResponse(w, http.StatusOK, newCacheData)
	}
	var cachedData models.CachedDashboard[models.AdminLayer2Join]
	err = json.Unmarshal(cached.Value(), &cachedData)
	if err != nil {
		return newInternalServerServiceError(err, "Error unmarshalling cached data")
	}
	return writeJsonResponse(w, http.StatusOK, cachedData)
}

const metricsDateFormat = "2006-01-02"

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// resolvePresetRange resolves a preset range key (e.g. "30d") against the
// server clock, returning the inclusive start day and an exclusive end bound
// (midnight after today, so all of "today" is included). Resolving presets
// server-side keeps the window anchored to the same clock that timestamps the
// data, so a client in a different timezone can't request a window that ends
// before data recorded "now".
func resolvePresetRange(key string) (time.Time, time.Time, bool) {
	now := time.Now()
	today := startOfDay(now)
	endExclusive := today.AddDate(0, 0, 1)
	switch key {
	case "7d":
		return today.AddDate(0, 0, -6), endExclusive, true
	case "30d":
		return today.AddDate(0, 0, -29), endExclusive, true
	case "90d":
		return today.AddDate(0, 0, -89), endExclusive, true
	case "ytd":
		return time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location()), endExclusive, true
	default:
		return time.Time{}, time.Time{}, false
	}
}

func parseDateRangeRequest(r *http.Request) (*time.Time, *time.Time, string, error) {
	q := r.URL.Query()
	if q.Get("all_time") == "true" {
		return nil, nil, "all", nil
	}
	// Preset ranges are resolved against the server clock. Any start_date/end_date
	// the client also sends are ignored in favor of the preset.
	if rangeKey := strings.ToLower(q.Get("range")); rangeKey != "" && rangeKey != "custom" {
		start, end, ok := resolvePresetRange(rangeKey)
		if !ok {
			return nil, nil, "", newInvalidQueryParamServiceError(fmt.Errorf("unknown range %q", rangeKey), "range")
		}
		return &start, &end, fmt.Sprintf("%s_%s", start.Format(metricsDateFormat), end.Format(metricsDateFormat)), nil
	}
	startStr := q.Get("start_date")
	endStr := q.Get("end_date")
	if startStr == "" && endStr == "" {
		now := time.Now()
		startDay := startOfDay(now.AddDate(0, 0, -30))
		endDay := startOfDay(now).AddDate(0, 0, 1)
		return &startDay, &endDay, fmt.Sprintf("%s_%s", startDay.Format(metricsDateFormat), endDay.Format(metricsDateFormat)), nil
	}
	if startStr == "" || endStr == "" {
		return nil, nil, "", newBadRequestServiceError(fmt.Errorf("start date and end date are both required"), "date range")
	}
	start, err := time.Parse(metricsDateFormat, startStr)
	if err != nil {
		return nil, nil, "", newInvalidQueryParamServiceError(err, "start_date")
	}
	end, err := time.Parse(metricsDateFormat, endStr)
	if err != nil {
		return nil, nil, "", newInvalidQueryParamServiceError(err, "end_date")
	}
	if end.Before(start) {
		return nil, nil, "", newBadRequestServiceError(fmt.Errorf("end date must be on or after start date"), "date range")
	}
	// Clamp a future end date to the server's current day rather than rejecting
	// it: a client whose clock or timezone runs ahead of the server should still
	// get data instead of a 400.
	todayMidnight := startOfDay(time.Now())
	if end.After(todayMidnight) {
		end = todayMidnight
	}
	endExclusive := end.AddDate(0, 0, 1)
	return &start, &endExclusive, fmt.Sprintf("%s_%s", start.Format(metricsDateFormat), end.Format(metricsDateFormat)), nil
}

type DashboardMetrics struct {
	ActiveUsers       int64                  `json:"active_users"`
	TotalLogins       int64                  `json:"total_logins"`
	LoginsPerDay      int64                  `json:"logins_per_day"`
	PercentActive     int64                  `json:"percent_active"`
	PercentInactive   int64                  `json:"percent_inactive"`
	TotalResidents    int64                  `json:"total_residents"`
	TotalAdmins       int64                  `json:"total_admins"`
	Facility          string                 `json:"facility,omitempty"`
	NewResidentsAdded int64                  `json:"new_residents_added"`
	PeakLoginTimes    []models.LoginActivity `json:"peak_login_times"`
}

func (srv *Server) handleDepartmentMetrics(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	facility := r.URL.Query().Get("facility")
	claims := r.Context().Value(ClaimsKey).(*Claims)
	var facilityId *uint
	facilityName := ""
	clearCache := r.URL.Query().Get("reset") == "true"
	switch facility {
	case "all":
		facilityId = nil
		facilityName = "All"
	case "":
		facilityId = &claims.FacilityID
		facility = strconv.Itoa(int(claims.FacilityID))
		facilityName = claims.FacilityName
	default:
		facilityIdInt, err := strconv.Atoi(facility)
		if err != nil {
			return newInvalidIdServiceError(err, "facility")
		}
		ref := uint(facilityIdInt)
		facilityId = &ref
		facility, err := srv.Db.GetFacilityByID(facilityIdInt)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		facilityName = facility.Name
	}
	start, end, rangeKey, err := parseDateRangeRequest(r)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s-%s", facility, rangeKey)
	cached, err := srv.buckets[LoginMetrics].Get(key)
	if err != nil && errors.Is(err, nats.ErrKeyNotFound) || clearCache {
		activeUsers, totalDays, err := srv.Db.GetNumberOfActiveUsersForTimePeriod(&args, true, start, end, facilityId)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		totalLogins, err := srv.Db.GetTotalLogins(&args, start, end, facilityId)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		totalResidents, err := srv.Db.GetTotalUsers(&args, facilityId)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		totalAdmins, err := srv.Db.GetTotalAdmins(&args, facilityId)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		newResidentsAdded, err := srv.Db.NewUsersInTimePeriod(&args, start, end, facilityId)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		loginTimes, err := srv.Db.GetLoginActivity(&args, start, end, facilityId)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		percentActive := int64(0)
		if totalResidents == 0 {
			percentActive = 0
		} else {
			percentActive = activeUsers / totalResidents * 100
		}
		percentInactive := 100 - percentActive
		activityMetrics := DashboardMetrics{
			ActiveUsers:       activeUsers,
			TotalLogins:       totalLogins,
			LoginsPerDay:      totalLogins / int64(totalDays),
			PercentActive:     percentActive,
			PercentInactive:   percentInactive,
			TotalResidents:    totalResidents,
			TotalAdmins:       totalAdmins,
			Facility:          facilityName,
			NewResidentsAdded: newResidentsAdded,
			PeakLoginTimes:    loginTimes,
		}
		var cachedData models.CachedDashboard[DashboardMetrics]
		cachedData.LastCache = time.Now()
		cachedData.Data = activityMetrics

		cacheBytes, err := json.Marshal(cachedData)
		if err != nil {
			return newMarshallingBodyServiceError(err)
		}
		_, err = srv.buckets[LoginMetrics].Put(key, cacheBytes)
		if err != nil {
			return newInternalServerServiceError(err, "Error caching login metrics")
		}
		return writeJsonResponse(w, http.StatusOK, cachedData)
	} else if err != nil {
		return newInternalServerServiceError(err, "Error retrieving login metrics")
	}
	var cachedData models.CachedDashboard[DashboardMetrics]
	err = json.Unmarshal(cached.Value(), &cachedData)
	if err != nil {
		return newInternalServerServiceError(err, "Error unmarshalling cached data")
	}

	return writeJsonResponse(w, http.StatusOK, cachedData)
}

func (srv *Server) handleProgramEngagementOverview(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	claims := r.Context().Value(ClaimsKey).(*Claims)
	facilityID, err := resolveFacilityFilter(claims, r.URL.Query().Get("facility"), args.FacilityID)
	if err != nil {
		return err
	}
	start, end, _, err := parseDateRangeRequest(r)
	if err != nil {
		return err
	}
	overview, err := srv.programs.EngagementOverview(r.Context(), start, end, facilityID)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting program engagement"))
	}
	return writeJsonResponse(w, http.StatusOK, overview)
}

func (srv *Server) handleSecondProgramEnrollment(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	claims := r.Context().Value(ClaimsKey).(*Claims)
	facilityID, err := resolveFacilityFilter(claims, r.URL.Query().Get("facility"), args.FacilityID)
	if err != nil {
		return err
	}
	rows, err := srv.programs.SecondProgramEnrollment(r.Context(), facilityID)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting second program enrollment"))
	}
	return writeJsonResponse(w, http.StatusOK, rows)
}

func (srv *Server) handleProgramCompletionMatrix(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	claims := r.Context().Value(ClaimsKey).(*Claims)
	facilityID, err := resolveFacilityFilter(claims, r.URL.Query().Get("facility"), args.FacilityID)
	if err != nil {
		return err
	}
	cells, err := srv.programs.CompletionMatrix(r.Context(), facilityID)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting program completion matrix"))
	}
	return writeJsonResponse(w, http.StatusOK, cells)
}

func (srv *Server) handleProgramLoadDistribution(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	claims := r.Context().Value(ClaimsKey).(*Claims)
	facilityID, err := resolveFacilityFilter(claims, r.URL.Query().Get("facility"), args.FacilityID)
	if err != nil {
		return err
	}
	dist, err := srv.programs.LoadDistribution(r.Context(), facilityID)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting program load distribution"))
	}
	return writeJsonResponse(w, http.StatusOK, dist)
}

func (srv *Server) handleEnrollmentByProgramType(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	claims := r.Context().Value(ClaimsKey).(*Claims)
	facilityID, err := resolveFacilityFilter(claims, r.URL.Query().Get("facility"), args.FacilityID)
	if err != nil {
		return err
	}
	rows, err := srv.programs.EnrollmentByProgramType(r.Context(), facilityID)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting enrollment by program type"))
	}
	return writeJsonResponse(w, http.StatusOK, rows)
}

func resolveFacilityFilter(claims *Claims, facility string, ownFacilityID uint) (*uint, error) {
	switch {
	case facility == "all" && claims.canSwitchFacility():
		return nil, nil
	case facility != "" && facility != "all" && claims.canSwitchFacility():
		facilityIdInt, err := strconv.Atoi(facility)
		if err != nil {
			return nil, newInvalidIdServiceError(err, "facility")
		}
		ref := uint(facilityIdInt)
		return &ref, nil
	default:
		return &ownFacilityID, nil
	}
}

func (srv *Server) handleDepartmentLoginTrend(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	claims := r.Context().Value(ClaimsKey).(*Claims)
	facilityID, err := resolveFacilityFilter(claims, r.URL.Query().Get("facility"), args.FacilityID)
	if err != nil {
		return err
	}
	start, end, _, err := parseDateRangeRequest(r)
	if err != nil {
		return err
	}
	trend, err := srv.Db.GetDailyLoginActivity(&args, start, end, facilityID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, trend)
}

func (srv *Server) handleKnowledgeCenterMetrics(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	claims := r.Context().Value(ClaimsKey).(*Claims)
	facilityID, err := resolveFacilityFilter(claims, r.URL.Query().Get("facility"), args.FacilityID)
	if err != nil {
		return err
	}
	start, end, _, err := parseDateRangeRequest(r)
	if err != nil {
		return err
	}
	window := ocapi.Window{Start: start, End: end, FacilityID: facilityID}
	interactions, err := srv.openContent.Interactions(args.Ctx, window)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting open_content_activities"))
	}
	avgSession, err := srv.openContent.AvgSessionMinutes(args.Ctx, window)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting open_content_activities"))
	}
	repeat, err := srv.openContent.RepeatEngagement(args.Ctx, window)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting open_content_activities"))
	}
	categories, err := srv.openContent.LibraryViewsByCategory(args.Ctx, window)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting open_content_tags"))
	}
	topLibraries, err := srv.openContent.TopLibraryVisits(args.Ctx, window, 8)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting libraries"))
	}
	topVideos, err := srv.openContent.TopVideoVisits(args.Ctx, window, 8)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting videos"))
	}
	metrics := models.KnowledgeCenterMetrics{
		TotalInteractions:       interactions.Total,
		TotalInteractionsChange: interactions.Change,
		UniqueResidents:         interactions.Unique,
		AvgSessionMinutes:       avgSession,
		RepeatEngagement:        repeat,
		LibraryViewsByCategory:  categories,
		TopLibraries:            topLibraries,
		TopVideos:               topVideos,
	}
	return writeJsonResponse(w, http.StatusOK, metrics)
}

func (srv *Server) handleFacilityEngagementComparison(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := r.Context().Value(ClaimsKey).(*Claims)
	if !claims.canSwitchFacility() {
		return newUnauthorizedServiceError()
	}
	args := srv.getQueryContext(r)
	start, end, _, err := parseDateRangeRequest(r)
	if err != nil {
		return err
	}
	comparison, err := srv.Db.GetFacilityEngagementComparison(&args, start, end)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, comparison)
}

func (srv *Server) handleClassDashboardMetrics(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	facility := r.URL.Query().Get("facility")
	var facilityId *uint
	switch facility {
	case "all":
		facilityId = nil
	default:
		facilityId = &args.FacilityID
	}

	metrics, err := srv.programs.ClassMetrics(r.Context(), facilityId)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting class metrics"))
	}

	return writeJsonResponse(w, http.StatusOK, metrics)
}

func (srv *Server) handleFacilityHealthSummary(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	claims := r.Context().Value(ClaimsKey).(*Claims)
	facility := r.URL.Query().Get("facility")
	var facilityID *uint
	if facility == "all" && claims.canSwitchFacility() {
		facilityID = nil
	} else {
		facilityID = &args.FacilityID
	}

	days := 3
	if daysQuery := r.URL.Query().Get("days"); daysQuery != "" {
		if parsedDays, err := strconv.Atoi(daysQuery); err == nil {
			days = parsedDays
		}
	}

	summaries, err := srv.programs.FacilityHealth(r.Context(), facilityID, days, args.Timezone)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting facility health"))
	}

	return writeJsonResponse(w, http.StatusOK, summaries)
}

func (srv *Server) getLayer2Data(r *http.Request, log sLog) (models.CachedDashboard[models.AdminLayer2Join], error) {
	facility := r.URL.Query().Get("facility")
	claims := r.Context().Value(ClaimsKey).(*Claims)
	var facilityId *uint
	switch facility {
	case "all":
		facilityId = nil
	case "":
		facilityId = &claims.FacilityID
	default:
		facilityIdInt, err := strconv.Atoi(facility)
		if err != nil {
			return models.CachedDashboard[models.AdminLayer2Join]{}, newInvalidIdServiceError(err, "facility")
		}
		ref := uint(facilityIdInt)
		facilityId = &ref
	}
	stats, err := srv.providerPlatforms.LearningStats(r.Context(), facilityId)
	if err != nil {
		log.add("facilityId", claims.FacilityID)
		return models.CachedDashboard[models.AdminLayer2Join]{}, newDatabaseServiceError(database.NewDBError(err, "error getting learning stats"))
	}

	adminDashboard := models.AdminLayer2Join{
		TotalCoursesOffered:   stats.TotalCoursesOffered,
		TotalStudentsEnrolled: stats.TotalStudentsEnrolled,
		TotalHourlyActivity:   stats.TotalHourlyActivity,
		LearningInsights:      stats.Insights,
	}
	var cachedData models.CachedDashboard[models.AdminLayer2Join]
	cachedData.LastCache = time.Now()
	cachedData.Data = adminDashboard
	return cachedData, nil
}
