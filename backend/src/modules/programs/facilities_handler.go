package programs

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func (srv *server) handleGetRooms(w http.ResponseWriter, r *http.Request, log sLog) error {
	facilityID := facilityScopedQueryContext(r).FacilityID
	log.add("facility_id", facilityID)
	rooms, err := srv.store.GetRoomsForFacility(facilityID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, rooms)
}

func (srv *server) handleCreateRoom(w http.ResponseWriter, r *http.Request, log sLog) error {
	facilityID, err := requireFacilityID(r)
	if err != nil {
		return err
	}
	var room Room
	if err := json.NewDecoder(r.Body).Decode(&room); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	room.FacilityID = facilityID
	log.add("facility_id", facilityID)
	log.add("room_name", room.Name)
	created, err := srv.store.CreateRoom(&room)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusCreated, created)
}

/**
* GET: /api/facilities/{facilityId}/instructors
 */
func (srv *server) handleGetFacilityInstructors(w http.ResponseWriter, r *http.Request, log sLog) error {
	facilityId, err := strconv.Atoi(r.PathValue("facilityId"))
	if err != nil {
		return newInvalidIdServiceError(err, "facility ID")
	}

	instructors, err := srv.store.GetFacilityInstructors(facilityId)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	return writeJsonResponse(w, http.StatusOK, instructors)
}
