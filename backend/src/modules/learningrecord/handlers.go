package learningrecord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
)

func (srv *server) routes() []module.Route {
	lr := []features.Access{features.LearningRecord}
	return []module.Route{
		{Pattern: "GET /api/learning-record/facilities", Handler: handle(srv.handleIndexFacilities), Access: module.AnyUser, Features: lr},
		{Pattern: "GET /api/learning-record/entries", Handler: handle(srv.handleIndexEntries), Access: module.AnyUser, Features: lr},
		{Pattern: "POST /api/learning-record/entries", Handler: handle(srv.handleCreateEntry), Access: module.AnyUser, Features: lr},
		{Pattern: "PUT /api/learning-record/entries/{id}", Handler: handle(srv.handleUpdateEntry), Access: module.AnyUser, Features: lr},
		{Pattern: "DELETE /api/learning-record/entries/{id}", Handler: handle(srv.handleDeleteEntry), Access: module.AnyUser, Features: lr},
		{Pattern: "GET /api/learning-record/draft", Handler: handle(srv.handleGetDraft), Access: module.AnyUser, Features: lr},
		{Pattern: "PUT /api/learning-record/draft", Handler: handle(srv.handleUpsertDraft), Access: module.AnyUser, Features: lr},
		{Pattern: "DELETE /api/learning-record/draft", Handler: handle(srv.handleDeleteDraft), Access: module.AnyUser, Features: lr},
	}
}

type learningRecordFacility struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// maxFacilityOtherRunes caps the free-text location. It prints as a single
// line on the transcript, and the column itself is unbounded text.
const maxFacilityOtherRunes = 120

/*
validateLocation normalizes and checks the achievement location on an
incoming entry or draft. The transcript treats facility_id and facility_other
as mutually exclusive alternatives, so that invariant is enforced here instead
of trusting the client, an unknown facility_id gets a 400 rather than a
foreign key error, and facility_name is replaced with the name on record so a
client cannot label a facility as something it is not.
*/
func (srv *server) validateLocation(entry *LearningRecordEntry) error {
	// Whitespace-only text is the same as no text: the resident may have opened
	// the "Other" field and typed nothing meaningful into it yet.
	entry.FacilityOther = strings.TrimSpace(entry.FacilityOther)
	entry.FacilityName = ""
	if utf8.RuneCountInString(entry.FacilityOther) > maxFacilityOtherRunes {
		return newBadRequestServiceError(nil,
			fmt.Sprintf("location must be %d characters or fewer", maxFacilityOtherRunes))
	}
	if entry.FacilityID == nil {
		return nil
	}
	if entry.FacilityOther != "" {
		return newBadRequestServiceError(nil, "location must be either a facility or free text, not both")
	}
	name, ok, err := srv.store.GetLocationFacility(*entry.FacilityID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if !ok {
		return newBadRequestServiceError(nil, "facility_id does not match a known facility")
	}
	entry.FacilityName = name
	return nil
}

// The main facilities routes are admin-only, so residents read the list
// through here, gated on the learning_record feature.
func (srv *server) handleIndexFacilities(w http.ResponseWriter, r *http.Request, log sLog) error {
	facilities, err := srv.store.GetAllFacilitiesOrdered()
	if err != nil {
		return newDatabaseServiceError(err)
	}
	results := make([]learningRecordFacility, 0, len(facilities))
	for _, f := range facilities {
		results = append(results, learningRecordFacility{ID: f.ID, Name: f.Name})
	}
	return writeJsonResponse(w, http.StatusOK, results)
}

func (srv *server) handleIndexEntries(w http.ResponseWriter, r *http.Request, log sLog) error {
	userID := claimsFrom(r).UserID
	entries, err := srv.store.GetEntries(userID)
	if err != nil {
		log.add("user_id", userID)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, entries)
}

func (srv *server) handleCreateEntry(w http.ResponseWriter, r *http.Request, log sLog) error {
	var entry LearningRecordEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	entry.UserID = claimsFrom(r).UserID
	if err := srv.validateLocation(&entry); err != nil {
		log.add("user_id", entry.UserID)
		return err
	}
	if err := srv.WithUserContext(r).CreateEntry(&entry); err != nil {
		log.add("user_id", entry.UserID)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusCreated, entry)
}

func (srv *server) handleUpdateEntry(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "entry ID")
	}
	var entry LearningRecordEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	entry.ID = uint(id)
	entry.UserID = claimsFrom(r).UserID
	if err := srv.validateLocation(&entry); err != nil {
		log.add("entry_id", id)
		log.add("user_id", entry.UserID)
		return err
	}
	if err := srv.WithUserContext(r).UpdateEntry(&entry); err != nil {
		log.add("entry_id", id)
		log.add("user_id", entry.UserID)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, entry)
}

func (srv *server) handleDeleteEntry(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "entry ID")
	}
	userID := claimsFrom(r).UserID
	if err := srv.WithUserContext(r).DeleteEntry(uint(id), userID); err != nil {
		log.add("entry_id", id)
		log.add("user_id", userID)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Entry deleted successfully")
}

func (srv *server) handleGetDraft(w http.ResponseWriter, r *http.Request, log sLog) error {
	userID := claimsFrom(r).UserID
	draft, err := srv.store.GetDraft(userID)
	if err != nil {
		log.add("user_id", userID)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, draft)
}

func (srv *server) handleUpsertDraft(w http.ResponseWriter, r *http.Request, log sLog) error {
	var draft LearningRecordEntry
	if err := json.NewDecoder(r.Body).Decode(&draft); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	draft.UserID = claimsFrom(r).UserID
	if err := srv.validateLocation(&draft); err != nil {
		log.add("user_id", draft.UserID)
		log.add("client_id", draft.ClientID)
		return err
	}
	if err := srv.WithUserContext(r).UpsertDraft(&draft); err != nil {
		log.add("user_id", draft.UserID)
		log.add("client_id", draft.ClientID)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, draft)
}

func (srv *server) handleDeleteDraft(w http.ResponseWriter, r *http.Request, log sLog) error {
	userID := claimsFrom(r).UserID
	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		return newBadRequestServiceError(nil, "client_id query parameter is required")
	}
	if err := srv.WithUserContext(r).DeleteDraft(userID, clientID); err != nil {
		log.add("user_id", userID)
		log.add("client_id", clientID)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Draft deleted successfully")
}
