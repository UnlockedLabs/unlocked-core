package providerplatforms

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/UnlockedLabs/unlocked-contracts/users"
	"github.com/sirupsen/logrus"
)

// Provider users and logins

// This function is used to take an existing canvas user that we receive from the middleware,
// in the request body, and a currently existing user's ID in the path, and create a mapping
// for that user, as well as create a login for that user in the provider
func (srv *server) handleMapProviderUser(w http.ResponseWriter, r *http.Request, log sLog) error {
	providerId, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "provider platform ID")
	}
	log.add("provider_platform_id", providerId)
	var userBody ImportUser
	err = json.NewDecoder(r.Body).Decode(&userBody)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	userId, err := strconv.Atoi(r.PathValue("user_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	log.add("user_id", userId)
	provider, err := srv.store.getProviderPlatformByID(providerId)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	user, err := srv.users.Get(r.Context(), uint(userId))
	if err != nil {
		return newDatabaseServiceError(newNotFoundDBError(err, "users"))
	}
	mapping := ProviderUserMapping{
		UserID:             user.ID,
		ProviderPlatformID: provider.ID,
		ExternalUsername:   userBody.ExternalUsername,
		ExternalUserID:     userBody.ExternalUserID,
	}
	err = srv.store.createProviderUserMapping(&mapping)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	srv.live.MappingsChanged(provider.ID, user.ID)
	// create login for user
	if provider.OidcID != 0 {
		if err := srv.registerProviderLogin(provider, user); err != nil {
			return newInternalServerServiceError(err, "Error creating provider login")
		}
	}
	return writeJsonResponse(w, http.StatusCreated, mapping)
}

type ImportUserResponse struct {
	Username     string `json:"username"`
	TempPassword string `json:"temp_password"`
	Error        string `json:"error"`
}

func stripNonAlphaChars(str string, keepCharacter func(char rune) bool) string {
	return strings.Map(func(r rune) rune {
		if keepCharacter(r) {
			return r
		}
		return -1
	}, str)
}

func isLetterOrNumber(char rune) bool {
	return unicode.IsLetter(char) || unicode.IsDigit(char)
}

func isLetterOrSpace(char rune) bool {
	return unicode.IsLetter(char) || unicode.IsSpace(char)
}

// This function takes an array of 1 or more Provider users (that come from the middleware, so they are
// already in the correct format) and creates a new user in the database for each of them, as well as
// creating a mapping for each user, and creating a login for each user in the provider
func (srv *server) handleImportProviderUsers(w http.ResponseWriter, r *http.Request, log sLog) error {
	facilityId, err := requireFacilityID(r)
	if err != nil {
		return err
	}
	providerId, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "provider platform ID")
	}
	log.add("facilityId", facilityId)
	log.add("providerId", providerId)
	provider, err := srv.store.getProviderPlatformByID(providerId)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	type ImportUserBody struct {
		Users []ImportUser `json:"users"`
	}
	var body ImportUserBody
	err = json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	toReturn := make([]ImportUserResponse, 0)
	for _, user := range body.Users {
		newUser := module.NewUser{
			Username:   stripNonAlphaChars(user.Username, isLetterOrNumber),
			Email:      user.Email,
			NameFirst:  stripNonAlphaChars(user.NameFirst, isLetterOrSpace),
			NameLast:   stripNonAlphaChars(user.NameLast, isLetterOrSpace),
			FacilityID: facilityId,
			Role:       users.Student,
		}
		userResponse := ImportUserResponse{
			Username: newUser.Username,
		}
		created, err := srv.users.Create(r.Context(), newUser)
		if err != nil {
			log.error("Error creating user in import-provider-users", err)
			userResponse.Error = "error creating user, likely a duplicate username"
			toReturn = append(toReturn, userResponse)
			continue
		}
		tempPw, err := srv.users.CreateLogin(r.Context(), created.ID)
		if err != nil {
			if delErr := srv.users.Delete(r.Context(), created.ID); delErr != nil {
				log.error("Error deleting user after failed provider user mapping import-provider-users")
			}
			log.warnf("Error creating user in kratos: %v, deleting the user for atomicity", err)
			userResponse.Error = "error creating authentication for user in kratos, please try again"
			toReturn = append(toReturn, userResponse)
			continue
		}
		userResponse.TempPassword = tempPw
		if kolibri, err := srv.store.findKolibriInstance(); err == nil {
			if err = srv.createUserInKolibri(created, kolibri); err != nil {
				log.error("Error creating kolibri user")
			}
		}
		mapping := ProviderUserMapping{
			UserID:             created.ID,
			ProviderPlatformID: provider.ID,
			ExternalUsername:   user.Username,
			ExternalUserID:     user.ExternalUserID,
		}
		if err = srv.store.createProviderUserMapping(&mapping); err != nil {
			if err = srv.users.Delete(r.Context(), created.ID); err != nil {
				log.error("Error deleting user after failed provider user mapping import-provider-users")
			}
			userResponse.Error = "user was created in database, but there was an error creating provider user mapping, please try again"
			toReturn = append(toReturn, userResponse)
			continue
		}
		if provider.OidcID != 0 {
			if err = srv.registerProviderLogin(provider, created); err != nil {
				log.error("error creating provider login, user has been deleted")
				userResponse.Error = "user was created in database, but there was an error creating provider login, please try again"
				toReturn = append(toReturn, userResponse)
				continue
			}
		}
		toReturn = append(toReturn, userResponse)
	}
	return writeJsonResponse(w, http.StatusOK, toReturn)
}

func (srv *server) handleCreateProviderUserAccount(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "provider platform ID")
	}
	log.add("provider_platform_id", id)
	user_id_int, err := strconv.Atoi(r.PathValue("user_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	log.add("user_id", user_id_int)
	provider, err := srv.store.getProviderPlatformByID(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	user, err := srv.users.Get(r.Context(), uint(user_id_int))
	if err != nil {
		return newDatabaseServiceError(newNotFoundDBError(err, "users"))
	}
	// if we aren't in a testing environment, register the user with Canvas or Kolibri
	if err = srv.createAndRegisterProviderUserAccount(provider, user); err != nil {
		return newInternalServerServiceError(err, err.Error())
	}
	return writeJsonResponse(w, http.StatusCreated, "User created successfully")
}

func (srv *server) handleGetMappedUsers(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "provider platform ID")
	}
	args := getQueryContext(r)
	users, err := srv.store.getMappedUsers(&args, id)
	if err != nil {
		log.add("providerId", id)
		return newDatabaseServiceError(err)
	}

	result := make([]MappedUserResponse, len(users))
	for i, u := range users {
		result[i] = MappedUserResponse{user: u}
	}

	// Enrich each entry with the external user's display name, fetched live from
	// the provider API. Failures are non-fatal — the frontend handles empty fields.
	if provider, pErr := srv.store.getProviderPlatformByID(id); pErr == nil {
		if externalUsers, uErr := srv.allProviderUsers(r.Context(), provider); uErr == nil {
			byExternalID := make(map[string]ImportUser, len(externalUsers))
			for _, eu := range externalUsers {
				byExternalID[eu.ExternalUserID] = eu
			}
			if extIDs, dbErr := srv.store.getMappedUsersExternalIDs(id); dbErr == nil {
				for i, u := range users {
					if extID, ok := extIDs[u.ID]; ok {
						if eu, ok := byExternalID[extID]; ok {
							result[i].CanvasNameFirst = eu.NameFirst
							result[i].CanvasNameLast = eu.NameLast
						}
					}
				}
			}
		}
	}

	return writePaginatedResponse(w, http.StatusOK, result, args.IntoMeta())
}

func (srv *server) handleGetMappingsForUser(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	mappings, err := srv.store.getAllProviderMappingsForUser(id)
	if err != nil {
		log.add("userId", id)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, mappings)
}

func (srv *server) handleCreateProviderUserMapping(w http.ResponseWriter, r *http.Request, log sLog) error {
	var mapping ProviderUserMapping
	err := json.NewDecoder(r.Body).Decode(&mapping)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	err = srv.store.createProviderUserMapping(&mapping)
	if err != nil {
		log.add("userId", mapping.UserID)
		return newDatabaseServiceError(err)
	}
	srv.live.MappingsChanged(mapping.ProviderPlatformID, mapping.UserID)
	return writeJsonResponse(w, http.StatusCreated, mapping)
}

func (srv *server) handleDeleteProviderUserMapping(w http.ResponseWriter, r *http.Request, log sLog) error {
	userId, err := strconv.Atoi(r.PathValue("userId"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	log.add("userId", userId)
	providerId, err := strconv.Atoi(r.PathValue("providerId"))
	if err != nil {
		return newInvalidIdServiceError(err, "provider ID")
	}
	err = srv.store.deleteProviderUserMappingByUserID(userId, providerId)
	if err != nil {

		log.add("providerId", providerId)
		return newDatabaseServiceError(err)
	}
	srv.live.MappingsChanged(uint(providerId), uint(userId))
	return writeJsonResponse(w, http.StatusNoContent, "Mapping deleted successfully")
}

// Actions: the worker's provider users and matching them to residents

func (srv *server) handleImportUsers(w http.ResponseWriter, r *http.Request, log sLog) error {
	log.add("file", "actions")
	service, err := srv.getService(r)
	if err != nil {
		return newBadRequestServiceError(err, err.Error())
	}
	providerUsers, err := service.GetUsers()
	if err != nil {
		return newInternalServerServiceError(err, err.Error())
	}
	provider, err := srv.store.getProviderPlatformByID(int(service.ProviderPlatformID))
	log.add("ProviderPlatformID", service.ProviderPlatformID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	for _, user := range providerUsers {
		// if this user was parsed improperly (happens randomly, unknown as to why), skip
		if user.Username == "" && user.Email == "" && user.NameLast == "" {
			log.add("error", err)
			log.debugf("received user with null values from provider: %d, skipping", provider.ID)
			continue
		}
		newUser := module.NewUser{
			Username:  user.Username,
			Email:     user.Email,
			NameFirst: user.NameFirst,
			NameLast:  user.NameLast,
		}
		created, err := srv.users.Create(r.Context(), newUser)
		if err != nil {
			log.error("Error creating user:" + err.Error())
			continue
		}
		if _, err := srv.users.CreateLogin(r.Context(), created.ID); err != nil {
			log.add("error", err.Error())
			log.errorf("Error creating user in kratos: %v", err)
			// FIXME: Error handling if we fail/handle atomicity
		}
		mapping := ProviderUserMapping{
			UserID:             created.ID,
			ProviderPlatformID: service.ProviderPlatformID,
			ExternalUsername:   user.Username,
			ExternalUserID:     user.ExternalUserID,
		}
		if err = srv.store.createProviderUserMapping(&mapping); err != nil {
			log.add("created.ID", created.ID)
			log.add("ExternalUserID", user.ExternalUserID)
			log.errorf("error creating a mapping between user %v and provider %d", user, provider.ID)
			return newDatabaseServiceError(err)
		}
		if provider.OidcID != 0 {
			// this should never be any other case, as it's triggered by the UI only (if oidc_id != 0). but we still check
			if err := srv.registerProviderLogin(provider, created); err != nil {
				log.error("Error registering provider login", err)
			}
		}
	}
	return writeJsonResponse(w, http.StatusOK, "Users imported successfully")
}

func paginateUsers(users []ImportUser, page, perPage int) (int, []ImportUser) {
	totalUsers := len(users)
	if totalUsers == 0 {
		return 0, []ImportUser{}
	}
	logrus.Debugf("total of %d uses found to import, returning %d", totalUsers, perPage)
	offset := (page - 1) * perPage
	if offset > totalUsers {
		return totalUsers, []ImportUser{}
	}
	end := offset + perPage
	if end > totalUsers {
		end = totalUsers
	}
	return totalUsers, users[offset:end]
}

func searchForUser(search string, users []ImportUser) []ImportUser {
	foundUsers := []ImportUser{}
	for _, user := range users {
		if strings.Contains(user.Username, search) || strings.Contains(user.NameFirst, search) || strings.Contains(user.NameLast, search) {
			foundUsers = append(foundUsers, user)
		}
	}
	return foundUsers
}

func (srv *server) handleGetUsers(w http.ResponseWriter, r *http.Request, log sLog) error {
	page, perPage := getPaginationInfo(r)
	service, err := srv.getService(r)
	if err != nil {
		return newBadRequestServiceError(err, err.Error())
	}

	search := r.URL.Query().Get("search")
	externalUsers, err := service.GetUsers()
	if err != nil {
		return newInternalServerServiceError(err, err.Error())
	}
	log.infof("Received import users: %v", externalUsers)
	if search != "" {
		externalUsers = searchForUser(search, externalUsers)
	}
	total, responseUsers := paginateUsers(externalUsers, page, perPage)

	meta := newPaginationInfo(page, perPage, int64(total))
	return writePaginatedResponse(w, http.StatusOK, responseUsers, meta)
}

func (srv *server) getService(r *http.Request) (*ProviderService, error) {
	providerId, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	provider, err := srv.store.getProviderPlatformByID(providerId)
	if err != nil {
		return nil, err
	}
	return GetProviderService(provider, srv.client)
}

func (srv *server) handleMatchUsers(w http.ResponseWriter, r *http.Request, log sLog) error {
	providerID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newBadRequestServiceError(err, "provider ID")
	}
	provider, err := srv.store.getProviderPlatformByID(providerID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	facilityID := getQueryContext(r).FacilityID

	providerUsers, err := srv.listProviderUsers(r.Context(), provider)
	if err != nil {
		return newInternalServerServiceError(err, "error fetching provider users")
	}

	unlockEdUsers, err := srv.store.getAllUnmappedUsers(providerID, facilityID)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	result := matchUsers(providerUsers, unlockEdUsers)
	return writeJsonResponse(w, http.StatusOK, result)
}

// allProviderUsers returns every user the provider knows about, mapped or not.
// listProviderUsers is the counterpart that returns only the unmapped ones.
func (srv *server) allProviderUsers(ctx context.Context, provider *ProviderPlatform) ([]ImportUser, error) {
	liveUsers, live, err := srv.live.Users(ctx, provider.ID)
	if err != nil {
		return nil, err
	}
	if live {
		return fromLive(liveUsers), nil
	}
	service, err := GetProviderService(provider, srv.client)
	if err != nil {
		return nil, err
	}
	return service.GetAllUsers()
}

// listProviderUsers reads the provider's unmapped users. Providers that can list
// their own users (Essential Ed) are read directly; the rest come from the
// worker (worker.go).
func (srv *server) listProviderUsers(ctx context.Context, provider *ProviderPlatform) ([]ImportUser, error) {
	liveUsers, live, err := srv.live.Users(ctx, provider.ID)
	if err != nil {
		return nil, err
	}
	if live {
		return srv.unmappedProviderUsers(provider.ID, fromLive(liveUsers))
	}
	service, err := GetProviderService(provider, srv.client)
	if err != nil {
		return nil, err
	}
	// The worker already filters out mapped users for these providers.
	return service.GetUsers()
}

// fromLive is the users core's live provider code listed.
func fromLive(liveUsers []module.ProviderUser) []ImportUser {
	out := make([]ImportUser, len(liveUsers))
	for i, u := range liveUsers {
		out[i] = ImportUser(u)
	}
	return out
}

// unmappedProviderUsers drops provider users that already have a
// ProviderUserMapping, so the matching screen only offers new ones.
func (srv *server) unmappedProviderUsers(providerID uint, users []ImportUser) ([]ImportUser, error) {
	mapped, err := srv.store.getMappedUsersExternalIDs(int(providerID))
	if err != nil {
		return nil, err
	}
	taken := make(map[string]struct{}, len(mapped))
	for _, externalID := range mapped {
		taken[externalID] = struct{}{}
	}
	unmapped := make([]ImportUser, 0, len(users))
	for _, user := range users {
		if _, ok := taken[user.ExternalUserID]; ok {
			continue
		}
		unmapped = append(unmapped, user)
	}
	return unmapped, nil
}

func (srv *server) handleApplyMatches(w http.ResponseWriter, r *http.Request, log sLog) error {
	var req ApplyMatchesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newBadRequestServiceError(err, "invalid request body")
	}

	service, err := srv.getService(r)
	if err != nil {
		return newBadRequestServiceError(err, err.Error())
	}

	provider, err := srv.store.getProviderPlatformByID(int(service.ProviderPlatformID))
	if err != nil {
		return newDatabaseServiceError(err)
	}

	claims := claimsFrom(r)
	var failed []string
	applied := 0

	for _, c := range req.Confirmed {
		user, err := srv.users.Get(r.Context(), c.UnlockEdUserID)
		if err != nil || (!claims.CanSwitchFacility() && user.FacilityID != getQueryContext(r).FacilityID) {
			failed = append(failed, c.CanvasUser.Username)
			continue
		}
		existing, _ := srv.store.getProviderUserMapping(int(c.UnlockEdUserID), int(service.ProviderPlatformID))
		if existing != nil {
			applied++
			continue
		}
		mapping := ProviderUserMapping{
			UserID:             c.UnlockEdUserID,
			ProviderPlatformID: service.ProviderPlatformID,
			ExternalUsername:   c.CanvasUser.ExternalUsername,
			ExternalUserID:     c.CanvasUser.ExternalUserID,
		}
		if err := srv.store.createProviderUserMapping(&mapping); err != nil {
			failed = append(failed, c.CanvasUser.Username)
			continue
		}
		srv.live.MappingsChanged(service.ProviderPlatformID, c.UnlockEdUserID)
		if provider.OidcID != 0 {
			if err := srv.registerProviderLogin(provider, user); err != nil {
				log.errorf("error registering provider login for user %d: %v", c.UnlockEdUserID, err)
			}
		}
		applied++
	}

	created := 0
	if len(req.ToCreate) > 0 && getQueryContext(r).FacilityID == 0 {
		return newBadRequestServiceError(nil, "facility_id is required to create users")
	}
	facilityID := getQueryContext(r).FacilityID
	for _, cu := range req.ToCreate {
		if strings.TrimSpace(cu.Username) == "" && strings.TrimSpace(cu.Email) == "" && strings.TrimSpace(cu.NameLast) == "" {
			continue
		}
		newUser := module.NewUser{
			Username:   stripNonAlphaChars(cu.Username, isLetterOrNumber),
			Email:      cu.Email,
			NameFirst:  stripNonAlphaChars(cu.NameFirst, isLetterOrSpace),
			NameLast:   stripNonAlphaChars(cu.NameLast, isLetterOrSpace),
			FacilityID: facilityID,
			Role:       users.Student,
		}
		user, err := srv.users.Create(r.Context(), newUser)
		if err != nil {
			log.errorf("error creating user %s in apply-matches: %v", cu.Username, err)
			failed = append(failed, cu.Username)
			continue
		}
		if _, err := srv.users.CreateLogin(r.Context(), user.ID); err != nil {
			log.errorf("error creating kratos user for %s: %v", cu.Username, err)
		}
		mapping := ProviderUserMapping{
			UserID:             user.ID,
			ProviderPlatformID: service.ProviderPlatformID,
			ExternalUsername:   cu.ExternalUsername,
			ExternalUserID:     cu.ExternalUserID,
		}
		if err := srv.store.createProviderUserMapping(&mapping); err != nil {
			failed = append(failed, cu.Username)
			continue
		}
		srv.live.MappingsChanged(service.ProviderPlatformID, user.ID)
		if provider.OidcID != 0 {
			if err := srv.registerProviderLogin(provider, user); err != nil {
				log.errorf("error registering provider login for %s: %v", cu.Username, err)
			}
		}
		created++
	}

	if failed == nil {
		failed = []string{}
	}
	return writeJsonResponse(w, http.StatusOK, ApplyMatchesResponse{
		Applied: applied,
		Created: created,
		Failed:  failed,
	})
}
