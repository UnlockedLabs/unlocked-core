package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/sirupsen/logrus"
)

// sLog is a wrapper around the log.Fields map and is implemented by the handleError method, this struct is not intended to be accessed directly and was created to make adding key/values and logging more efficient.
type sLog struct{ f logrus.Fields }

func (log sLog) adminAudit() {
	log.f["audit"] = "admin_action"
	logrus.WithFields(log.f).Println()
}

func (slog sLog) info(args ...any) {
	logrus.WithFields(slog.f).Info(args...)
}

func (slog sLog) infof(format string, args ...any) {
	logrus.WithFields(slog.f).Infof(format, args...)
}

func (slog sLog) debug(args ...any) {
	logrus.WithFields(slog.f).Debug(args...)
}

func (slog sLog) warn(args ...any) {
	logrus.WithFields(slog.f).Warn(args...)
}

func (slog sLog) error(args ...any) {
	logrus.WithFields(slog.f).Error(args...)
}

func (slog sLog) errorf(format string, args ...any) {
	logrus.WithFields(slog.f).Errorf(format, args...)
}

func (slog *sLog) add(key string, value any) {
	slog.f[key] = value
}

// getQueryContext resolves the facility scope for a request: a switch-capable
// admin (sysadmin/dept admin) gets the ?facility_id they're viewing, or 0
// (statewide) when none is selected; a facility admin is always pinned to their
// own facility. See facilityScopedQueryContext for reads that must fall back to
// the home facility instead of going statewide.
func (srv *Server) getQueryContext(r *http.Request) models.QueryContext {
	var facilityID, userID uint
	claims := r.Context().Value(ClaimsKey).(*Claims)
	ctxWithUser := context.WithValue(r.Context(), models.UserIDKey, claims.UserID)
	f, err := strconv.Atoi(r.URL.Query().Get("facility_id"))
	switch {
	case err == nil && claims.canSwitchFacility():
		facilityID = uint(f)
	case claims.canSwitchFacility():
		facilityID = 0
	default:
		facilityID = claims.FacilityID
	}
	u, err := strconv.Atoi(r.URL.Query().Get("user_id"))
	if err != nil {
		userID = claims.UserID
	} else {
		userID = uint(u)
	}
	page, perPage := srv.getPaginationInfo(r)
	orderBy := strings.ToLower(r.URL.Query().Get("order_by"))
	order := strings.ToLower(r.URL.Query().Get("order"))
	if orderBy != "" && order == "" {
		orderBySplit := strings.Fields(orderBy)
		if len(orderBySplit) > 1 {
			orderBy = orderBySplit[0]
			order = orderBySplit[1]
		}
	}
	search := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("search")))
	tags := r.URL.Query()["tags"]
	all := r.URL.Query().Get("all") == "true"
	includeDeactivated := r.URL.Query().Get("include_deactivated") == "true"
	tz := claims.TimeZone
	return models.QueryContext{
		Params:             r.URL.Query(),
		Ctx:                ctxWithUser,
		Page:               page,
		PerPage:            perPage,
		FacilityID:         uint(facilityID),
		UserID:             uint(userID),
		OrderBy:            orderBy,
		Order:              order,
		IsAdmin:            claims.isAdmin(),
		CanSwitchFacility:  claims.canSwitchFacility(),
		Search:             search,
		Tags:               tags,
		All:                all,
		Timezone:           tz,
		IncludeDeactivated: includeDeactivated,
		FeatureAccess:      claims.FeatureAccess,
	}
}

// facilityScopedQueryContext is for reads whose results are inherently
// per-facility (resident/user listings tied to a facility). A statewide admin
// has no single facility (FacilityID == 0), so these fall back to the admin's
// home facility rather than matching no facility at all.
func (srv *Server) facilityScopedQueryContext(r *http.Request) models.QueryContext {
	args := srv.getQueryContext(r)
	if args.FacilityID == 0 {
		args.FacilityID = r.Context().Value(ClaimsKey).(*Claims).FacilityID
	}
	return args
}

// requireFacilityID resolves the request's target facility for a facility-scoped
// write. A statewide admin (FacilityID == 0) has not selected one, which is an
// error for a write that must land in a concrete facility.
func (srv *Server) requireFacilityID(r *http.Request) (uint, error) {
	facilityID := srv.getQueryContext(r).FacilityID
	if facilityID == 0 {
		return 0, newBadRequestServiceError(nil, "facility selection is required")
	}
	return facilityID, nil
}

func newRoute(method string, handler HttpFunc) routeDef {
	return routeDef{
		routeMethod: method,
		handler:     handler,
		admin:       false,
		features:    []models.FeatureAccess{},
	}
}

func newAdminRoute(method string, handler HttpFunc) routeDef {
	return routeDef{
		routeMethod: method,
		handler:     handler,
		admin:       true,
		features:    []models.FeatureAccess{},
	}
}

func newDeptAdminRoute(method string, handler HttpFunc) routeDef {
	return routeDef{
		routeMethod: method,
		handler:     handler,
		admin:       true,
		features:    []models.FeatureAccess{},
		resolver:    DeptAdminResolver,
	}
}

func newSystemAdminRoute(method string, handler HttpFunc) routeDef {
	return routeDef{
		routeMethod: method,
		handler:     handler,
		admin:       true,
		features:    []models.FeatureAccess{},
		resolver:    SystemAdminResolver,
	}
}

func validatedRoute(method string, handler HttpFunc, validate RouteResolver) routeDef {
	return routeDef{
		routeMethod: method,
		handler:     handler,
		admin:       false,
		features:    []models.FeatureAccess{},
		resolver:    validate,
	}
}

func validatedAdminRoute(method string, handler HttpFunc, validate RouteResolver) routeDef {
	return routeDef{
		routeMethod: method,
		handler:     handler,
		admin:       true,
		features:    []models.FeatureAccess{},
		resolver:    validate,
	}
}

