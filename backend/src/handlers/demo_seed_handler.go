package handlers

import (
	"net/http"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
)

func (srv *Server) registerDemoSeedRoutes() []routeDef {
	return []routeDef{
		validatedAdminRoute("POST /api/auth/demo-seed", srv.handleRunDemoSeed, func(_ *database.DB, r *http.Request) bool {
			return userIsSystemAdmin(r)
		}),
	}
}

func (srv *Server) handleRunDemoSeed(w http.ResponseWriter, r *http.Request, log sLog) error {
	log.info("running seeder for demo environment")
	err := srv.Db.RunOrResetDemoSeed(srv.getFacilityID(r), srv.openContent)
	if err != nil {
		return newInternalServerServiceError(err, "unable to run demo seed")
	}
	return writeJsonResponse(w, http.StatusOK, "demo seed ran successfully")
}
