package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/UnlockedLabs/unlocked-core/backend/src"
	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
	"github.com/UnlockedLabs/unlocked-core/backend/src/jasper"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	programsapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs/api"
)

// maxDocIDLen matches the users.doc_id column (VARCHAR(32), migration 00042).
// Without this bound an over-long value from a non-browser client reaches
// Postgres and fails as a 22001 (500) instead of a 400.
const maxDocIDLen = 32

func (srv *Server) registerUserRoutes() []routeDef {
	resolver := UserRoleResolver("id")
	return []routeDef{
		validatedRoute("GET /api/users/{id}", srv.handleShowUser, resolver),
		validatedRoute("GET /api/users/{id}/notes", srv.handleGetUserNotes, resolver),
		validatedAdminRoute("POST /api/users/{id}/notes", srv.handleCreateUserNote, FacilityAdminResolver("users", "id")),
		/* admin */
		newAdminRoute("GET /api/users/stats", srv.handleGetUserStats),
		newAdminRoute("GET /api/users", srv.handleIndexUsers),
		newAdminRoute("POST /api/users", srv.handleCreateUser),
		newAdminRoute("GET /api/users/resident-verify", srv.handleResidentVerification),
		newDeptAdminRoute("PATCH /api/users/resident-transfer", srv.handleResidentTransfer),
		validatedAdminRoute("POST /api/users/{id}/student-password", srv.handleResetStudentPassword, func(tx *database.DB, r *http.Request) bool {
			role, err := tx.GetUserRoleByID(r.Context(), r.PathValue("id"))
			return err == nil && canResetUserPassword(r.Context().Value(ClaimsKey).(*Claims), role)
		}),
		validatedAdminRoute("DELETE /api/users/{id}", srv.handleDeleteUser, FacilityAdminResolver("users", "id")),
		validatedAdminRoute("PATCH /api/users/{id}", srv.handleUpdateUser, FacilityAdminResolver("users", "id")),
		validatedAdminRoute("GET /api/users/{id}/account-history", srv.handleGetUserAccountHistory, resolver),
		newAdminRoute("POST /api/users/bulk/upload", srv.handleBulkUpload),
		newAdminRoute("POST /api/users/bulk/create", srv.handleBulkCreate),
		newAdminRoute("POST /api/users/bulk/reset-password", srv.handleBulkResetPassword),
		newAdminRoute("POST /api/users/bulk/deactivate", srv.handleBulkDeactivateUsers),
		newAdminRoute("POST /api/users/bulk/delete", srv.handleBulkDeleteUsers),
		validatedAdminRoute("POST /api/users/{id}/deactivate", srv.handleDeactivateUser, FacilityAdminResolver("users", "id")),
		validatedAdminRoute("GET /api/users/{id}/usage-report", srv.handleGenerateUsageReportPDF, FacilityAdminResolver("users", "id")),
	}
}

func (srv *Server) handleIndexUsers(w http.ResponseWriter, r *http.Request, log sLog) error {
	role := r.URL.Query().Get("role")
	// Eligible/unmapped resident branches need a concrete facility, so default to
	// home here; the statewide list branch resets to 0 below.
	args := srv.facilityScopedQueryContext(r)
	include := r.URL.Query()["include"]
	var users []models.User
	var err error
	switch {
	case slices.Contains(include, "only_unmapped"):
		providerId := r.URL.Query().Get("provider_id")
		return srv.handleGetUnmappedUsers(w, r, providerId, log)

	case slices.Contains(include, "only_unenrolled"):
		classID, err := strconv.Atoi(r.URL.Query().Get("cohort_id"))
		if err != nil {
			return newInvalidIdServiceError(err, "class ID")
		}
		users, err = srv.residentsForClassFilter(&args, srv.programs.EligibleUserIDs, classID, "users.created_at desc")
		if err != nil {
			log.add("facility_id", args.FacilityID)
			log.add("search", args.Search)
			return newDatabaseServiceError(err)
		}
	case slices.Contains(include, "only_enrolled"):
		classID, err := strconv.Atoi(r.URL.Query().Get("cohort_id"))
		if err != nil {
			return newInvalidIdServiceError(err, "class ID")
		}
		users, err = srv.residentsForClassFilter(&args, srv.programs.EnrolledUserIDs, classID, "users.name_last asc")
		if err != nil {
			log.add("facility_id", args.FacilityID)
			log.add("search", args.Search)
			return newDatabaseServiceError(err)
		}
	default:
		claims := r.Context().Value(ClaimsKey).(*Claims)
		if claims.canSwitchFacility() && r.URL.Query().Get("facility_id") == "" {
			args.FacilityID = 0
		}
		users, err = srv.Db.GetCurrentUsers(&args, role)
		if err != nil {
			log.add("facility_id", args.FacilityID)
			log.add("search", args.Search)
			return newDatabaseServiceError(err)
		}
	}
	return writePaginatedResponse(w, http.StatusOK, users, args.IntoMeta())
}

func (srv *Server) handleGetUserStats(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := r.Context().Value(ClaimsKey).(*Claims)
	var facilityID *uint
	if fid := r.URL.Query().Get("facility_id"); fid != "" {
		id, err := strconv.Atoi(fid)
		if err != nil {
			return newInvalidIdServiceError(err, "facility ID")
		}
		ref := uint(id)
		facilityID = &ref
	} else if !claims.canSwitchFacility() {
		facilityID = &claims.FacilityID
	}
	stats, err := srv.Db.GetUserStats(r.Context(), facilityID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, stats)
}

func (srv *Server) handleGetUnmappedUsers(w http.ResponseWriter, r *http.Request, providerId string, log sLog) error {
	log.add("subhandlerCall", "HandleGetUnmappedUsers")
	args := srv.getQueryContext(r)
	provID, err := strconv.Atoi(providerId)
	if err != nil {
		return newInvalidIdServiceError(err, "provider ID")
	}
	search := r.URL.Query()["search"]
	mappedUserIDs, err := srv.providerPlatforms.MappedUserIDs(r.Context(), uint(provID))
	if err != nil {
		log.add("provider_id", providerId)
		return newDatabaseServiceError(database.NewDBError(err, "error getting mapped users"))
	}
	users, err := srv.Db.GetUnmappedUsers(&args, mappedUserIDs, search)
	if err != nil {
		log.add("provider_id", providerId)
		log.add("facility_id", args.FacilityID)
		return newDatabaseServiceError(err)
	}
	return writePaginatedResponse(w, http.StatusOK, users, args.IntoMeta())
}

// residentsForClassFilter is a page of the users list's residents for one of
// Programs' class filters, which answers with the residents' IDs.
func (srv *Server) residentsForClassFilter(args *models.QueryContext, userIDs func(context.Context, uint) ([]uint, error), classID int, defaultOrder string) ([]models.User, error) {
	ids, err := userIDs(args.Ctx, uint(classID))
	if err != nil {
		return nil, database.NewDBError(err, "error getting users")
	}
	return srv.Db.GetResidentsByIDs(args, ids, defaultOrder)
}

/**
* GET: /api/users/{id}
**/
type UserResponse struct {
	ID            uint                 `json:"id"`
	Username      string               `json:"username"`
	NameFirst     string               `json:"name_first"`
	NameLast      string               `json:"name_last"`
	Email         string               `json:"email"`
	Role          models.UserRole      `json:"role"`
	DocID         *string              `json:"doc_id"`
	DeactivatedAt *time.Time           `json:"deactivated_at,omitempty"`
	Facility      *models.Facility     `json:"facility,omitempty"`
	LoginMetrics  *models.LoginMetrics `json:"login_metrics,omitempty"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
}

func (srv *Server) handleShowUser(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	user, err := srv.Db.GetUserByID(uint(id))
	if err != nil {
		log.add("userId", id)
		return newDatabaseServiceError(err)
	}

	resp := UserResponse{
		ID:            user.ID,
		Username:      user.Username,
		NameFirst:     user.NameFirst,
		NameLast:      user.NameLast,
		Email:         user.Email,
		Role:          user.Role,
		DocID:         user.DocID,
		DeactivatedAt: user.DeactivatedAt,
		Facility:      user.Facility,
		LoginMetrics:  user.LoginMetrics,
		CreatedAt:     user.CreatedAt,
		UpdatedAt:     user.UpdatedAt,
	}
	return writeJsonResponse(w, http.StatusOK, resp)
}

/**
* POST: /api/users
* TODO: transactional
**/
func (srv *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := r.Context().Value(ClaimsKey).(*Claims)
	reqForm := struct {
		User      models.User `json:"user"`
		Providers []int       `json:"provider_platforms"`
	}{}
	err := json.NewDecoder(r.Body).Decode(&reqForm)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if !claims.canSwitchFacility() {
		// A facility admin can only create users in their own facility.
		reqForm.User.FacilityID = claims.FacilityID
	} else if reqForm.User.FacilityID == 0 {
		return newBadRequestServiceError(nil, "facility selection is required")
	}
	invalidUser := validateUser(&reqForm.User)
	if invalidUser != "" {
		return newBadRequestServiceError(errors.New("invalid user"), invalidUser)
	}

	reqForm.User.DocID = models.NilIfBlank(strings.TrimSpace(models.FormatNullableString(reqForm.User.DocID)))
	if invalidDocID := validateResidentID(reqForm.User.Role, models.FormatNullableString(reqForm.User.DocID)); invalidDocID != "" {
		return newBadRequestServiceError(errors.New("invalid resident id"), invalidDocID)
	}
	log.add("created_username", reqForm.User.Username)
	userNameExists, docExists := srv.Db.UserIdentityExists(reqForm.User.Username, models.FormatNullableString(reqForm.User.DocID))
	if userNameExists {
		return newBadRequestServiceError(err, "Username already exists")
	}
	if docExists {
		return newBadRequestServiceError(err, "Resident ID already exists")
	}
	reqForm.User.Username = stripNonAlphaChars(reqForm.User.Username, isUsernameChar)
	err = srv.WithUserContext(r).CreateUser(&reqForm.User)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if len(reqForm.Providers) > 0 {
		providerIDs := make([]uint, len(reqForm.Providers))
		for i, id := range reqForm.Providers {
			providerIDs[i] = uint(id)
		}
		if err := srv.providerPlatforms.AddUserToProviders(r.Context(), toModuleUser(&reqForm.User), providerIDs); err != nil {
			log.add("provider_ids", reqForm.Providers)
			log.error("Error getting provider platform by id createProviderUserAccount")
			return newDatabaseServiceError(database.NewDBError(err, "provider_platforms not found"))
		}
	}
	tempPw, err := reqForm.User.CreateTempPassword()
	if err != nil {
		return newInternalServerServiceError(err, "Error creating temporary password")
	}
	response := struct {
		TempPassword     string      `json:"temp_password"`
		User             models.User `json:"user"`
		KratosRegistered bool        `json:"kratos_registered"`
	}{
		User:             reqForm.User,
		TempPassword:     tempPw,
		KratosRegistered: true,
	}
	if reqForm.User.Role == models.Student {
		accountCreation := models.NewUserAccountHistory(reqForm.User.ID, models.AccountCreation, &claims.UserID, nil, nil)
		if err := srv.Db.InsertUserAccountHistoryAction(r.Context(), accountCreation); err != nil {
			return newCreateRequestServiceError(err)
		}
		facilityTransfer := models.NewUserAccountHistory(reqForm.User.ID, models.FacilityTransfer, &claims.UserID, nil, &reqForm.User.FacilityID)
		if err := srv.Db.InsertUserAccountHistoryAction(r.Context(), facilityTransfer); err != nil {
			return newCreateRequestServiceError(err)
		}
	}
	if !srv.testingMode {
		// register the user as an Identity with Kratos + Kolibri
		if err := srv.HandleCreateUserKratos(reqForm.User.Username, tempPw); err != nil {
			log.infof("Error creating user in kratos: %v", err)
			response.KratosRegistered = false
		}
		// still return 201 because user has been created in kratos,
		// kolibri might not be set up/available
		if err := srv.providerPlatforms.AddUserToKolibri(r.Context(), toModuleUser(&reqForm.User)); err != nil {
			log.add("user_id", reqForm.User.ID)
			log.errorf("error creating user in kolibri: %v", err)
		}
	}
	return writeJsonResponse(w, http.StatusCreated, response)
}

/**
* DELETE: /api/users/{id}
 */
func (srv *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	log.add("user_id", id)
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	user, err := srv.Db.GetUserByID(uint(id))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	log.add("deleted_username", user.Username)
	if err := srv.deleteIdentityInKratos(r.Context(), user.KratosID); err != nil {
		log.add("deleted_kratos_id", user.KratosID)
		return newInternalServerServiceError(err, "error deleting user in kratos")
	}
	if err := srv.WithUserContext(r).DeleteUser(id); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusNoContent, "User deleted successfully")
}

/**
* PATCH: /api/users/{id}
* NOTE: Method is not configured to update (sync) role and username fields within kratos identity (Traits)
**/
func (srv *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	user := models.User{}
	err = json.NewDecoder(r.Body).Decode(&user)
	if err != nil {
		return newBadRequestServiceError(err, "invalid form data submited")
	}
	toUpdate, err := srv.Db.GetUserByID(uint(id))
	log.add("userId", id)
	if err != nil {
		log.error("Error getting user by ID:" + fmt.Sprintf("%d", id))
		return newDatabaseServiceError(err)
	}
	// Check if user is deactivated
	if toUpdate.DeactivatedAt != nil {
		return newBadRequestServiceError(errors.New("cannot update deactivated user"), "User is deactivated")
	}
	if !strings.EqualFold(toUpdate.Username, user.Username) && user.Username != "" {
		// usernames are immutable, but case-insensitively so (ID-849) — a
		// same-username-different-case submission isn't actually a change.
		return newBadRequestServiceError(errors.New("username cannot be updated"), "username")
	}
	// Casing is fixed at creation; even a same-identity resubmission must not
	// silently re-case the stored value via UpdateStruct below.
	user.Username = toUpdate.Username
	user.DocID = models.NilIfBlank(strings.TrimSpace(models.FormatNullableString(user.DocID)))
	if user.DocID == nil {
		user.DocID = toUpdate.DocID
	}

	if invalidDocID := validateResidentID(toUpdate.Role, models.FormatNullableString(user.DocID)); invalidDocID != "" {
		return newBadRequestServiceError(errors.New("invalid resident id"), invalidDocID)
	}
	invalidUser := validateUser(&user)
	if invalidUser != "" {
		return newBadRequestServiceError(errors.New("invalid user"), invalidUser)
	}
	models.UpdateStruct(toUpdate, &user)
	err = srv.WithUserContext(r).UpdateUser(toUpdate)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, toUpdate)
}

func (srv *Server) handleResetStudentPassword(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := r.Context().Value(ClaimsKey).(*Claims)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInternalServerServiceError(err, "id")
	}
	response := make(map[string]string)
	user, err := srv.Db.GetUserByID(uint(id))
	log.add("student.user_id", user.ID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	lockedStatus, err := srv.Db.IsAccountLocked(user.ID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if lockedStatus.IsLocked {
		log.infof("Resetting failed login attempts for user %d due to password reset", user.ID)
		err = srv.Db.ResetFailedLoginAttempts(user.ID)
		if err != nil {
			return newDatabaseServiceError(err)
		}
	}
	newPass, err := user.CreateTempPassword()
	if err != nil {
		return newInternalServerServiceError(err, "Error creating temporary password")
	}
	response["temp_password"] = newPass
	response["message"] = "Temporary password assigned"
	if user.KratosID == nil {
		err := srv.HandleCreateUserKratos(user.Username, newPass)
		if err != nil {
			return newInternalServerServiceError(err, "Error creating user in kratos")
		}
	} else {
		claims := claimsFromUser(user)
		claims.PasswordReset = true
		if err := srv.handleUpdatePasswordKratos(claims, newPass, true); err != nil {
			log.add("claims.user_id", claims.UserID)
			return newInternalServerServiceError(err, err.Error())
		}
	}
	resetPassword := models.NewUserAccountHistory(user.ID, models.ResetPassword, &claims.UserID, nil, nil)
	if err := srv.Db.InsertUserAccountHistoryAction(r.Context(), resetPassword); err != nil {
		return newCreateRequestServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, response)
}

func canResetUserPassword(currentUser *Claims, toUpdate models.UserRole) bool {
	switch toUpdate {
	case models.DepartmentAdmin: // department admin can only reset password of users in their department
		return currentUser.canSwitchFacility()
	case models.SystemAdmin: // system admin can only reset password of other system admins
		return currentUser.Role == toUpdate
	default: // user is garaunteed to be admin already at this point, can reset facility + student passwords
		return currentUser.isAdmin()
	}
}

func canDeleteUser(currentUser *Claims, toDelete models.UserRole) bool {
	switch toDelete {
	case models.DepartmentAdmin:
		return currentUser.canSwitchFacility()
	case models.SystemAdmin:
		return currentUser.Role == toDelete
	default:
		return currentUser.isAdmin()
	}
}

func stripNonAlphaChars(str string, keepCharacter func(char rune) bool) string {
	return strings.Map(func(r rune) rune {
		if keepCharacter(r) {
			return r
		}
		return -1
	}, str)
}

func isUsernameChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.'
}

func isNameChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsSpace(r) || r == '-' || r == '.'
}

// validateUser returns a message naming the offending field and the exact
// character that broke it, or "" when the user is valid. The message is shown
// to the user verbatim in a toast, so it has to say what to fix (ID-848).
func validateUser(user *models.User) string {
	fields := []struct {
		label     string
		value     string
		isAllowed func(rune) bool
		allowed   string
	}{
		{"Username", user.Username, isUsernameChar, "letters, numbers, and periods"},
		{"First name", user.NameFirst, isNameChar, "letters, spaces, hyphens, and periods"},
		{"Last name", user.NameLast, isNameChar, "letters, spaces, hyphens, and periods"},
	}
	for _, field := range fields {
		for _, char := range field.value {
			if !field.isAllowed(char) {
				return fmt.Sprintf("%s cannot contain %q, please use only %s", field.label, string(char), field.allowed)
			}
		}
	}
	return ""
}

// validateResidentID enforces that residents carry a resident ID (ID-835) so
// records can be correlated with OMS later; the value itself is never verified.
// Scoped to students because admins share models.User and legitimately have
// none. The role is passed in rather than read off the user because PATCH
// bodies omit it — callers must supply the stored role on update.
//
// This deliberately lives at the API boundary and not on the model or in
// db.CreateUser: the provider-sync imports create students with no source for
// an ID and must keep working.
func validateResidentID(role models.UserRole, docID string) string {
	if role != models.Student {
		return ""
	}
	if docID == "" {
		return "Resident ID is required"
	}
	if utf8.RuneCountInString(docID) > maxDocIDLen {
		return fmt.Sprintf("Resident ID must be %d characters or fewer", maxDocIDLen)
	}
	return ""
}

func (srv *Server) handleGetUserAccountHistory(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	args := srv.getQueryContext(r)
	categories := r.URL.Query()["categories"]
	history, err := srv.Db.GetUserAccountHistory(&args, uint(id), categories)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	var classHistoryIDs []uint
	for _, h := range history {
		if h.ClassHistoryID != nil {
			classHistoryIDs = append(classHistoryIDs, *h.ClassHistoryID)
		}
	}
	times, err := srv.programs.ClassHistoryCreatedAt(r.Context(), classHistoryIDs)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting user_account_history"))
	}
	// An action that logged a class change shows the change's time, as it did
	// when core joined program_classes_history itself.
	for i, h := range history {
		if h.ClassHistoryID == nil {
			continue
		}
		if t, ok := times[*h.ClassHistoryID]; ok {
			history[i].CreatedAt = &t
		}
	}
	return writePaginatedResponse(w, http.StatusOK, history, args.IntoMeta())
}

func (srv *Server) handleResidentVerification(w http.ResponseWriter, r *http.Request, log sLog) error {
	transferFacilityId, err := strconv.Atoi(r.URL.Query().Get("facility_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "facility ID")
	}
	log.add("trans_facility_id", transferFacilityId)
	userID, err := strconv.Atoi(r.URL.Query().Get("user_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	log.add("user_id", userID)
	docID := r.URL.Query().Get("doc_id")
	ctx := r.Context()
	user, err := srv.Db.GetUserByDocIDAndID(ctx, docID, userID)
	if err != nil {
		return writeJsonResponse(w, http.StatusNotFound, "Resident not found")
	}
	log.add("doc_id", docID)
	programNames, err := srv.programs.TransferConflicts(ctx, user.ID, uint(transferFacilityId))
	if err != nil {
		return writeJsonResponse(w, http.StatusNotFound, "Resident not found")
	}
	transferNotes := struct {
		User            models.User                                    `json:"user"`
		ProgramNames    []programsapi.ResidentTransferProgramConflicts `json:"program_names"`
		TransFacilityID int                                            `json:"trans_facility_id"`
	}{
		User:            *user,
		ProgramNames:    programNames,
		TransFacilityID: transferFacilityId,
	}
	return writeJsonResponse(w, http.StatusOK, transferNotes)
}

func (srv *Server) handleResidentTransfer(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	var transRequest struct {
		UserID          int `json:"user_id"`
		TransFacilityID int `json:"trans_facility_id"`
		CurrFacilityID  int `json:"curr_facility_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&transRequest); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	log.add("user_id", transRequest.UserID)
	log.add("admin_id", args.UserID)
	log.add("transfer_facility_id", transRequest.TransFacilityID)
	log.add("current_facility_id", transRequest.CurrFacilityID)

	user, err := srv.Db.GetUserByID(uint(transRequest.UserID))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if user.DeactivatedAt != nil {
		return newBadRequestServiceError(errors.New("cannot transfer deactivated user"), "User is deactivated")
	}
	tx, err := srv.Db.TransferResident(&args, transRequest.UserID, transRequest.CurrFacilityID, transRequest.TransFacilityID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	err = srv.updateFacilityInKratosIdentity(transRequest.UserID, transRequest.TransFacilityID)
	if err != nil {
		tx.Rollback()
		return newInternalServerServiceError(err, "error updating facility in kratos")
	}
	log.info("successfully transferred resident")
	transFacilityID := uint(transRequest.TransFacilityID)
	facilityTransfer := models.NewUserAccountHistory(uint(transRequest.UserID), models.FacilityTransfer, &args.UserID, nil, &transFacilityID)
	if err := srv.Db.InsertUserAccountHistoryAction(r.Context(), facilityTransfer); err != nil {
		tx.Rollback()
		return newCreateRequestServiceError(err)
	}
	if err := tx.Commit().Error; err != nil {
		// transfer back to original facility if we cannot commit tx
		err = srv.updateFacilityInKratosIdentity(transRequest.UserID, transRequest.CurrFacilityID)
		// pray that this never happens 🙏
		log.error("Error committing transaction AND updating user facility in kratos: " + err.Error())
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "successfully transferred resident")
}

func (srv *Server) handleGetUserNotes(w http.ResponseWriter, r *http.Request, log sLog) error {
	id := r.PathValue("id")
	userId, err := strconv.Atoi(id)
	if err != nil {
		return newInvalidIdServiceError(err, "error converting user_id")
	}
	notes, err := srv.Db.GetUserNotes(r.Context(), uint(userId))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, notes)
}

func (srv *Server) handleCreateUserNote(w http.ResponseWriter, r *http.Request, log sLog) error {
	id := r.PathValue("id")
	userId, err := strconv.Atoi(id)
	if err != nil {
		return newInvalidIdServiceError(err, "error converting user_id")
	}
	claims := r.Context().Value(ClaimsKey).(*Claims)
	var body struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return newBadRequestServiceError(err, "invalid request body")
	}
	if strings.TrimSpace(body.Note) == "" {
		return newBadRequestServiceError(errors.New("note is required"), "note cannot be empty")
	}
	note := &models.UserNote{
		UserID: uint(userId),
		Note:   strings.TrimSpace(body.Note),
	}
	note.CreateUserID = &claims.UserID
	if err := srv.Db.CreateUserNote(r.Context(), note); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusCreated, "Note added successfully")
}

func (srv *Server) handleBulkUpload(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := r.Context().Value(ClaimsKey).(*Claims)

	err := r.ParseMultipartForm(5 << 20) // 5mb file size limit for reference
	if err != nil {
		log.add("error", "failed to parse multipart form")
		return newBadRequestServiceError(err, "file too large or invalid format")
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		log.errorf("error closing file %v", err)
	}

	defer func() {
		if file.Close() != nil {
			log.errorf("Failed to close file %v", err)
		}
	}()

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".csv") {
		log.add("filename", header.Filename)
		return newBadRequestServiceError(errors.New("invalid file type"), "file type not supported - only .csv files can be uploaded")
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		log.add("error", "failed to read file")
		return newInternalServerServiceError(err, "failed to read file")
	}

	if len(fileBytes) == 0 {
		return newBadRequestServiceError(errors.New("empty file"), "file is empty - no data found")
	}

	log.add("filename", header.Filename)
	log.add("file_size", len(fileBytes))
	log.add("facility_id", claims.FacilityID)

	records, err := src.ParseCSVFile(fileBytes)
	if err != nil {
		log.add("parse_error", err.Error())
		return newBadRequestServiceError(err, err.Error())
	}

	if len(records) == 0 {
		return newBadRequestServiceError(errors.New("empty file"), "file is empty - no data found")
	}

	headers := records[0]
	headerMap, err := src.ValidateCSVHeaders(headers)
	if err != nil {
		log.add("validation_error", err.Error())
		return newBadRequestServiceError(err, err.Error())
	}

	var validRows []models.ValidatedUserRow
	var invalidRows []models.InvalidUserRow
	existingResidentIDs := make(map[string]int)
	existingUsernames := make(map[string]int)

	checkIdentity := func(username string, docID string) (bool, bool) {
		return srv.Db.UserIdentityExists(username, docID)
	}
	normalizeUsername := func(username string) string {
		return stripNonAlphaChars(username, isUsernameChar)
	}

	for i, record := range records[1:] {
		rowNum := i + 2

		validRow, invalidRow := src.ValidateUserRow(record, rowNum, headerMap, existingResidentIDs, checkIdentity, existingUsernames, normalizeUsername)
		if validRow != nil {
			validRows = append(validRows, *validRow)
		}
		if invalidRow != nil {
			invalidRows = append(invalidRows, *invalidRow)
		}
	}

	log.add("valid_count", len(validRows))
	log.add("error_count", len(invalidRows))
	log.info("CSV upload processed and validated")

	var errorCSVData []byte
	if len(invalidRows) > 0 {
		errorCSVData, err = src.GenerateErrorCSV(invalidRows)
		if err != nil {
			log.add("error", err.Error())
			return newInternalServerServiceError(err, "failed to generate error report")
		}
	}

	response := models.BulkUploadResponse{
		ValidCount:   len(validRows),
		ErrorCount:   len(invalidRows),
		ValidRows:    validRows,
		InvalidRows:  invalidRows,
		ErrorCSVData: errorCSVData,
	}

	return writeJsonResponse(w, http.StatusOK, response)
}

func (srv *Server) handleBulkCreate(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := r.Context().Value(ClaimsKey).(*Claims)

	var validRows []models.ValidatedUserRow

	if err := json.NewDecoder(r.Body).Decode(&validRows); err != nil {
		log.add("error", "failed to decode request body")
		return newBadRequestServiceError(err, "invalid request body")
	}

	if len(validRows) == 0 {
		return newBadRequestServiceError(errors.New("no valid rows"), "no valid rows provided for user creation")
	}

	facilityID, err := srv.requireFacilityID(r)
	if err != nil {
		return err
	}

	log.add("admin_id", claims.UserID)
	log.add("facility_id", facilityID)
	log.add("users_to_create", len(validRows))

	usersToCreate := make([]models.User, 0, len(validRows))
	for _, validRow := range validRows {
		user := models.User{
			Username:   stripNonAlphaChars(validRow.Username, isUsernameChar),
			NameFirst:  validRow.FirstName,
			NameLast:   validRow.LastName,
			DocID:      models.NilIfBlank(validRow.ResidentID),
			Role:       models.Student,
			FacilityID: facilityID,
		}
		usersToCreate = append(usersToCreate, user)
	}

	err = srv.Db.CreateUsersBulk(usersToCreate, claims.UserID)
	if err != nil {
		log.add("transaction_error", err.Error())
		log.error("Bulk user creation transaction failed")
		return newDatabaseServiceError(err)
	}

	log.add("created_users", len(usersToCreate))
	log.info("Bulk user creation transaction completed successfully")

	return writeJsonResponse(w, http.StatusOK, len(usersToCreate))
}

func (srv *Server) handleDeactivateUser(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}

	claims := r.Context().Value(ClaimsKey).(*Claims)
	err = srv.Db.DeactivateUser(r.Context(), uint(id), &claims.UserID)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	return writeJsonResponse(w, http.StatusOK, "User deactivated successfully")
}

func (srv *Server) handleGenerateUsageReportPDF(w http.ResponseWriter, r *http.Request, log sLog) error {
	userID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "error converting user_id")
	}

	subject, err := srv.Db.GetUserByID(uint(userID))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	subjectFeatures, err := srv.Db.GetFacilityFeatureAccess(subject.FacilityID, srv.features)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	hasProgramAccess := slices.Contains(subjectFeatures, models.ProgramAccess)

	pdfBytes, err := jasper.GenerateUsageReportPDF(r.Context(), srv.Db, srv.openContent, hasProgramAccess, userID)
	if err != nil {
		log.errorf("jasper service error: %v", err)
		return newInternalServerServiceError(err, "failed to generate PDF report")
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=transcript.pdf")

	_, err = w.Write(pdfBytes)
	return err
}

func (srv *Server) handleBulkResetPassword(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := r.Context().Value(ClaimsKey).(*Claims)
	var req struct {
		UserIDs []uint `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if len(req.UserIDs) == 0 {
		return newBadRequestServiceError(errors.New("no user IDs provided"), "user_ids is required")
	}
	var facID uint
	if !claims.canSwitchFacility() {
		facID = claims.FacilityID
	}
	users, err := srv.Db.GetUsersByIDs(r.Context(), req.UserIDs, facID, models.Student, models.FacilityAdmin, models.DepartmentAdmin)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	type successEntry struct {
		UserID       uint    `json:"user_id"`
		Username     string  `json:"username"`
		Name         string  `json:"name"`
		DocID        *string `json:"doc_id"`
		TempPassword string  `json:"temp_password"`
	}
	type failedEntry struct {
		UserID   uint   `json:"user_id"`
		Username string `json:"username"`
		Name     string `json:"name"`
		Reason   string `json:"reason"`
	}
	var successes []successEntry
	var failures []failedEntry
	for i := range users {
		user := &users[i]
		if !canResetUserPassword(claims, user.Role) {
			log.add("user_id", user.ID)
			log.warn("bulk reset: caller not authorized for target role")
			failures = append(failures, failedEntry{UserID: user.ID, Username: user.Username, Name: user.NameFirst + " " + user.NameLast, Reason: "not authorized to reset password for this user"})
			continue
		}
		lockedStatus, err := srv.Db.IsAccountLocked(user.ID)
		if err != nil {
			log.add("user_id", user.ID)
			log.error("bulk reset: error checking locked status")
			failures = append(failures, failedEntry{UserID: user.ID, Username: user.Username, Name: user.NameFirst + " " + user.NameLast, Reason: "error checking locked status"})
			continue
		}
		if lockedStatus.IsLocked {
			if err := srv.Db.ResetFailedLoginAttempts(user.ID); err != nil {
				log.add("user_id", user.ID)
				log.error("bulk reset: error resetting failed login attempts")
				failures = append(failures, failedEntry{UserID: user.ID, Username: user.Username, Name: user.NameFirst + " " + user.NameLast, Reason: "error resetting failed login attempts"})
				continue
			}
		}
		newPass, err := user.CreateTempPassword()
		if err != nil {
			log.add("user_id", user.ID)
			log.error("bulk reset: error creating temp password")
			failures = append(failures, failedEntry{UserID: user.ID, Username: user.Username, Name: user.NameFirst + " " + user.NameLast, Reason: "error creating temp password"})
			continue
		}
		if user.KratosID == nil {
			if err := srv.HandleCreateUserKratos(user.Username, newPass); err != nil {
				log.add("user_id", user.ID)
				log.error("bulk reset: error creating user in kratos")
				failures = append(failures, failedEntry{UserID: user.ID, Username: user.Username, Name: user.NameFirst + " " + user.NameLast, Reason: "error creating identity"})
				continue
			}
		} else {
			kratosC := claimsFromUser(user)
			kratosC.PasswordReset = true
			if err := srv.handleUpdatePasswordKratos(kratosC, newPass, true); err != nil {
				log.add("user_id", user.ID)
				log.error("bulk reset: error updating password in kratos")
				failures = append(failures, failedEntry{UserID: user.ID, Username: user.Username, Name: user.NameFirst + " " + user.NameLast, Reason: "error updating password"})
				continue
			}
		}
		resetPw := models.NewUserAccountHistory(user.ID, models.ResetPassword, &claims.UserID, nil, nil)
		if err := srv.Db.InsertUserAccountHistoryAction(r.Context(), resetPw); err != nil {
			log.add("user_id", user.ID)
			log.error("bulk reset: error inserting account history")
		}
		successes = append(successes, successEntry{
			UserID:       user.ID,
			Username:     user.Username,
			Name:         user.NameFirst + " " + user.NameLast,
			DocID:        user.DocID,
			TempPassword: newPass,
		})
	}
	return writeJsonResponse(w, http.StatusOK, map[string]any{
		"successes": successes,
		"failures":  failures,
	})
}

func (srv *Server) handleBulkDeactivateUsers(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := r.Context().Value(ClaimsKey).(*Claims)
	var req struct {
		UserIDs []uint `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if len(req.UserIDs) == 0 {
		return newBadRequestServiceError(errors.New("no user IDs provided"), "user_ids is required")
	}
	var facID uint
	if !claims.canSwitchFacility() {
		facID = claims.FacilityID
	}
	users, err := srv.Db.GetUsersByIDs(r.Context(), req.UserIDs, facID, models.Student)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	type failedEntry struct {
		UserID   uint   `json:"user_id"`
		Username string `json:"username"`
		Name     string `json:"name"`
		Reason   string `json:"reason"`
	}
	var successCount int
	var failures []failedEntry
	for _, user := range users {
		if err := srv.Db.DeactivateUser(r.Context(), user.ID, &claims.UserID); err != nil {
			log.add("user_id", user.ID)
			log.error("bulk deactivate: error deactivating user")
			failures = append(failures, failedEntry{UserID: user.ID, Username: user.Username, Name: user.NameFirst + " " + user.NameLast, Reason: "error deactivating user"})
			continue
		}
		successCount++
	}
	return writeJsonResponse(w, http.StatusOK, map[string]any{
		"success_count": successCount,
		"failed_count":  len(failures),
		"failures":      failures,
	})
}

type bulkUserFailure struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Reason   string `json:"reason"`
}

func reportMissingUserIDs(requested []uint, found []models.User) []bulkUserFailure {
	seen := make(map[uint]struct{}, len(found))
	for _, u := range found {
		seen[u.ID] = struct{}{}
	}
	var missing []bulkUserFailure
	for _, id := range requested {
		if _, ok := seen[id]; !ok {
			missing = append(missing, bulkUserFailure{
				UserID: id,
				Reason: "not found or out of scope",
			})
		}
	}
	return missing
}

func (srv *Server) handleBulkDeleteUsers(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := r.Context().Value(ClaimsKey).(*Claims)
	var req struct {
		UserIDs []uint `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if len(req.UserIDs) == 0 {
		return newBadRequestServiceError(errors.New("no user IDs provided"), "user_ids is required")
	}
	var facID uint
	if !claims.canSwitchFacility() {
		facID = claims.FacilityID
	}
	users, err := srv.Db.GetUsersByIDs(r.Context(), req.UserIDs, facID, models.Student, models.FacilityAdmin, models.DepartmentAdmin)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	var successCount int
	failures := reportMissingUserIDs(req.UserIDs, users)
	for _, user := range users {
		if !canDeleteUser(claims, user.Role) {
			log.add("user_id", user.ID)
			log.warn("bulk delete: caller not authorized for target role")
			failures = append(failures, bulkUserFailure{UserID: user.ID, Username: user.Username, Name: user.NameFirst + " " + user.NameLast, Reason: "not authorized to delete this user"})
			continue
		}
		if err := srv.deleteIdentityInKratos(r.Context(), user.KratosID); err != nil {
			log.add("user_id", user.ID)
			log.error("bulk delete: error deleting identity in kratos")
			failures = append(failures, bulkUserFailure{UserID: user.ID, Username: user.Username, Name: user.NameFirst + " " + user.NameLast, Reason: "error deleting identity"})
			continue
		}
		if err := srv.WithUserContext(r).DeleteUser(int(user.ID)); err != nil {
			log.add("user_id", user.ID)
			log.error("bulk delete: error deleting user")
			failures = append(failures, bulkUserFailure{UserID: user.ID, Username: user.Username, Name: user.NameFirst + " " + user.NameLast, Reason: "error deleting user"})
			continue
		}
		successCount++
	}
	return writeJsonResponse(w, http.StatusOK, map[string]any{
		"success_count": successCount,
		"failed_count":  len(failures),
		"failures":      failures,
	})
}
