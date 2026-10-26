package programs

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-contracts/users"
	log "github.com/sirupsen/logrus"
)

func (srv *server) handleGetUserPrograms(w http.ResponseWriter, r *http.Request, log sLog) error {
	id := r.PathValue("id")
	userId, err := strconv.Atoi(id)
	if err != nil {
		return newInvalidIdServiceError(err, "error converting user_id")
	}
	queryCtx := getQueryContext(r)
	// The resident profile builds its active / completed / incomplete cards from
	// the full enrollment set, so return all of them rather than a single page
	// (otherwise residents with >per_page enrollments lose rows, e.g. completed
	// programs falling off page 1).
	queryCtx.All = true
	userPrograms, err := srv.store.GetUserProgramInfo(&queryCtx, userId)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	for i := range userPrograms {
		userPrograms[i].CalculateAttendancePercentage()
		userPrograms[i].Schedule = FormatScheduleFromRRule(userPrograms[i].RecurrenceRule)
	}
	claims := claimsFrom(r)
	if claims.HasFeature(features.ProviderPlatforms) {
		canvasPrograms := srv.fetchCanvasUserPrograms(userId)
		userPrograms = append(userPrograms, canvasPrograms...)
		queryCtx.Total += int64(len(canvasPrograms))
	}
	return writePaginatedResponse(w, http.StatusOK, userPrograms, queryCtx.IntoMeta())
}

func (srv *server) fetchCanvasUserPrograms(userID int) []ResidentProgramClassInfo {
	ctx := context.Background()
	mappings, err := srv.providerPlatforms.UserMappings(ctx, uint(userID))
	if err != nil || len(mappings) == 0 {
		return nil
	}
	providers, err := srv.activeProviders(ctx)
	if err != nil {
		return nil
	}
	providerMap := make(map[uint]ppapi.Provider, len(providers))
	for _, p := range providers {
		providerMap[p.ID] = p
	}
	var result []ResidentProgramClassInfo
	for _, mapping := range mappings {
		provider, ok := providerMap[mapping.ProviderID]
		if !ok || !isLiveProgramProvider(&provider) {
			continue
		}
		if isCanvasProvider(&provider) {
			result = append(result, srv.fetchCanvasCoursesForUser(&provider, mapping.ExternalUserID)...)
			continue
		}
		result = append(result, srv.fetchLiveCoursesForUser(&provider, mapping.ExternalUserID)...)
	}
	return result
}

// fetchLiveCoursesForUser builds a resident's program rows for a live provider
// that reports its own per-user enrollments. Canvas has a dedicated endpoint and
// is handled by fetchCanvasCoursesForUser instead.
func (srv *server) fetchLiveCoursesForUser(provider *ppapi.Provider, externalUserID string) []ResidentProgramClassInfo {
	reader, err := srv.newLiveProgramProvider(provider)
	if err != nil {
		return nil
	}
	lister, ok := reader.(liveUserCourseLister)
	if !ok {
		return nil
	}
	courses, err := lister.ListUserCourses(context.Background(), externalUserID)
	if err != nil {
		log.WithError(err).Warnf("failed to fetch courses for user %s on provider %d", externalUserID, provider.ID)
		return nil
	}
	programID := CanvasProgramIDOffset + provider.ID
	result := make([]ResidentProgramClassInfo, 0, len(courses))
	for _, course := range courses {
		enrollment := Enrolled
		endDate := ""
		if course.endDt != nil {
			endDate = course.endDt.Format(time.RFC3339)
			if !course.endDt.After(time.Now()) {
				enrollment = EnrollmentCompleted
			}
		}
		// A provider can mark a class finished without giving it an end date --
		// Essential Ed does exactly that when active is 0 -- so the status wins.
		if course.status == Completed {
			enrollment = EnrollmentCompleted
		}
		startDate := ""
		if !course.startDt.IsZero() {
			startDate = course.startDt.Format(time.RFC3339)
		}
		result = append(result, ResidentProgramClassInfo{
			ProgramName:          provider.Name,
			ClassName:            course.name,
			ProgramID:            programID,
			EnrollmentStatus:     enrollment,
			StartDate:            startDate,
			EndDate:              endDate,
			AttendancePercentage: "--",
			IsCanvas:             true,
			Source:               providerSourceLabel(provider),
		})
	}
	return result
}

func (srv *server) fetchCanvasCoursesForUser(provider *ppapi.Provider, canvasUserID string) []ResidentProgramClassInfo {
	apiURL := fmt.Sprintf("%s/api/v1/users/%s/courses?enrollment_state=active&per_page=100", provider.BaseURL, canvasUserID)
	var result []ResidentProgramClassInfo
	for apiURL != "" {
		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			return result
		}
		req.Header.Add("Authorization", "Bearer "+provider.AccessKey)
		req.Header.Add("Accept", "application/json")
		resp, err := srv.Client.Do(req)
		if err != nil {
			return result
		}
		var courses []map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&courses); err != nil {
			_ = resp.Body.Close()
			return result
		}
		_ = resp.Body.Close()
		programID := CanvasProgramIDOffset + provider.ID
		for _, course := range courses {
			name, _ := course["name"].(string)
			startAt, _ := course["start_at"].(string)
			endAt, _ := course["end_at"].(string)
			enrollment := EnrollmentCompleted
			if endAt == "" {
				enrollment = Enrolled
			} else if t, err := time.Parse(time.RFC3339, endAt); err == nil && t.After(time.Now()) {
				enrollment = Enrolled
			}
			result = append(result, ResidentProgramClassInfo{
				ProgramName:          provider.Name,
				ClassName:            name,
				ProgramID:            programID,
				EnrollmentStatus:     enrollment,
				StartDate:            startAt,
				EndDate:              endAt,
				AttendancePercentage: "--",
				IsCanvas:             true,
				Source:               providerSourceLabel(provider),
			})
		}
		apiURL = NextPageURL(resp.Header.Get("Link"))
	}
	return result
}

func (srv *server) handleGetUserAttendanceTrend(w http.ResponseWriter, r *http.Request, log sLog) error {
	id := r.PathValue("id")
	userId, err := strconv.Atoi(id)
	if err != nil {
		return newInvalidIdServiceError(err, "error converting user_id")
	}
	weeks := 8
	if wk := r.URL.Query().Get("weeks"); wk != "" {
		if parsed, err := strconv.Atoi(wk); err == nil && parsed > 0 {
			weeks = parsed
		}
	}
	service := NewUsersService(srv.store)
	trends, err := service.GetWeeklyAttendanceTrend(r.Context(), userId, weeks)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, trends)
}

func (srv *server) handleExportResidentAttendanceCSV(w http.ResponseWriter, r *http.Request, log sLog) error {
	userID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}

	claims := claimsFrom(r)
	queryCtx := getQueryContext(r)

	user, err := srv.store.GetUserByID(uint(userID))
	if err != nil {
		log.add("user_id", userID)
		return newDatabaseServiceError(err)
	}

	if claims.Role == users.FacilityAdmin && user.FacilityID != claims.FacilityID {
		return newUnauthorizedServiceError()
	}

	allFacilities := claims.CanSwitchFacility() || queryCtx.All
	var classID *uint
	if cid := r.URL.Query().Get("cohort_id"); cid != "" {
		parsed, err := strconv.Atoi(cid)
		if err != nil {
			return newInvalidIdServiceError(err, "class_id")
		}
		ref := uint(parsed)
		classID = &ref
	}
	csvData, err := srv.store.GetResidentAttendanceCSVData(r.Context(), uint(userID), claims.FacilityID, allFacilities, classID)
	if err != nil {
		log.add("user_id", userID)
		return newDatabaseServiceError(err)
	}

	toCSV, err := ResidentAttendanceToCSVFormat(csvData)
	if err != nil {
		return newInternalServerServiceError(err, "Failed to convert attendance data to CSV format")
	}

	docLabel := strings.TrimSpace(FormatNullableString(user.DocID))
	if docLabel == "" {
		docLabel = fmt.Sprintf("User%d", userID)
	}
	date := time.Now().Format("2006-01-02")
	filename := fmt.Sprintf("Attendance-%s-%s.csv", docLabel, date)

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.WriteHeader(http.StatusOK)

	writer := csv.NewWriter(w)
	if err := writer.WriteAll(toCSV); err != nil {
		return newInternalServerServiceError(err, "Failed to write CSV data")
	}

	log.add("user_id", userID)
	log.add("rows_exported", len(csvData))
	log.info("Exported resident attendance CSV")

	return nil
}
