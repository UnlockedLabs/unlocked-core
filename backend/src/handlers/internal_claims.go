package handlers

import (
	"errors"
	"net/http"

	"gorm.io/gorm"
)

/**
* GET: /internal/v1/claims?kratos_id=ID
* The identity.Claims of the user with this Kratos identity, as core builds
* them for its own requests, without the session's id (the caller has it from
* Kratos). 404 for an unknown user, 403 for a deactivated one. Called by
* unlocked-contracts' identity.SessionChecker (EN-256).
**/
func (srv *Server) handleInternalClaims(w http.ResponseWriter, r *http.Request, log sLog) error {
	kratosID := r.URL.Query().Get("kratos_id")
	if kratosID == "" {
		return newInvalidQueryParamServiceError(errors.New("kratos_id is required"), "kratos_id")
	}
	log.add("kratos_id", kratosID)
	claims, err := srv.claimsForKratosID(kratosID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return NewServiceError(err, http.StatusNotFound, "user not found")
	case errors.Is(err, errUserDeactivated):
		return NewServiceError(err, http.StatusForbidden, "account deactivated")
	case err != nil:
		return newInternalServerServiceError(err, "error getting user")
	}
	return writeJsonResponse(w, http.StatusOK, claims.identityClaims())
}
