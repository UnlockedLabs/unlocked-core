package programs

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func (srv *server) handleGetUserProgramCompletions(w http.ResponseWriter, r *http.Request, log sLog) error {
	userId, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "User ID")
	}
	args := getQueryContext(r)
	classID := args.MaybeID("class_id")
	enrollemnt, err := srv.store.GetClassCompletionsForUser(&args, userId, classID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writePaginatedResponse(w, http.StatusOK, enrollemnt, args.IntoMeta())
}

func (srv *server) handleGetEnrollmentsForProgram(w http.ResponseWriter, r *http.Request, log sLog) error {
	classId, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "Class ID")
	}
	if uint(classId) >= CanvasClassIDOffset {
		return srv.handleGetCanvasClassEnrollments(w, r, log, uint(classId))
	}
	status := r.URL.Query().Get("status")
	if status == "all" {
		status = ""
	}
	log.add("status", status)
	args := getQueryContext(r)
	enrollments, err := srv.store.GetProgramClassEnrollmentsForProgram(&args, classId, status)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writePaginatedResponse(w, http.StatusOK, enrollments, args.IntoMeta())
}

func (srv *server) handleCheckEnrollmentConflicts(w http.ResponseWriter, r *http.Request, log sLog) error {
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	body := struct {
		UserIDs []int `json:"user_ids"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if len(body.UserIDs) == 0 {
		return writeJsonResponse(w, http.StatusOK, map[string]any{"conflicts": []any{}})
	}
	conflicts, err := srv.store.CheckSchedulingConflicts(classID, body.UserIDs)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, map[string]any{"conflicts": conflicts})
}

func (srv *server) handleEnrollUsersInClass(w http.ResponseWriter, r *http.Request, log sLog) error {
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	log.add("class_id", classID)
	class, err := srv.store.GetCohortByID(classID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if class.CannotUpdateClass() {
		return newBadRequestServiceError(err, "cannot perform action on class that is completed cancelled or archived")
	}
	enrollment := struct {
		UserIDs []int `json:"user_ids"`
		Confirm bool  `json:"confirm"` // Check for explicit confirmation
	}{}
	err = json.NewDecoder(r.Body).Decode(&enrollment)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}

	deactivatedUsers, err := srv.store.DeactivatedUsersPresent(enrollment.UserIDs)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if deactivatedUsers {
		return newBadRequestServiceError(errors.New("cannot enroll deactivated user"), "deactivated user")
	}

	if !enrollment.Confirm {
		conflicts, err := srv.store.CheckSchedulingConflicts(classID, enrollment.UserIDs)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		if len(conflicts) > 0 {
			return writeJsonResponse(w, http.StatusConflict, map[string]any{
				"message":   "Scheduling conflicts detected",
				"conflicts": conflicts,
			})
		}
	}

	skipped, err := srv.WithUserContext(r).CreateProgramClassEnrollments(classID, enrollment.UserIDs)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	response := "users enrolled"
	if skipped > 0 {
		response = fmt.Sprintf("%d users were enrolled, %d were not added because capacity is full.", len(enrollment.UserIDs)-skipped, skipped)
	}
	return writeJsonResponse(w, http.StatusCreated, response)
}

func (srv *server) handleDeleteProgramClassEnrollments(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "program class enrollment ID")
	}
	log.add("class_enrollment_id", id)
	err = srv.WithUserContext(r).DeleteProgramClassEnrollments(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	log.info("class enrollment deleted")
	return writeJsonResponse(w, http.StatusNoContent, "Class enrollment deleted successfully")
}

func (srv *server) handleUpdateProgramClassEnrollments(w http.ResponseWriter, r *http.Request, log sLog) error {
	classId, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class enrollment ID")
	}
	enrollment := struct {
		EnrollmentStatus ProgramEnrollmentStatus `json:"enrollment_status"`
		UserIDs          []int                   `json:"user_ids"`
		ChangeReason     *string                 `json:"change_reason,omitempty"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&enrollment); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if enrollment.EnrollmentStatus == "" {
		return newInvalidIdServiceError(errors.New("enrollment status is required"), "enrollment status")
	}
	class, err := srv.store.GetCohortByID(classId)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if class.CannotUpdateClassWithEnrollment(enrollment.EnrollmentStatus) {
		return newBadRequestServiceError(err, "cannot perform action due to invalid class or enrollment status")
	}
	switch enrollment.EnrollmentStatus {
	case "Completed":
		// The completion records the admin's email, from the users table as
		// core's claims had it.
		var admin *User
		admin, err = srv.store.GetUserByID(claimsFrom(r).UserID)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		err = srv.WithUserContext(r).GraduateEnrollments(admin.Email, enrollment.UserIDs, classId)
	default:
		err = srv.WithUserContext(r).UpdateProgramClassEnrollments(classId, enrollment.UserIDs, enrollment.EnrollmentStatus, enrollment.ChangeReason)
	}
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "updated")
}

func (srv *server) handleUpdateEnrollmentDate(w http.ResponseWriter, r *http.Request, log sLog) error {
	classId, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	enrollmentId, err := strconv.Atoi(r.PathValue("enrollment_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "enrollment ID")
	}
	enrollmentUpdate := struct {
		EnrolledDate string `json:"enrolled_date"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&enrollmentUpdate); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if enrollmentUpdate.EnrolledDate == "" {
		return newBadRequestServiceError(errors.New("enrolled date is required"), "enrolled date")
	}
	enrolledDate, err := time.Parse(time.RFC3339, enrollmentUpdate.EnrolledDate)
	if err != nil {
		return newBadRequestServiceError(err, "invalid date format")
	}
	class, err := srv.store.GetCohortByID(classId)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if class.CannotUpdateClass() {
		return newBadRequestServiceError(errors.New("cannot perform action on class that is completed cancelled or archived"), "invalid class status")
	}
	err = srv.WithUserContext(r).UpdateProgramClassEnrollmentDate(enrollmentId, enrolledDate)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	log.add("enrollment_id", enrollmentId)
	log.info("enrollment date updated")
	return writeJsonResponse(w, http.StatusOK, "updated")
}

func (srv *server) handleGetProgramClassEnrollmentsAttendance(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("enrollment_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class enrollment ID")
	}
	log.add("class_enrollment_id", id)
	page, perPage := getPaginationInfo(r)
	total, attendance, err := srv.store.GetProgramClassEnrollmentsAttendance(page, perPage, id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	log.add("total", total)
	log.info("class enrollment attendance fetched")
	paginationData := newPaginationInfo(page, perPage, total)
	return writePaginatedResponse(w, http.StatusOK, attendance, paginationData)
}
