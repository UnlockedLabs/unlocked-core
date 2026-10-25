package programs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-contracts/users"
	logrus "github.com/sirupsen/logrus"
)

func (srv *server) handleGetClassesForProgram(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}
	if uint(id) >= CanvasProgramIDOffset {
		return srv.handleGetCanvasClasses(w, r, log, uint(id))
	}
	args := getQueryContext(r)
	service := NewClassesService(srv.store)
	classes, err := service.GetProgramClassDetailsForProgram(&args, id)
	if err != nil {
		log.add("program_id", id)
		return newDatabaseServiceError(err)
	}
	return writePaginatedResponse(w, http.StatusOK, classes, args.IntoMeta())
}

func (srv *server) handleGetClass(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	if uint(id) >= CanvasClassIDOffset {
		return srv.handleGetCanvasClassDetail(w, r, log, uint(id))
	}
	class, err := srv.store.GetCohortByID(id)
	if err != nil {
		log.add("class_id", id)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, class)
}

func (srv *server) handleIndexClassesForFacility(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := getQueryContext(r)
	claims := claimsFrom(r)
	facilityParam := r.URL.Query().Get("facility")
	var facilityID *uint
	if facilityParam == "all" && claims.CanSwitchFacility() {
		facilityID = nil
	} else {
		facilityID = &args.FacilityID
	}
	classes, err := srv.store.GetClasses(&args, facilityID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if claims.HasFeature(features.ProviderPlatforms) {
		// Provider classes are read over HTTP, which is far slower than our own
		// tables, so they are served from cache and refreshed in the background.
		// Our classes go out now; the provider's arrive on a later poll.
		canvasClasses, loading := srv.getCanvasProviderClasses(facilityID)
		canvasClasses = srv.filterClassesByProviderAccess(r.Context(), canvasClasses)
		classes = append(classes, canvasClasses...)
		args.Total += int64(len(canvasClasses))
		if loading {
			placeholders := srv.loadingClassPlaceholders(facilityID)
			classes = append(classes, placeholders...)
			args.Total += int64(len(placeholders))
		}
	}
	return writePaginatedResponse(w, http.StatusOK, classes, args.IntoMeta())
}

// filterClassesByProviderAccess drops Canvas classes belonging to a facility that has
// disabled provider_platforms via its per-facility override. The caller-level
// hasFeatureAccess check only reflects the requesting user's own facility, which isn't
// sufficient when classes span every facility (facility=all).
func (srv *server) filterClassesByProviderAccess(ctx context.Context, classes []ProgramClassCohort) []ProgramClassCohort {
	access := make(map[uint]bool)
	filtered := make([]ProgramClassCohort, 0, len(classes))
	for _, class := range classes {
		enabled, resolved := access[class.FacilityID]
		if !resolved {
			effective, err := srv.facilityFeatures.FacilityFeatures(ctx, class.FacilityID)
			if err != nil {
				logrus.WithError(err).WithField("facility_id", class.FacilityID).Warn("failed to resolve facility feature access, excluding canvas classes")
				enabled = false
			} else {
				enabled = slices.Contains(effective, features.ProviderPlatforms)
			}
			access[class.FacilityID] = enabled
		}
		if enabled {
			filtered = append(filtered, class)
		}
	}
	return filtered
}

func (srv *server) handleCreateClass(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("program_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}

	var class ProgramClassCohort
	err = json.Unmarshal(bodyBytes, &class)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}

	facilityID, err := requireFacilityID(r)
	if err != nil {
		return err
	}
	class.FacilityID = facilityID
	class.ProgramID = uint(id)

	if class.InstructorID == nil || *class.InstructorID == 0 {
		return newBadRequestServiceError(nil, "instructor selection is required")
	}

	instructorName, err := srv.store.GetInstructorNameByID(*class.InstructorID, class.FacilityID)
	if err != nil || instructorName == "" {
		return newBadRequestServiceError(err, "invalid instructor selected")
	}
	for i := range class.Events {
		class.Events[i].InstructorID = class.InstructorID
	}

	var conflictReq *ConflictCheckRequest
	if len(class.Events) > 0 && class.Events[0].RecurrenceRule != "" && class.Events[0].Duration != "" {
		event := class.Events[0]
		if event.RoomID != nil {
			if _, err := srv.store.GetRoomByIDForFacility(*event.RoomID, class.FacilityID); err != nil {
				return newDatabaseServiceError(err)
			}
		}
		conflictReq = &ConflictCheckRequest{
			FacilityID:     class.FacilityID,
			RecurrenceRule: event.RecurrenceRule,
			Duration:       event.Duration,
		}
		if event.RoomID != nil {
			conflictReq.RoomID = *event.RoomID
		}
		if class.InstructorID != nil {
			conflictReq.InstructorID = *class.InstructorID
		}
	}

	newClass, conflicts, err := srv.WithUserContext(r).CreateProgramClass(&class, conflictReq)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if len(conflicts) > 0 {
		return writeConflictResponse(w, conflicts)
	}
	log.add("program_id", id)
	log.add("class_id", newClass.ID)
	return writeJsonResponse(w, http.StatusCreated, newClass)
}

func (srv *server) handleUpdateClass(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}

	class := ProgramClassCohort{}
	err = json.Unmarshal(bodyBytes, &class)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}

	claims := claimsFrom(r)

	var rawClass map[string]any
	var instructorIDProvided bool
	if err := json.Unmarshal(bodyBytes, &rawClass); err == nil {
		if _, exists := rawClass["instructor_id"]; exists {
			instructorIDProvided = true
		}
	}

	existing, err := srv.store.GetCohortByID(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if existing.CannotUpdateClass() {
		return newBadRequestServiceError(err, "cannot perform action on class that is completed cancelled or archived")
	}

	if instructorIDProvided {
		if class.InstructorID == nil || *class.InstructorID == 0 {
			class.InstructorID = nil
		} else {
			instructorName, err := srv.store.GetInstructorNameByID(*class.InstructorID, existing.FacilityID)
			if err != nil || instructorName == "" {
				return newBadRequestServiceError(err, "invalid instructor selected")
			}
		}
		class.UpdateInstructor = true
	}
	enrolled, err := srv.store.GetTotalEnrollmentsByClassID(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if enrolled > class.Capacity {
		return writeJsonResponse(w, http.StatusBadRequest, "Cannot update class until unenrolling residents")
	}
	class.UpdateUserID = UintPtr(claims.UserID)

	var conflictReq *ConflictCheckRequest
	if len(existing.Events) > 0 {
		existingEvent := existing.Events[0]

		var roomChanged bool
		if len(class.Events) > 0 && class.Events[0].RoomID != nil {
			if _, err := srv.store.GetRoomByIDForFacility(*class.Events[0].RoomID, existing.FacilityID); err != nil {
				return newDatabaseServiceError(err)
			}
			existingRoomID := uint(0)
			if existingEvent.RoomID != nil {
				existingRoomID = *existingEvent.RoomID
			}
			if *class.Events[0].RoomID != existingRoomID {
				roomChanged = true
			}
		}

		var instructorChanged bool
		if instructorIDProvided && class.InstructorID != nil {
			existingInstructorID := uint(0)
			if existingEvent.InstructorID != nil {
				existingInstructorID = *existingEvent.InstructorID
			}
			if *class.InstructorID != existingInstructorID {
				instructorChanged = true
			}
		}

		if (roomChanged || instructorChanged) && existingEvent.RecurrenceRule != "" && existingEvent.Duration != "" {
			conflictReq = &ConflictCheckRequest{
				FacilityID:     existing.FacilityID,
				RecurrenceRule: existingEvent.RecurrenceRule,
				Duration:       existingEvent.Duration,
				ExcludeEventID: &existingEvent.ID,
				ExcludeClassID: &existing.ID,
			}
			if roomChanged {
				conflictReq.RoomID = *class.Events[0].RoomID
			}
			if instructorChanged {
				conflictReq.InstructorID = *class.InstructorID
			}
		}
	}

	updated, conflicts, err := srv.WithUserContext(r).UpdateProgramClass(&class, id, conflictReq)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if len(conflicts) > 0 {
		return writeConflictResponse(w, conflicts)
	}
	return writeJsonResponse(w, http.StatusOK, updated)
}

func (srv *server) handleUpdateClasses(w http.ResponseWriter, r *http.Request, log sLog) error {
	ids := r.URL.Query()["id"]
	classIDs := make([]int, 0, len(ids))
	for _, id := range ids {
		classID, err := strconv.Atoi(id)
		if err != nil {
			log.warn("invalid class ID in batch update, skipping: ", id)
			continue
		}
		classIDs = append(classIDs, classID)
	}
	classMap := make(map[string]any)
	if err := json.NewDecoder(r.Body).Decode(&classMap); err != nil {
		return newJSONReqBodyServiceError(err)
	}

	claims := claimsFrom(r)
	classMap["update_user_id"] = claims.UserID

	if err := srv.WithUserContext(r).UpdateProgramCohorts(classIDs, classMap); err != nil {
		return newDatabaseServiceError(err)
	}

	return writeJsonResponse(w, http.StatusOK, "Successfully updated program class")
}

func (srv *server) handleGetClassHistory(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	if uint(id) >= CanvasClassIDOffset {
		args := getQueryContext(r)
		return writePaginatedResponse(w, http.StatusOK,
			[]ChangeLogEntry{},
			newPaginationInfo(1, args.PerPage, 0))
	}
	args := getQueryContext(r)
	categories := r.URL.Query()["categories"]
	historyEvents, err := srv.store.GetChangeLogEntries(&args, TableNameCohort, id, categories)
	if err != nil {
		return err
	}
	pageMeta, createdByDetails, err := srv.getCreatedByForHistory(id, TableNameCohort, args.IntoMeta(), &args, len(historyEvents), categories)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if createdByDetails.Action != "" {
		historyEvents = append(historyEvents, createdByDetails)
	}
	return writePaginatedResponse(w, http.StatusOK, historyEvents, pageMeta)
}

func (srv *server) handleGetAttendanceFlagsForClass(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	if uint(id) >= CanvasClassIDOffset {
		return srv.handleGetCanvasAtRiskStudents(w, r, uint(id))
	}
	args := getQueryContext(r)
	flags, err := srv.store.GetAttendanceFlagsForClass(id, &args)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writePaginatedResponse(w, http.StatusOK, flags, args.IntoMeta())
}

func (srv *server) handleGetProgramClassOutcomes(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("program_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}
	args := getQueryContext(r)
	outcome, err := srv.store.GetProgramClassOutcomes(id, &args)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, outcome)
}

func (srv *server) handleGetMissingAttendance(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	args := getQueryContext(r)
	totalMissing, err := srv.store.GetMissingAttendance(id, &args)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, totalMissing)
}

func (srv *server) handleGetMissingAttendanceForFacility(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := getQueryContext(r)
	facility := r.URL.Query().Get("facility")
	var facilityId *uint
	switch facility {
	case "all":
		facilityId = nil
	case "":
		facilityId = &args.FacilityID
	default:
		facilityId = &args.FacilityID
	}

	days := 3
	if daysQuery := r.URL.Query().Get("days"); daysQuery != "" {
		if parsedDays, err := strconv.Atoi(daysQuery); err == nil {
			days = parsedDays
		}
	}

	service := NewClassesService(srv.store)
	items, err := service.GetMissingAttendanceForFacility(&args, facilityId, days)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	args.Total = int64(len(items))
	if !args.All {
		start := args.CalcOffset()
		if start < len(items) {
			end := start + args.PerPage
			if end > len(items) {
				end = len(items)
			}
			items = items[start:end]
		} else {
			items = []MissingAttendanceItem{}
		}
	}

	return writePaginatedResponse(w, http.StatusOK, items, args.IntoMeta())
}

func (srv *server) handleGetCumulativeAttendanceRate(w http.ResponseWriter, r *http.Request, log sLog) error {
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	if uint(classID) >= CanvasClassIDOffset {
		return writeJsonResponse(w, http.StatusOK, map[string]*float64{"attendance_rate": nil})
	}
	attendanceRate, err := srv.store.GetCumulativeAttendanceRateForClass(r.Context(), classID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	response := map[string]*float64{
		"attendance_rate": attendanceRate,
	}
	return writeJsonResponse(w, http.StatusOK, response)
}

func (srv *server) handleGetClassesByInstructor(w http.ResponseWriter, r *http.Request, log sLog) error {
	instructorId, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || instructorId < 0 {
		return newInvalidIdServiceError(fmt.Errorf("instructor ID must be 0 or positive"), "instructor ID")
	}

	claims := claimsFrom(r)

	startDate := r.URL.Query().Get("start_date")
	endDate := r.URL.Query().Get("end_date")

	if startDate == "" || endDate == "" {
		return newBadRequestServiceError(nil, "start_date and end_date are required")
	}

	classes, err := srv.store.GetClassesByInstructor(instructorId, int(claims.FacilityID), startDate, endDate)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	return writeJsonResponse(w, http.StatusOK, classes)
}

func (srv *server) handleBulkCancelSessions(w http.ResponseWriter, r *http.Request, log sLog) error {
	var req BulkCancelSessionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}

	claims := claimsFrom(r)

	// Instructors live on program_class_events, not on the cohort (id751 moved the
	// column), so validate against the same source the instructor picker draws from:
	// facility users with an instructor role. InstructorID 0 is the picker's
	// "Unassigned" option, which BulkCancelSessions handles as "events with no
	// instructor" -- there is no user row to check for it.
	if req.InstructorID != 0 {
		var count int64
		err := srv.store.WithContext(r.Context()).
			Table("users").
			Where("id = ? AND facility_id = ? AND role IN ? AND deactivated_at IS NULL AND deleted_at IS NULL",
				req.InstructorID, claims.FacilityID,
				[]users.Role{users.FacilityAdmin, users.DepartmentAdmin}).
			Count(&count).Error
		if err != nil {
			return newDatabaseServiceError(err)
		}
		if count == 0 {
			return newBadRequestServiceError(nil,
				"instructor not found or does not belong to your facility")
		}
	}

	claimsAdapter := &BulkCancelClaimsAdapter{Claims: claims}

	response, err := srv.store.BulkCancelSessions(&req, int(claims.FacilityID), claimsAdapter)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	log.add("instructor_id", req.InstructorID)
	log.add("start_date", req.StartDate)
	log.add("end_date", req.EndDate)
	log.add("session_count", response.SessionCount)
	log.add("class_count", response.ClassCount)

	return writeJsonResponse(w, http.StatusOK, response)
}

type BulkCancelClaimsAdapter struct {
	*identity.Claims
}

func (c *BulkCancelClaimsAdapter) GetUserID() uint {
	return c.UserID
}

func (c *BulkCancelClaimsAdapter) GetFacilityID() uint {
	return c.FacilityID
}

func (c *BulkCancelClaimsAdapter) GetTimezone() string {
	return c.TimeZone
}

func (srv *server) handleGetClassDeleteCheck(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	log.add("class_id", id)
	if uint(id) >= CanvasClassIDOffset {
		payload := struct {
			CanDelete bool                   `json:"can_delete"`
			Blockers  DeleteBlockingChildren `json:"blockers"`
		}{
			CanDelete: false,
			Blockers:  DeleteBlockingChildren{NonDeletableStatus: "Canvas"},
		}
		return writeJsonResponse(w, http.StatusOK, payload)
	}
	blockers, err := srv.store.ClassBlockingChildren(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	payload := struct {
		CanDelete bool                   `json:"can_delete"`
		Blockers  DeleteBlockingChildren `json:"blockers"`
	}{
		CanDelete: !blockers.HasAny(),
		Blockers:  blockers,
	}
	return writeJsonResponse(w, http.StatusOK, payload)
}

func (srv *server) handleDeleteClass(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	log.add("class_id", id)
	if uint(id) >= CanvasClassIDOffset {
		return newBadRequestServiceError(fmt.Errorf("canvas classes cannot be deleted"), "class ID")
	}

	blockers, err := srv.store.ClassBlockingChildren(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if blockers.HasAny() {
		log.info("class delete blocked by child records")
		return writeDeleteConflictResponse(w, "cannot delete: class has child records", blockers)
	}

	if err := srv.store.DeleteClass(id); err != nil {
		return newDatabaseServiceError(err)
	}
	log.info("class deleted")
	return writeJsonResponse(w, http.StatusNoContent, "Class deleted successfully")
}
