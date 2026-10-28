package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
	"github.com/UnlockedLabs/unlocked-core/backend/src/jasper"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
)

// moduleRouter registers feature-module routes behind the same auth, admin and
// feature-flag middleware as core's own routes. The module's handler gets the
// caller's identity.Claims and can add fields, such as its admin audit action,
// to core's log lines for the request.
type moduleRouter struct{ srv *Server }

func (mr moduleRouter) Handle(rt module.Route) {
	h := func(w http.ResponseWriter, r *http.Request, log sLog) error {
		claims := r.Context().Value(ClaimsKey).(*Claims)
		ctx := identity.NewContext(r.Context(), claims.identityClaims())
		ctx = module.WithLog(ctx, log.f, log.add)
		rt.Handler.ServeHTTP(w, r.WithContext(ctx))
		return nil
	}
	def := routeDef{routeMethod: rt.Pattern, handler: h, features: rt.Features}
	var checks []RouteResolver
	switch rt.Access {
	case module.AnyUser:
	case module.Admin:
		def.admin = true
	case module.DeptAdmin:
		def.admin = true
		checks = append(checks, DeptAdminResolver)
	case module.SystemAdmin:
		def.admin = true
		checks = append(checks, SystemAdminResolver)
	default:
		// Registration runs once at startup, so an unknown level stops the
		// server instead of leaving a route open.
		panic(fmt.Sprintf("module route %q: unknown access level %d", rt.Pattern, rt.Access))
	}
	if rt.UserParam != "" {
		checks = append(checks, userResolver(rt))
	}
	if rt.Allow != nil {
		checks = append(checks, allowResolver(rt.Allow))
	}
	if len(checks) > 0 {
		def.resolver = AllResolvers(checks...)
	}
	mr.srv.register(func() []routeDef { return []routeDef{def} })
}

// userResolver is the check for a route's UserParam: the same check core's own
// routes use for the user in their path. A misconfigured route stops startup
// instead of leaving the route open or closed by mistake.
func userResolver(rt module.Route) RouteResolver {
	if !strings.Contains(rt.Pattern, "{"+rt.UserParam+"}") {
		panic(fmt.Sprintf("module route %q: no path parameter %q", rt.Pattern, rt.UserParam))
	}
	switch rt.Access {
	case module.AnyUser:
		return UserRoleResolver(rt.UserParam)
	case module.Admin:
		return FacilityAdminResolver("users", rt.UserParam)
	default:
		panic(fmt.Sprintf("module route %q: UserParam works only with AnyUser and Admin", rt.Pattern))
	}
}

// allowResolver runs a module's own access check with the caller's
// identity.Claims.
func allowResolver(allow func(*http.Request, *identity.Claims) bool) RouteResolver {
	return func(_ *database.DB, r *http.Request) bool {
		return allow(r, r.Context().Value(ClaimsKey).(*Claims).identityClaims())
	}
}

// identityClaims is what a module sees of the caller: core's claims without
// the Kratos and password-reset details.
func (c *Claims) identityClaims() *identity.Claims {
	return &identity.Claims{
		UserID:        c.UserID,
		Username:      c.Username,
		Role:          c.Role,
		FacilityID:    c.FacilityID,
		FacilityName:  c.FacilityName,
		FeatureAccess: c.FeatureAccess,
		SessionID:     c.SessionID,
		TimeZone:      c.TimeZone,
	}
}

// wsNotifier sends module messages over the websocket. It reads srv.wsClient
// on each call because the server creates the websocket after registering
// routes, and the testing server has none.
type wsNotifier struct{ srv *Server }

func (n wsNotifier) Notify(userID uint, msg module.Message) {
	if n.srv.wsClient == nil {
		return
	}
	n.srv.wsClient.notifyUser(WsMsg{
		EventType: WsEventType(msg.Event),
		UserID:    userID,
		Msg:       MsgContent{ActivityID: msg.ActivityID, Msg: msg.Text},
	})
}

// moduleUsers gives modules core's user accounts. It reads srv's fields on each
// call, like wsNotifier.
type moduleUsers struct{ srv *Server }

func (u moduleUsers) Get(ctx context.Context, id uint) (module.User, error) {
	user, err := database.NewDB(u.srv.Db.WithContext(ctx)).GetUserByID(id)
	if err != nil {
		return module.User{}, fmt.Errorf("get user %d: %w", id, dbCause(err))
	}
	return toModuleUser(user), nil
}

func (u moduleUsers) Create(ctx context.Context, nu module.NewUser) (module.User, error) {
	user := models.User{
		Username:   nu.Username,
		NameFirst:  nu.NameFirst,
		NameLast:   nu.NameLast,
		Email:      nu.Email,
		FacilityID: nu.FacilityID,
		Role:       nu.Role,
	}
	// The creator comes from ctx the way WithUserContext takes it from a
	// request.
	if c, ok := identity.FromContext(ctx); ok {
		ctx = context.WithValue(ctx, models.UserIDKey, c.UserID)
	}
	if err := database.NewDB(u.srv.Db.WithContext(ctx)).CreateUser(&user); err != nil {
		return module.User{}, fmt.Errorf("create user %s: %w", nu.Username, dbCause(err))
	}
	return toModuleUser(&user), nil
}

func (u moduleUsers) CreateLogin(ctx context.Context, userID uint) (string, error) {
	user, err := database.NewDB(u.srv.Db.WithContext(ctx)).GetUserByID(userID)
	if err != nil {
		return "", fmt.Errorf("get user %d: %w", userID, dbCause(err))
	}
	password, err := user.CreateTempPassword()
	if err != nil {
		return "", fmt.Errorf("create temporary password: %w", err)
	}
	if err := u.srv.HandleCreateUserKratos(user.Username, password); err != nil {
		return "", fmt.Errorf("create kratos login for user %d: %w", userID, err)
	}
	return password, nil
}

func (u moduleUsers) Delete(ctx context.Context, id uint) error {
	if err := database.NewDB(u.srv.Db.WithContext(ctx)).DeleteUser(int(id)); err != nil {
		return fmt.Errorf("delete user %d: %w", id, dbCause(err))
	}
	return nil
}

// httpClient sends module requests with srv.Client. It reads srv.Client on
// each call, like wsNotifier, so a client set after startup is the one used.
type httpClient struct{ srv *Server }

func (c httpClient) Do(req *http.Request) (*http.Response, error) {
	return c.srv.Client.Do(req)
}

// facilityFeatures gives modules core's feature flags for each facility.
type facilityFeatures struct{ srv *Server }

func (f facilityFeatures) FacilityFeatures(ctx context.Context, facilityID uint) ([]models.FeatureAccess, error) {
	access, err := database.NewDB(f.srv.Db.WithContext(ctx)).GetFacilityFeatureAccess(facilityID, f.srv.features)
	if err != nil {
		return nil, fmt.Errorf("get features for facility %d: %w", facilityID, dbCause(err))
	}
	return access, nil
}

// pdfReports renders module reports with core's Jasper report templates.
type pdfReports struct{}

func (pdfReports) PDF(template string, report module.Report) ([]byte, error) {
	return jasper.GenerateReportPDF(template, report)
}

func toModuleUser(u *models.User) module.User {
	return module.User{
		ID:         u.ID,
		Username:   u.Username,
		NameFirst:  u.NameFirst,
		NameLast:   u.NameLast,
		Email:      u.Email,
		FacilityID: u.FacilityID,
	}
}

// dbCause is the error inside a database.DBError, which doesn't unwrap, so a
// module can match it with errors.Is.
func dbCause(err error) error {
	var dbErr database.DBError
	if errors.As(err, &dbErr) {
		return dbErr.InternalErr
	}
	return err
}

func (srv *Server) moduleDeps() module.Deps {
	deps := module.Deps{
		DB:        srv.Db.DB,
		NATS:      srv.nats,
		Notify:    wsNotifier{srv},
		Users:     moduleUsers{srv},
		HTTP:      httpClient{srv},
		Features:  facilityFeatures{srv},
		Reports:   pdfReports{},
		ModuleDBs: srv.moduleDBs.For,
	}
	deps.LiveProviders = registry.LiveProviders(deps)
	return deps
}

func (srv *Server) registerModuleRoutes() {
	deps := srv.moduleDeps()
	for _, m := range srv.modules {
		moduleDeps := deps
		moduleDeps.DB = srv.moduleDBs.For(m.Name())
		m.Register(moduleRouter{srv}, moduleDeps)
	}
}
