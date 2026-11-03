package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
)

func (srv *Server) registerFacilityFeatureAccessRoutes() []routeDef {
	return []routeDef{
		newDeptAdminRoute("GET /api/features", srv.handleGetFeatureCatalog),
		newDeptAdminRoute("GET /api/facilities/features", srv.handleGetFacilityFeatureOverview),
		newDeptAdminRoute("PUT /api/facilities/features/apply-all", srv.handleApplyFacilityFeaturesToAll),
		newDeptAdminRoute("GET /api/facilities/{id}/features", srv.handleGetFacilityFeatureDetail),
		newDeptAdminRoute("PUT /api/facilities/{id}/features/{feature}", srv.handleSetFacilityFeature),
	}
}

/**
* GET: /api/features
* Every feature Feature Control shows, top-level ones first: core's own and any
* a product registered. A sub-feature carries its parent's name.
**/
func (srv *Server) handleGetFeatureCatalog(w http.ResponseWriter, r *http.Request, log sLog) error {
	catalog, err := srv.Db.FeatureCatalog()
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, catalog)
}

/**
* GET: /api/facilities/features
* List-panel data: every facility with the effective on/off state of each
* top-level feature, optionally filtered by name search and by one feature's
* on/off state.
**/
func (srv *Server) handleGetFacilityFeatureOverview(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)

	var filterFeature *models.FeatureAccess
	var filterEnabled *bool
	if raw := r.URL.Query().Get("feature"); raw != "" {
		feature := models.FeatureAccess(raw)
		catalog, err := srv.Db.FeatureCatalog()
		if err != nil {
			return newDatabaseServiceError(err)
		}
		if _, ok := catalog.Find(feature); !ok {
			return newBadRequestServiceError(errors.New("invalid feature"), "invalid feature filter")
		}
		filterFeature = &feature
		log.add("feature", feature)
		if enabledRaw := r.URL.Query().Get("enabled"); enabledRaw != "" {
			enabled, err := strconv.ParseBool(enabledRaw)
			if err != nil {
				return newBadRequestServiceError(err, "invalid enabled filter")
			}
			filterEnabled = &enabled
			log.add("enabled", enabled)
		}
	}

	rows, err := srv.Db.GetFacilityFeatureOverview(&args, filterFeature, filterEnabled)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, rows)
}

/**
* GET: /api/facilities/{id}/features
* Detail-panel data: every feature (top-level and sub) and its effective state
* for one facility.
**/
func (srv *Server) handleGetFacilityFeatureDetail(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "facility ID")
	}
	log.add("facility_id", id)
	rows, err := srv.Db.GetFacilityFeatureDetail(uint(id))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, rows)
}

// Enabled is a pointer so an omitted property is distinguishable from an
// explicit `false` — otherwise a `{}` body would silently disable the feature.
type setFacilityFeatureRequest struct {
	Enabled *bool `json:"enabled"`
}

/**
* PUT: /api/facilities/{id}/features/{feature}
**/
func (srv *Server) handleSetFacilityFeature(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "facility ID")
	}
	feature := models.FeatureAccess(r.PathValue("feature"))
	log.add("facility_id", id)
	log.add("feature", feature)

	var req setFacilityFeatureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if req.Enabled == nil {
		return newBadRequestServiceError(errors.New("enabled required"), "enabled required")
	}

	args := srv.getQueryContext(r)
	if err := srv.Db.UpsertFacilityFeatureFlag(&args, uint(id), feature, *req.Enabled); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "facility feature updated successfully")
}

type applyFacilityFeaturesRequest struct {
	SourceFacilityID uint `json:"source_facility_id"`
}

/**
* PUT: /api/facilities/features/apply-all
**/
func (srv *Server) handleApplyFacilityFeaturesToAll(w http.ResponseWriter, r *http.Request, log sLog) error {
	var req applyFacilityFeaturesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if req.SourceFacilityID == 0 {
		return newBadRequestServiceError(errors.New("source_facility_id required"), "source_facility_id required")
	}
	log.add("source_facility_id", req.SourceFacilityID)

	args := srv.getQueryContext(r)
	if err := srv.Db.ApplyFacilityFeaturesToAll(&args, req.SourceFacilityID); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "settings applied to all facilities successfully")
}
