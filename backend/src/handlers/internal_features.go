package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"

	"github.com/UnlockedLabs/unlocked-contracts/coreapi"
)

// registerInternalRoutes registers routes that only other services call. They
// sit outside /api, which is all the ingress sends to the server, so they can
// only be reached from inside the cluster. Callers are services, not users, so
// there is no session to check.
func (srv *Server) registerInternalRoutes() {
	srv.Mux.Handle("GET /internal/v1/features", srv.handleError(srv.handleInternalFeatures))
	srv.Mux.Handle("GET /internal/v1/claims", srv.handleError(srv.handleInternalClaims))
	srv.Mux.Handle("PUT /internal/v1/features/{name}", srv.handleError(srv.handleInternalRegisterFeature))
	srv.Mux.Handle("/internal/v1/core/", coreapi.NewHandler(coreUsers{srv}, liveProviders{srv}, registry.ProviderPlatformsToken()))
}

type internalFeaturesResponse struct {
	FacilityID uint                   `json:"facility_id"`
	Features   []models.FeatureAccess `json:"features"`
}

/**
* GET: /internal/v1/features?facility_id=N
* The features that are on at one facility, after its overrides and the
* sub-feature rule: the same answer Feature Control shows. Called by
* unlocked-contracts' features.Checker (EN-188, EN-189).
**/
func (srv *Server) handleInternalFeatures(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.URL.Query().Get("facility_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "facility ID")
	}
	log.add("facility_id", id)
	// Without this, an unknown id would return the statewide defaults, which
	// looks like a real answer.
	if _, err := srv.Db.GetFacilityByID(id); err != nil {
		return newDatabaseServiceError(err)
	}
	on, err := srv.Db.GetFacilityFeatureAccess(uint(id))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, internalFeaturesResponse{FacilityID: uint(id), Features: on})
}

// featureName matches feature_flags' name check (migration 00079).
var featureName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

const (
	maxFeatureTitle       = 100
	maxFeatureDescription = 500
)

type registerFeatureRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

/**
* PUT: /internal/v1/features/{name}
* A product registers its feature on startup (EN-257). A new feature starts
* off at every facility until an admin turns it on in Feature Control.
* Registering again only updates the title and description, so it is safe to
* repeat. 201 when the feature is new, 200 when it already existed. Core's own
* features can't be registered, so no product can rename or blank them. Called
* by unlocked-contracts' features.Register.
**/
func (srv *Server) handleInternalRegisterFeature(w http.ResponseWriter, r *http.Request, log sLog) error {
	name := models.FeatureAccess(r.PathValue("name"))
	log.add("feature", name)
	if !featureName.MatchString(string(name)) {
		return newBadRequestServiceError(errors.New("invalid feature name"), "feature name must start with a letter and use only lowercase letters, digits and underscores, up to 64 characters")
	}
	if slices.Contains(features.All, name) {
		return NewServiceError(errors.New("core feature name"), http.StatusConflict, "a core feature can't be registered")
	}
	var req registerFeatureRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	// Postgres text can't hold a NUL character.
	if strings.ContainsRune(req.Title, 0) || strings.ContainsRune(req.Description, 0) {
		return newBadRequestServiceError(errors.New("NUL character"), "title and description can't contain a NUL character")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" || utf8.RuneCountInString(title) > maxFeatureTitle {
		return newBadRequestServiceError(errors.New("invalid title"), "title is required, up to 100 characters")
	}
	var description *string
	if d := strings.TrimSpace(req.Description); d != "" {
		if utf8.RuneCountInString(d) > maxFeatureDescription {
			return newBadRequestServiceError(errors.New("invalid description"), "description is up to 500 characters")
		}
		description = &d
	}

	catalog, err := srv.Db.FeatureCatalog()
	if err != nil {
		return newDatabaseServiceError(err)
	}
	_, exists := catalog.Find(name)
	if err := srv.Db.UpsertFeature(name, title, description); err != nil {
		return newDatabaseServiceError(err)
	}
	// Two pods registering a new feature at once may both get 201; the row
	// ends up the same either way.
	status := http.StatusOK
	if !exists {
		status = http.StatusCreated
	}
	return writeJsonResponse(w, status, "feature registered")
}
