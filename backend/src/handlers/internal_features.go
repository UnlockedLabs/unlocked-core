package handlers

import (
	"net/http"
	"strconv"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
)

// registerInternalRoutes registers routes that only other services call. They
// sit outside /api, which is all the ingress sends to the server, so they can
// only be reached from inside the cluster. Callers are services, not users, so
// there is no session to check.
func (srv *Server) registerInternalRoutes() {
	srv.Mux.Handle("GET /internal/v1/features", srv.handleError(srv.handleInternalFeatures))
	srv.Mux.Handle("GET /internal/v1/claims", srv.handleError(srv.handleInternalClaims))
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
	features, err := srv.Db.GetFacilityFeatureAccess(uint(id), srv.features)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, internalFeaturesResponse{FacilityID: uint(id), Features: features})
}
