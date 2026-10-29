package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

/*
Client-side crash reporting.

The SPA's only error boundary is react-router's `errorElement`, which renders
`pages/Error.tsx` — a page that used to discard the exception entirely. A resident
therefore saw "Something went wrong" while the deployment logs stayed completely
silent, which is what made ID-846 impossible to diagnose from Maine's logs. This
endpoint gives the browser somewhere to say what actually threw.

Two known blind spots, both acceptable: the report needs a valid session and a CSRF
cookie, so a crash caused by a broken session reports nothing; and a resident still
carrying `password_reset` gets the HTML redirect from `authMiddleware` instead of a
log line (see auth.go's password-reset gate).
*/

// Field caps. A stack is the only field worth real bytes; everything else is short
// by nature, and all of it arrives from the browser, so none of it is trusted.
const (
	clientErrorStackMax = 2048
	clientErrorFieldMax = 512
)

type ClientErrorReport struct {
	Name      string `json:"name"`
	Message   string `json:"message"`
	Stack     string `json:"stack"`
	Route     string `json:"route"`
	Source    string `json:"source"`
	UserAgent string `json:"user_agent"`
}

// Caps length and strips newlines so one report cannot spread across log lines or
// flood the log with a runaway stack.
func truncateForLog(value string, max int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > max {
		value = value[:max] + "…(truncated)"
	}
	return strings.ReplaceAll(value, "\n", " | ")
}

func (srv *Server) registerClientErrorRoutes() []routeDef {
	return []routeDef{
		// Deliberately has no feature gate: a crash must be reportable with
		// every feature turned off.
		newRoute("POST /api/client-errors", srv.handleReportClientError),
	}
}

func (srv *Server) handleReportClientError(w http.ResponseWriter, r *http.Request, log sLog) error {
	report := ClientErrorReport{}
	defer func() {
		if r.Body.Close() != nil {
			log.warn("error closing client error report body")
		}
	}()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&report); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if claims, ok := r.Context().Value(ClaimsKey).(*Claims); ok {
		// handleError only attaches these for admins on writes, so a resident's
		// report would otherwise carry no identity at all.
		log.add("user_id", claims.UserID)
		log.add("facility_id", claims.FacilityID)
		log.add("role", claims.Role)
	}
	log.add("client_error", true)
	log.add("error_name", truncateForLog(report.Name, clientErrorFieldMax))
	log.add("error_message", truncateForLog(report.Message, clientErrorFieldMax))
	log.add("client_route", truncateForLog(report.Route, clientErrorFieldMax))
	log.add("client_source", truncateForLog(report.Source, clientErrorFieldMax))
	log.add("user_agent", truncateForLog(report.UserAgent, clientErrorFieldMax))
	log.add("stack", truncateForLog(report.Stack, clientErrorStackMax))
	log.error("client-side error reported by browser")
	w.WriteHeader(http.StatusNoContent)
	return nil
}
