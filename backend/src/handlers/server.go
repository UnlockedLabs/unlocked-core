package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"

	database "github.com/UnlockedLabs/unlocked-core/backend/src/database"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	ocapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/opencontent/api"
	programsapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs/api"
	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"
	"github.com/UnlockedLabs/unlocked-core/backend/src/tasks"

	"github.com/nats-io/nats.go"
	ory "github.com/ory/kratos-client-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

type Server struct {
	port              string
	Db                *database.DB
	Mux               *http.ServeMux
	OryClient         *ory.APIClient
	Client            *http.Client
	nats              *nats.Conn
	dev               bool
	buckets           map[string]nats.KeyValue
	features          []models.FeatureAccess
	testingMode       bool
	wsClient          *ClientManager
	scheduler         *tasks.Scheduler
	modules           []module.Module
	openContent       ocapi.OpenContent
	providerPlatforms ppapi.ProviderPlatforms
	programs          programsapi.Programs
}

type routeDef struct {
	routeMethod string
	handler     HttpFunc
	admin       bool
	features    []models.FeatureAccess
	resolver    RouteResolver
}

func (srv *Server) register(routes func() []routeDef) {
	for _, route := range routes() {
		h := route.handler
		if route.admin {
			srv.Mux.Handle(route.routeMethod,
				srv.applyAdminMiddleware(h, route.resolver, route.features...),
			)
		} else {
			srv.Mux.Handle(route.routeMethod,
				srv.applyMiddleware(h, route.resolver, route.features...),
			)
		}
	}
}

/**
* Register all API routes right here
**/
func (srv *Server) RegisterRoutes() {
	/* register special routes */
	srv.registerImageRoutes()
	srv.registerWebsocketRoute()
	srv.registerInternalRoutes()
	srv.registerVersionRoute()
	srv.Mux.Handle("/api/metrics", promhttp.Handler())
	srv.Mux.HandleFunc("/api/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("OK")); err != nil {
			log.Errorln("Error writing healthcheck response: ", err)
			return
		}
	})
	for _, route := range []func() []routeDef{
		srv.registerLoginFlowRoutes,
		srv.registerAuthRoutes,
		srv.registerUserRoutes,
		srv.registerActivityRoutes,
		srv.registerDashboardRoutes,
		srv.registerOryRoutes,
		srv.registerFacilitiesRoutes,
		srv.registerFacilityFeatureAccessRoutes,
		srv.registerDemoSeedRoutes,
		srv.registerLearningRecordRoutes,
		srv.registerClientErrorRoutes,
	} {
		srv.register(route)
	}
	srv.registerModuleRoutes()
}

func init() {
	prometheus.MustRegister(requestCount)
	prometheus.MustRegister(requestDuration)
	prometheus.MustRegister(requestSize)
	prometheus.MustRegister(responseSize)
	prometheus.MustRegister(responseStatus)
	prometheus.MustRegister(errorCount)
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/proxy/") {
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		} else {
			w.Header().Set("X-Frame-Options", "DENY")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if os.Getenv("APP_ENV") != "dev" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func (srv *Server) Handler() http.Handler {
	return securityHeadersMiddleware(corsMiddleware(srv.Mux))
}

func (srv *Server) ListenAndServe(ctx context.Context) {
	handler := srv.Handler()
	log.Println("Starting server on port: ", srv.port)
	// Listen for context cancellation to trigger graceful shutdown
	go func() {
		<-ctx.Done()
		log.Println("Context cancelled, shutting down server...")
		if srv != nil {
			srv.Shutdown()
		}
	}()
	if err := http.ListenAndServe(":"+srv.port, handler); err != nil {
		log.Fatal(err)
	}
}

func (srv *Server) Shutdown() {
	srv.wsClient.Close(srv.Db, srv.openContent)
	if srv.scheduler != nil {
		if err := srv.scheduler.Stop(); err != nil {
			log.Errorf("Error stopping scheduler: %v", err)
		}
	}
	srv.nats.Close()
}

// NewServer builds the API server. Outside testing it returns nil if ctx is
// canceled while it waits for the database to be migrated.
func NewServer(isTesting bool, ctx context.Context) *Server {
	if isTesting {
		return newTestingServer()
	}
	return newServer(ctx)
}

func newServer(ctx context.Context) *Server {
	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}
	conn, err := ConnectNATS()
	if err != nil {
		log.Errorf("Failed to connect to NATS: %v", err)
	}
	dev := os.Getenv("APP_ENV") == "dev"
	db := database.InitDB(ctx, false)
	if db == nil {
		return nil
	}
	server := Server{
		port:              port,
		Db:                db,
		Mux:               http.NewServeMux(),
		OryClient:         ory.NewAPIClient(oryConfig()),
		Client:            &http.Client{},
		nats:              conn,
		dev:               dev,
		modules:           registry.All(),
		openContent:       registry.OpenContent(db.DB),
		providerPlatforms: registry.ProviderPlatforms(db.DB),
		programs:          registry.Programs(db.DB),
	}
	features, err := server.Db.GetFeatureAccess()
	if err != nil {
		log.Fatal("Failed to fetch feature flags")
	}
	server.features = features
	err = server.setupNatsKvBuckets()
	if err != nil {
		log.Fatalf("Failed to setup JetStream KV store: %v", err)
	}
	server.RegisterRoutes()
	if err := server.setupDefaultAdminInKratos(ctx); err != nil {
		log.Fatal("Error setting up default admin in Kratos")
	}
	server.wsClient = newClientManager()
	if tasks.ServerRunsJobs() {
		server.scheduler = tasks.Start(dev, server.nats, server.Db.DB, server.modules)
	} else {
		log.Info("RUN_SCHEDULER=false: background jobs run in the scheduler process, not here")
	}
	return &server
}

func newTestingServer() *Server {
	db := database.InitDB(context.Background(), true)
	features := models.AllFeatures
	return &Server{
		Db:                db,
		Mux:               http.NewServeMux(),
		OryClient:         nil,
		Client:            nil,
		features:          features,
		testingMode:       true,
		modules:           registry.All(),
		openContent:       registry.OpenContent(db.DB),
		providerPlatforms: registry.ProviderPlatforms(db.DB),
		programs:          registry.Programs(db.DB),
	}
}

// ConnectNATS connects to NATS with NATS_URL, NATS_USER and NATS_PASSWORD. The
// scheduler process uses it too.
func ConnectNATS() (*nats.Conn, error) {
	options := nats.GetDefaultOptions()
	options.Url = os.Getenv("NATS_URL")
	options.User = os.Getenv("NATS_USER")
	options.Password = os.Getenv("NATS_PASSWORD")
	conn, err := options.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	return conn, nil
}

func oryConfig() *ory.Configuration {
	return &ory.Configuration{
		Servers: ory.ServerConfigurations{
			{
				URL: os.Getenv("KRATOS_ADMIN_URL"),
			},
			{
				URL: os.Getenv("KRATOS_PUBLIC_URL"),
			},
		},
		DefaultHeader: map[string]string{"Authorization": "Bearer " + os.Getenv("KRATOS_TOKEN")},
	}
}

const (
	CachedUsers  string = "cache_users"
	LoginMetrics string = "login_metrics"
	AdminLayer2  string = "admin_layer_2"
)

func (srv *Server) setupNatsKvBuckets() error {
	js, err := srv.nats.JetStream()
	if err != nil {
		log.Fatalf("Error initializing JetStream: %v", err)
		return err
	}
	buckets := map[string]nats.KeyValue{}
	for _, bucket := range []string{CachedUsers, LoginMetrics, AdminLayer2} {
		kv, err := js.KeyValue(bucket)
		if err != nil {
			cfg := &nats.KeyValueConfig{
				Bucket:  bucket,
				History: 1,
			}
			switch bucket {
			case CachedUsers:
				cfg.TTL = time.Hour * 1
			default:
				cfg.TTL = time.Hour * 24
			}
			kv, err = js.CreateKeyValue(cfg)
			if err != nil {
				log.Errorf("Error creating JetStream KV store: %v", err)
				return err
			}
		}
		buckets[bucket] = kv
	}
	srv.buckets = buckets
	return nil
}

func (srv *Server) checkForAdminInKratos(ctx context.Context) (string, error) {
	identities, err := srv.handleFindKratosIdentities(ctx)
	if err != nil {
		log.Error("unable to get identities in kratos")
		return "", err
	}

	log.Debug("checking for admin in kratos")
	for _, user := range identities {
		if username, ok := user.GetTraitsOk(); ok {
			log.Debug("got traits from ory identity")
			if username != nil {
				deref := *username
				if traits, ok := deref.(map[string]any); !ok {
					log.Debug("cannot deref traits from ory identity")
					return "", nil
				} else {
					if traits["username"].(string) == "SuperAdmin" {
						log.Debug("checking for admin in kratos, found admin with ID:", user.GetId())
						return user.GetId(), nil
					}
				}
			}
		}
	}
	return "", nil
}

func (srv *Server) syncKratosAdminDB(ctx context.Context) error {
	id, err := srv.checkForAdminInKratos(ctx)
	if err != nil {
		return err
	}
	if id == "" {
		// this means the default admin was just created, so we use default password
		err := srv.providerPlatforms.SetupKolibri(ctx)
		if err != nil {
			log.Warn("KOLIBRI_URL was set but failed to generate OIDC client for kolibri")
		}
		if err := srv.HandleCreateUserKratos("SuperAdmin", "ChangeMe!"); err != nil {
			return err
		}
		return nil
	} else {
		// the admin acct exists in kratos already
		user, err := srv.Db.GetSystemAdmin(ctx)
		if err != nil {
			log.Fatal("this should never happen, we just created the user")
		}
		if models.FormatNullableString(user.KratosID) != id {
			if err := srv.Db.Exec("UPDATE users SET kratos_id = ? WHERE id = 1", id).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (srv *Server) setupDefaultAdminInKratos(ctx context.Context) error {
	user, err := srv.Db.GetSystemAdmin(ctx)
	if err != nil {
		srv.Db.SeedDefaultData(false)
		return srv.setupDefaultAdminInKratos(ctx)
		// rerun after seeding the default user
	}
	if user.KratosID != nil {
		// double check that the stored kratos ID is valid
		if err := srv.validateUserIDKratos(*user.KratosID); err != nil {
			// if not, it's a freshly migrated database
			// so we create the OIDC client if KOLIBRI_URL is set
			// then create the default admin in kratos
			log.Println("kratos id not found")
			return srv.HandleCreateUserKratos(user.Username, "ChangeMe!")
		}
	}
	return srv.syncKratosAdminDB(ctx)
}

func (srv *Server) getFacilityID(r *http.Request) uint {
	return r.Context().Value(ClaimsKey).(*Claims).FacilityID
}

func (srv *Server) getUserID(r *http.Request) uint {
	return r.Context().Value(ClaimsKey).(*Claims).UserID
}

func (srv *Server) getPaginationInfo(r *http.Request) (int, int) {
	page := r.URL.Query().Get("page")
	perPage := r.URL.Query().Get("per_page")
	if page == "" {
		page = "1"
	}
	if perPage == "" {
		perPage = "20"
	}
	intPage, err := strconv.Atoi(page)
	if err != nil || intPage < 1 {
		intPage = 1
	}
	intPerPage, err := strconv.Atoi(perPage)
	if err != nil || intPerPage < 1 {
		intPerPage = 20
	}
	return intPage, intPerPage
}

type HttpFunc func(w http.ResponseWriter, r *http.Request, log sLog) error

// Wraps a handler function that returns an error so that it can handle the error by writing it to the response and logging it.
func (svr *Server) handleError(handler HttpFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log := sLog{f: log.Fields{"handler": getHandlerName(handler), "method": r.Method, "path": r.URL.Path}}
		claims, ok := r.Context().Value(ClaimsKey).(*Claims)
		audit := false
		if ok {
			if claims.isAdmin() && r.Method != http.MethodGet {
				audit = true
				ip := r.Header.Get("X-Forwarded-For")
				if ip == "" {
					ip = r.RemoteAddr
				} else {
					ip = strings.Split(ip, ",")[0]
				}
				log.add("admin_id", claims.UserID)
				log.add("username", claims.Username)
				log.add("role", claims.Role)
				log.add("session_id", claims.SessionID)
				log.add("facility_id", claims.FacilityID)
				log.add("facility_name", claims.FacilityName)
				log.add("ip_address", ip)
			}
		}
		if err := handler(w, r, log); err != nil {
			if svcErr, ok := err.(serviceError); ok {
				svr.errorResponse(w, svcErr.Status, svcErr.Message)
				log.error("Error occurred is ", svcErr.Err)
			} else { //capture all other error types
				svr.errorResponse(w, http.StatusInternalServerError, err.Error())
				log.error("Error occurred is ", err.Error())
			}
		}
		defer func() {
			if r.Body.Close() != nil {
				log.error("Error closing request body")
			}
		}()
		if audit {
			log.adminAudit()
		}
	})
}

// Uses reflection to get the name of the handler function using a series of string splits
func getHandlerName(handler HttpFunc) string {
	handlerName := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()
	hyphenSplit := strings.Split(handlerName, "-")
	if len(hyphenSplit) != 2 {
		return "NameNotFound"
	}
	periodSplit := strings.Split(hyphenSplit[0], ".")
	if len(periodSplit) != 3 {
		return "NameNotFound"
	}
	return periodSplit[2]
}

func writePaginatedResponse[T any](w http.ResponseWriter, status int, data []T, meta models.PaginationMeta) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := models.PaginatedResource[T]{Message: "", Data: data, Meta: meta}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return newResponseServiceError(err)
	}
	return nil
}

func writeJsonResponse[T any](w http.ResponseWriter, status int, data T) error {
	if any(data) == nil {
		w.WriteHeader(status)
		return nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if str, ok := any(data).(string); ok {
		resp := models.Resource[struct{}]{Message: str, Data: struct{}{}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			return newResponseServiceError(err)
		}
		return nil
	}
	resp := models.Resource[T]{Message: "Data fetched successfully", Data: data}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return newResponseServiceError(err)
	}
	return nil
}

// Returns a database instance with user context for audit hooks
func (srv *Server) WithUserContext(r *http.Request) *database.DB {
	if claims, ok := r.Context().Value(ClaimsKey).(*Claims); ok {
		return database.NewDB(srv.Db.WithContext(context.WithValue(r.Context(), models.UserIDKey, claims.UserID)))
	}
	return database.NewDB(srv.Db.WithContext(r.Context()))
}

func writeFacilityDeleteConflictResponse(w http.ResponseWriter, message string, blockers models.FacilityBlockingChildren) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	resp := models.Resource[models.FacilityBlockingChildren]{
		Message: message,
		Data:    blockers,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return newResponseServiceError(err)
	}
	return nil
}

func (srv *Server) errorResponse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resource := models.Resource[any]{Message: message}
	err := json.NewEncoder(w).Encode(resource)
	if err != nil {
		log.Error("error writing error response: ", err)
		http.Error(w, message, status)
	}
}
