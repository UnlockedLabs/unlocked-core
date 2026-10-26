package providerplatforms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"golang.org/x/oauth2"
)

// routes is every Provider Platforms route, with the access level and
// features it had in core (EN-193).
func (srv *server) routes() []module.Route {
	pp := []features.Access{features.ProviderPlatforms}
	return []module.Route{
		// Provider platforms
		{Pattern: "GET /api/provider-platforms", Handler: handle(srv.handleIndexProviders), Access: module.Admin, Features: pp},
		{Pattern: "GET /api/provider-platforms/{id}", Handler: handle(srv.handleShowProvider), Access: module.Admin, Features: pp},
		{Pattern: "POST /api/provider-platforms", Handler: handle(srv.handleCreateProvider), Access: module.Admin, Features: pp},
		{Pattern: "GET /api/provider-platforms/callback", Handler: handle(srv.handleOAuthProviderCallback), Access: module.Admin, Features: pp},
		{Pattern: "GET /api/provider-platforms/{id}/refresh", Handler: handle(srv.handleOAuthRefreshToken), Access: module.Admin, Features: pp},
		{Pattern: "PATCH /api/provider-platforms/{id}", Handler: handle(srv.handleUpdateProvider), Access: module.Admin, Features: pp},
		{Pattern: "DELETE /api/provider-platforms/{id}", Handler: handle(srv.handleDeleteProvider), Access: module.Admin, Features: pp},

		// Provider users and logins
		{Pattern: "POST /api/provider-platforms/{id}/map-user/{user_id}", Handler: handle(srv.handleMapProviderUser), Access: module.Admin, UserParam: "user_id", Features: pp},
		{Pattern: "POST /api/provider-platforms/{id}/users/import", Handler: handle(srv.handleImportProviderUsers), Access: module.Admin, Features: pp},
		{Pattern: "POST /api/provider-platforms/{id}/create-user/{user_id}", Handler: handle(srv.handleCreateProviderUserAccount), Access: module.Admin, Features: pp},
		{Pattern: "GET /api/users/{id}/logins", Handler: handle(srv.handleGetMappingsForUser), Access: module.Admin, Features: pp},
		{Pattern: "POST /api/users/{id}/logins", Handler: handle(srv.handleCreateProviderUserMapping), Access: module.Admin, Features: pp},
		{Pattern: "POST /api/provider-platforms/{id}/user-accounts/{user_id}", Handler: handle(srv.handleCreateProviderUserAccount), Access: module.Admin, Features: pp},
		{Pattern: "DELETE /api/users/{userId}/logins/{providerId}", Handler: handle(srv.handleDeleteProviderUserMapping), Access: module.Admin, Features: pp},
		{Pattern: "GET /api/provider-platforms/{id}/mapped-users", Handler: handle(srv.handleGetMappedUsers), Access: module.Admin, Features: pp},

		// Actions: the worker's provider users and matching them to residents
		{Pattern: "GET /api/actions/provider-platforms/{id}/get-users", Handler: handle(srv.handleGetUsers), Access: module.Admin, Features: pp},
		{Pattern: "POST /api/actions/provider-platforms/{id}/import-users", Handler: handle(srv.handleImportUsers), Access: module.Admin, Features: pp},
		{Pattern: "GET /api/actions/provider-platforms/{id}/match-users", Handler: handle(srv.handleMatchUsers), Access: module.Admin, Features: pp},
		{Pattern: "POST /api/actions/provider-platforms/{id}/apply-matches", Handler: handle(srv.handleApplyMatches), Access: module.Admin, Features: pp},

		// OIDC clients
		{Pattern: "GET /api/oidc/clients", Handler: handle(srv.handleGetAllClients), Access: module.Admin, Features: pp},
		{Pattern: "POST /api/oidc/clients", Handler: handle(srv.handleRegisterClient), Access: module.Admin, Features: pp},
		{Pattern: "GET /api/oidc/clients/{id}", Handler: handle(srv.handleGetOidcClient), Access: module.Admin, Features: pp},
		{Pattern: "POST /api/oidc/clients/{id}/recreate", Handler: handle(srv.handleRecreateClient), Access: module.Admin, Features: pp},

		// Courses, outcomes and activity
		{Pattern: "GET /api/courses", Handler: handle(srv.handleIndexCourses), Access: module.Admin, Features: pp},
		{Pattern: "GET /api/courses/{id}", Handler: handle(srv.handleShowCourse), Features: pp},
		{Pattern: "GET /api/courses/{id}/activity", Handler: handle(srv.handleGetCourseActivity), Access: module.Admin, Features: pp},
		{Pattern: "GET /api/users/{id}/outcomes", Handler: handle(srv.handleGetOutcomes), Features: pp},
		{Pattern: "GET /api/users/{id}/daily-activity", Handler: handle(srv.handleGetDailyActivityByUserID), UserParam: "id", Features: pp},
		{Pattern: "GET /api/users/{id}/catalog", Handler: handle(srv.handleUserCatalog), UserParam: "id", Features: pp},
		{Pattern: "GET /api/users/{id}/courses", Handler: handle(srv.handleUserCourses), UserParam: "id", Features: pp},
	}
}

// Provider platforms

func (srv *server) handleIndexProviders(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := getQueryContext(r)
	platforms, err := srv.store.getAllProviderPlatforms(&args)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	only := r.URL.Query().Get("only")
	if only == "oidc_enabled" {
		// this is for offering user creation in enabled providers
		platforms = slices.DeleteFunc(platforms, func(platform ProviderPlatform) bool {
			// don't return kolibri, as users are automatically created in kolibri
			return platform.OidcID == 0 || platform.Type == Kolibri
		})
	}
	slices.SortFunc(platforms, func(i, j ProviderPlatform) int {
		if i.State == Enabled && j.State != Enabled {
			return -1
		} else if i.State != Enabled && j.State == Enabled {
			return 1
		} else if i.State == Archived && j.State != Archived {
			return 1
		} else if i.State != Archived && j.State == Archived {
			return -1
		} else {
			return 0
		}
	})

	log.info("Found "+strconv.Itoa(int(args.Total)), " provider platforms")
	return writePaginatedResponse(w, http.StatusOK, platforms, args.IntoMeta())
}

func (srv *server) handleShowProvider(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "provider platform ID")
	}
	log.add("providerPlatformID", id)
	platform, err := srv.store.getProviderPlatformByID(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, *platform)
}

func (srv *server) handleCreateProvider(w http.ResponseWriter, r *http.Request, log sLog) error {
	var platform ProviderPlatform
	err := json.NewDecoder(r.Body).Decode(&platform)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	// The access key travels on every request to this base URL, so a plaintext
	// one would put it on the wire. Rejected here rather than in a model hook so
	// the admin gets a message they can act on.
	if err := requireHTTPS(platform.BaseUrl); err != nil {
		return newBadRequestServiceError(err, "provider base URL must use https")
	}
	if platform.Type == Brightspace {
		oauthURL, err := srv.getOAuthUrl(&platform)
		if err != nil {
			return err
		}
		return writeJsonResponse(w, http.StatusCreated, map[string]interface{}{
			"platform":  platform,
			"oauth2Url": oauthURL,
		})
	}
	err = srv.store.createProviderPlatform(r.Context(), &platform)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusCreated, map[string]interface{}{
		"platform": platform,
	})
}

// if errors occur within this handler they will be handled by passing the message and status via query parameters
// in the redirect url, therefore this method will only return nil as its error
func (srv *server) handleOAuthProviderCallback(w http.ResponseWriter, r *http.Request, log sLog) error {
	const (
		successRedirectUrl = "/learning-platforms?status=success&message=Provider platform %s successfully"
		errorRedirectUrl   = "/learning-platforms?status=error&message=Failed to configure provider platform"
	)
	stateFromClient := r.FormValue("state")
	if stateFromClient == "" { //state is an opaque value used by the client to maintain state between the request and callback
		log.error("state value not found from oauth provider, unable to process request") //error
		http.Redirect(w, r, errorRedirectUrl, http.StatusTemporaryRedirect)
		return nil
	}
	if srv.oauthState == nil {
		log.error("server not configured with NATS KeyValue buckets, unable to process request")
		http.Redirect(w, r, errorRedirectUrl, http.StatusTemporaryRedirect)
		return nil
	}
	entry, err := srv.oauthState.Get(stateFromClient)
	if err != nil {
		log.errorf("invalid oauth state value found from oauth provider and UnlockEd, state value found %s", stateFromClient)
		http.Redirect(w, r, errorRedirectUrl, http.StatusTemporaryRedirect)
		return nil
	}
	var provider ProviderPlatform
	err = json.Unmarshal(entry.Value(), &provider)
	if err != nil {
		log.errorf("unable to unmarshal value from bucket, error is %s", err)
		http.Redirect(w, r, errorRedirectUrl, http.StatusTemporaryRedirect)
		return nil
	}
	log.info("Deleting provider platform from bucket")
	if err := srv.oauthState.Delete(stateFromClient); err != nil {
		log.errorf("unable to delete entry in bucket, error is %s", err) //error
	}
	code := r.FormValue("code")
	config := provider.GetOAuth2Config()
	token, err := config.Exchange(context.Background(), code) // Exchange the authorization code for an access token
	if err != nil {
		log.errorf("unable to make oauth token exchange with provider platform, error is %s", err) //error
		http.Redirect(w, r, errorRedirectUrl, http.StatusTemporaryRedirect)
		return nil
	}
	//verfying that the BaseUrl used is correct and checking user role
	apiURL := fmt.Sprintf("%s/d2l/api/lp/%s/dataExport/bds/list", provider.BaseUrl, BrightspaceApiVersion)
	client := config.Client(context.Background(), token)
	resp, err := client.Get(apiURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		log.errorf("unable to execute api call, most likely due to provider BaseUrl %s not being correct, error is %s", provider.BaseUrl, err) //error
		http.Redirect(w, r, errorRedirectUrl, http.StatusTemporaryRedirect)
		return nil
	}
	provider.AccessKey = config.ClientSecret + ";" + token.RefreshToken
	var action string
	if provider.ID > 0 {
		if _, err := srv.store.updateProviderPlatform(r.Context(), &provider, provider.ID); err != nil {
			log.errorf("unable to update provider platform, error is %s", err) //error
			http.Redirect(w, r, errorRedirectUrl, http.StatusTemporaryRedirect)
			return nil
		}
		action = "updated"
	} else {
		if err := srv.store.createProviderPlatform(r.Context(), &provider); err != nil {
			log.errorf("unable to save provider platform, error is %s", err) //error
			http.Redirect(w, r, errorRedirectUrl, http.StatusTemporaryRedirect)
			return nil
		}
		action = "created"
	}
	http.Redirect(w, r, fmt.Sprintf(successRedirectUrl, action), http.StatusTemporaryRedirect)
	return nil
}

func (srv *server) handleOAuthRefreshToken(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "provider platform ID")
	}
	log.add("provider_platform_id", id)
	platform, err := srv.store.getProviderPlatformByID(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	oauthURL, err := srv.getOAuthUrl(platform)
	if err != nil {
		return err
	}
	return writeJsonResponse(w, http.StatusOK, map[string]interface{}{
		"oauth2Url": oauthURL,
	})
}

func (srv *server) handleUpdateProvider(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "provider platform ID")
	}
	log.add("provider_platform_id", id)
	var platform ProviderPlatform
	err = json.NewDecoder(r.Body).Decode(&platform)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	// A PATCH carries only what changed, so an update that leaves base_url alone
	// is not asked to justify it -- an existing plaintext provider can still be
	// archived or disabled.
	if platform.BaseUrl != "" {
		if err := requireHTTPS(platform.BaseUrl); err != nil {
			return newBadRequestServiceError(err, "provider base URL must use https")
		}
	}

	if platform.BaseUrl != "" || platform.AccessKey != "" || platform.AccountID != "" || (platform.State != "" && platform.State == Enabled) {
		existingPlatform, err := srv.store.getProviderPlatformByID(id)
		if err != nil {
			return newDatabaseServiceError(err)
		}
		if existingPlatform.Type == Brightspace && (platform.State == Enabled || existingPlatform.State == Enabled) {
			updateStruct(&existingPlatform, &platform)
			oauthURL, err := srv.getOAuthUrl(existingPlatform)
			if err != nil {
				return err
			}
			return writeJsonResponse(w, http.StatusOK, map[string]interface{}{
				"platform":  existingPlatform,
				"oauth2Url": oauthURL,
			})
		}
	}
	updated, err := srv.store.updateProviderPlatform(r.Context(), &platform, uint(id))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, map[string]interface{}{
		"platform": *updated,
	})
}

func (srv *server) handleDeleteProvider(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "provider platform ID")
	}
	log.add("providerPlatformId", id)
	if err = srv.store.deleteProviderPlatform(id); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusNoContent, "Provider platform deleted successfully")
}

func (srv *server) getOAuthUrl(platform *ProviderPlatform) (string, error) {
	var (
		brightspaceConfig = platform.GetOAuth2Config()
		oauthState        = CreateOAuth2UrlState()
		oauthURL          string
	)
	if !strings.HasPrefix(brightspaceConfig.RedirectURL, "https") {
		return oauthURL, newInternalServerServiceError(errors.New("web server does not use proper scheme https"), "Server not configured to use https scheme, unable to process oauth2 flow.")
	}
	if srv.oauthState == nil {
		return oauthURL, newInternalServerServiceError(errors.New("server not configured with NATS KeyValue buckets"), "Server not configured with buckets, unable to process oauth2 flow.")
	}
	platformBytes, err := json.Marshal(platform) //temporarily put provider platform in bucket to process in callback.
	if err != nil {
		return oauthURL, newInternalServerServiceError(err, "unable to marshal platform")
	}
	if _, err := srv.oauthState.Put(oauthState, platformBytes); err != nil {
		return oauthURL, newInternalServerServiceError(err, "could not add platform to bucket")
	}
	oauthURL = brightspaceConfig.AuthCodeURL(oauthState, oauth2.AccessTypeOffline)
	return oauthURL, nil
}

// OIDC clients

type RegisterClientRequest struct {
	RedirectURI        string `json:"redirect_uri"`
	ProviderPlatformID uint   `json:"provider_platform_id"`
	AutoRegister       bool   `json:"auto_register"`
}

func clientToResponse(client *OidcClient) *ClientResponse {
	return &ClientResponse{
		ClientID:      client.ClientID,
		ClientSecret:  client.ClientSecret,
		AuthEndpoint:  os.Getenv("APP_URL") + "/oauth2/auth",
		TokenEndpoint: os.Getenv("APP_URL") + "/oauth2/token",
		Scopes:        client.Scopes,
	}
}

func (srv *server) handleGetAllClients(w http.ResponseWriter, r *http.Request, log sLog) error {
	clients, err := srv.store.getAllRegisteredClients()
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, clients)
}

func (srv *server) handleGetOidcClient(w http.ResponseWriter, r *http.Request, log sLog) error {
	id := r.PathValue("id")
	log.add("oidc_id", id)
	client, err := srv.store.getOidcClientById(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, *clientToResponse(client))
}

// The OIDC handlers below update the provider without recording the admin as
// its update user, as they did in core.

func (srv *server) handleRecreateClient(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "oidc client ID")
	}
	log.add("oidc_client_id", id)
	existing, err := srv.store.getOidcClientById(fmt.Sprintf("%d", id))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	provider, err := srv.store.getProviderPlatformByID(int(existing.ProviderPlatformID))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	// delete from Hydra
	hydraReq, err := http.NewRequest("DELETE", os.Getenv("HYDRA_ADMIN_URL")+"/admin/clients/"+existing.ClientID, nil)
	if err == nil {
		hydraReq.Header.Set("Authorization", "Bearer "+os.Getenv("ORY_TOKEN"))
		resp, doErr := srv.client.Do(hydraReq)
		if doErr == nil {
			defer func() { _ = resp.Body.Close() }()
		}
	}
	if err := srv.store.deleteOidcClient(existing.ID); err != nil {
		return newDatabaseServiceError(err)
	}
	provider.ExternalAuthProviderId = ""
	if _, err := srv.store.updateProviderPlatform(context.Background(), provider, provider.ID); err != nil {
		return newDatabaseServiceError(err)
	}
	newClient, externalId, err := OidcClientFromProvider(provider, true, srv.client)
	if err != nil {
		return newInternalServerServiceError(err, err.Error())
	}
	if err := srv.store.registerClient(newClient); err != nil {
		return newDatabaseServiceError(err)
	}
	provider.ExternalAuthProviderId = externalId
	if _, err := srv.store.updateProviderPlatform(context.Background(), provider, provider.ID); err != nil {
		log.add("clientId", newClient.ID)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusCreated, *clientToResponse(newClient))
}

func (srv *server) handleRegisterClient(w http.ResponseWriter, r *http.Request, log sLog) error {
	request := RegisterClientRequest{}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	provider, err := srv.store.getProviderPlatformByID(int(request.ProviderPlatformID))
	log.add("providerPlatformId", request.ProviderPlatformID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if provider.OidcID != 0 || provider.ExternalAuthProviderId != "" {
		return newBadRequestServiceError(errors.New("client already registered"), "Client already registered")
	}
	client, externalId, err := OidcClientFromProvider(provider, request.AutoRegister, srv.client)
	if err != nil {
		return newInternalServerServiceError(err, err.Error())
	}
	log.add("externalId", externalId)
	if err := srv.store.registerClient(client); err != nil {
		return newDatabaseServiceError(err)
	}
	provider.ExternalAuthProviderId = externalId
	if _, err := srv.store.updateProviderPlatform(context.Background(), provider, provider.ID); err != nil {
		log.add("clientId", client.ID)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusCreated, *clientToResponse(client))
}

// Courses, outcomes and activity

/*
* @Query Params:
* ?page=: page
* ?perPage=: perPage
* ?sort=: sort
* ?search=: search
* ?searchFields=: searchFields
 */
func (srv *server) handleIndexCourses(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := getQueryContext(r)
	courses, err := srv.store.getCourses(&args)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writePaginatedResponse(w, http.StatusOK, courses, args.IntoMeta())
}

func (srv *server) handleShowCourse(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newBadRequestServiceError(err, "Invalid course ID")
	}
	course, err := srv.store.getCourseByID(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, course)
}

/****
 * @Query Params:
 * ?type=: "certificate", "grade", "pathway_completion", "college_credit"
 ****/
func (srv *server) handleGetOutcomes(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	typeString := r.URL.Query().Get("type")
	outcomeType := OutcomeType(typeString)
	args := getQueryContext(r)
	args.UserID = uint(id)
	outcome, err := srv.store.getOutcomesForUser(&args, outcomeType)
	if err != nil {
		log.add("user_id", id)
		log.add("type", outcomeType)
		return newDatabaseServiceError(err)
	}
	return writePaginatedResponse(w, http.StatusOK, outcome, args.IntoMeta())
}

/****
 * @Query Params:
 * ?start_date
 * ?end_date
 ****/
func (srv *server) handleGetDailyActivityByUserID(w http.ResponseWriter, r *http.Request, log sLog) error {
	userID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	startDate, err := time.Parse("2006-01-02", strings.Split(r.URL.Query().Get("start_date"), "T")[0])
	if err != nil {
		return newInvalidQueryParamServiceError(err, "start_date")
	}
	endDate, err := time.Parse("2006-01-02", strings.Split(r.URL.Query().Get("end_date"), "T")[0])
	if err != nil {
		return newInvalidQueryParamServiceError(err, "end_date")
	}
	activities, err := srv.store.getDailyActivityByUserID(userID, startDate, endDate)
	if err != nil {
		log.error("error getting daily activity by user ID")
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, map[string]any{
		"activities": activities,
	})
}

func (srv *server) handleGetCourseActivity(w http.ResponseWriter, r *http.Request, log sLog) error {
	courseID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "course ID")
	}
	log.add("course_id", courseID)
	page, perPage := getPaginationInfo(r)
	count, activities, err := srv.store.getActivityByCourseID(page, perPage, courseID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, map[string]any{
		"count":      count,
		"activities": activities,
	})
}

/**
* GET: /api/users/{id}/catalog
* @Query Params:
* tag: any number of tags to filter by
* ?tag=some_tag&tag=another_tag
* provider_id: provider id to filter by
**/
func (srv *server) handleUserCatalog(w http.ResponseWriter, r *http.Request, log sLog) error {
	userId, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user_id")
	}
	tags := r.URL.Query()["tags"]
	var tagsSplit []string
	if len(tags) > 0 {
		tagsSplit = strings.Split(tags[0], ",")
	}
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	order := r.URL.Query().Get("order")
	userCatalog, err := srv.store.getUserCatalog(userId, tagsSplit, search, order)
	if err != nil {
		log.add("user_id", userId)
		log.add("search", search)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, userCatalog)
}

func (srv *server) handleUserCourses(w http.ResponseWriter, r *http.Request, log sLog) error {
	userId, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	log.add("user_id", userId)
	args := getQueryContext(r)
	args.UserID = uint(userId)
	userCourses, err := srv.store.getUserCourses(&args)
	if err != nil {
		log.add("search", args.Search)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, userCourses)
}
