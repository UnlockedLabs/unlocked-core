package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
)

func (srv *Server) registerFacilitiesRoutes() []routeDef {
	return []routeDef{
		newAdminRoute("GET /api/facilities", srv.handleIndexFacilities),
		newAdminRoute("GET /api/facilities/{id}", srv.handleShowFacility),
		newDeptAdminRoute("POST /api/facilities", srv.handleCreateFacility),
		newDeptAdminRoute("GET /api/facilities/{id}/delete-check", srv.handleGetFacilityDeleteCheck),
		newDeptAdminRoute("DELETE /api/facilities/{id}", srv.handleDeleteFacility),
		newDeptAdminRoute("PATCH /api/facilities/{id}", srv.handleUpdateFacility),
	}
}

/**
* GET: /api/facility/{id}
**/
func (srv *Server) handleIndexFacilities(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := srv.getQueryContext(r)
	facilities, err := srv.Db.GetAllFacilitiesWithStats(&args)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	ids := make([]uint, len(facilities))
	for i, f := range facilities {
		ids[i] = f.ID
	}
	content, err := srv.openContent.FacilityContent(r.Context(), ids)
	if err != nil {
		return newDatabaseServiceError(database.NewDBError(err, "error getting facilities"))
	}
	for i := range facilities {
		// Open Content's activity and favorites block a delete, as in
		// FacilityBlockingChildren.HasAny.
		if c := content[facilities[i].ID]; c.Activities > 0 || c.Favorites > 0 {
			facilities[i].CanDelete = false
		}
	}
	return writePaginatedResponse(w, http.StatusOK, facilities, args.IntoMeta())
}

/**
* GET: /api/facility/{id}
**/
func (srv *Server) handleShowFacility(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "facility ID")
	}
	log.add("facility_id", id)
	facility, err := srv.Db.GetFacilityByID(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	return writeJsonResponse(w, http.StatusOK, facility)
}

func (srv *Server) handleCreateFacility(w http.ResponseWriter, r *http.Request, log sLog) error {
	var facility models.Facility
	err := json.NewDecoder(r.Body).Decode(&facility)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	err = srv.WithUserContext(r).CreateFacility(&facility)
	if err != nil {
		log.add("facility_name", facility.Name)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusCreated, facility)
}

func (srv *Server) handleUpdateFacility(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "facility ID")
	}
	log.add("facility_id", id)
	var facility models.Facility
	err = json.NewDecoder(r.Body).Decode(&facility)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	err = srv.WithUserContext(r).UpdateFacility(&facility, uint(id))
	if err != nil {
		log.add("facilityName", facility.Name)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "facility updated successfully")
}

/**
* GET: /api/facilities/{id}/delete-check
* Preflight used by the UI to decide whether the delete control is enabled and,
* when it is not, what is blocking the delete.
 */
func (srv *Server) handleGetFacilityDeleteCheck(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "facility ID")
	}
	log.add("facility_id", id)
	blockers, err := srv.facilityBlockers(r.Context(), id)
	if err != nil {
		return err
	}
	payload := struct {
		CanDelete bool                            `json:"can_delete"`
		Blockers  models.FacilityBlockingChildren `json:"blockers"`
	}{
		CanDelete: !blockers.HasAny(),
		Blockers:  blockers,
	}
	return writeJsonResponse(w, http.StatusOK, payload)
}

/**
* DELETE: /api/facility/{id}
* A facility may only be deleted when it has no associated records. The guard is
* re-checked here (not only in the preflight) so a stale UI cannot force a delete.
 */
func (srv *Server) handleDeleteFacility(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "facility ID")
	}
	log.add("facilityId", id)
	blockers, err := srv.facilityBlockers(r.Context(), id)
	if err != nil {
		return err
	}
	if blockers.HasAny() {
		log.info("facility delete blocked by associated records")
		return writeFacilityDeleteConflictResponse(w, "cannot delete: facility has associated records", blockers)
	}
	if err = srv.WithUserContext(r).DeleteFacility(id); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusNoContent, "facility deleted successfully")
}

// facilityBlockers counts the rows that point at a facility: core's, and
// Open Content's from its api.
func (srv *Server) facilityBlockers(ctx context.Context, id int) (models.FacilityBlockingChildren, error) {
	blockers, err := srv.Db.FacilityBlockingChildren(id)
	if err != nil {
		return blockers, newDatabaseServiceError(err)
	}
	content, err := srv.openContent.FacilityContent(ctx, []uint{uint(id)})
	if err != nil {
		return blockers, newDatabaseServiceError(database.NewDBError(err, "error getting delete_guard_facility_counts"))
	}
	c := content[uint(id)]
	blockers.OpenContentActivity = c.Activities
	blockers.OpenContentFavorites = c.Favorites
	blockers.VisibilityStatuses = c.VisibilityStatuses
	return blockers, nil
}
