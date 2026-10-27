package learningrecord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// The response and error helpers Learning Record's handlers use. They are
// copies of core's (EN-188: handler helpers stay in each module), so every
// response is the same JSON as before the move.

type resource[T any] struct {
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func writeJsonResponse[T any](w http.ResponseWriter, status int, data T) error {
	if any(data) == nil {
		w.WriteHeader(status)
		return nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if str, ok := any(data).(string); ok {
		resp := resource[struct{}]{Message: str, Data: struct{}{}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			return newResponseServiceError(err)
		}
		return nil
	}
	resp := resource[T]{Message: "Data fetched successfully", Data: data}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return newResponseServiceError(err)
	}
	return nil
}

func errorResponse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(resource[any]{Message: message}); err != nil {
		logrus.Error("error writing error response: ", err)
		http.Error(w, message, status)
	}
}

// handlerFunc is a route handler that returns its error for handle to write.
type handlerFunc func(w http.ResponseWriter, r *http.Request, log sLog) error

// handle turns h into an http.Handler that writes h's error as core's
// handleError does.
func handle(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Start from core's fields, so an admin write's error line names the
		// admin, as core's own handlers' lines do.
		f := logrus.Fields(module.LogFields(r.Context()))
		f["module"] = "learningrecord"
		f["method"] = r.Method
		f["path"] = r.URL.Path
		log := sLog{ctx: r.Context(), f: f}
		if err := h(w, r, log); err != nil {
			var svcErr serviceError
			if errors.As(err, &svcErr) {
				errorResponse(w, svcErr.Status, svcErr.Message)
				log.error("Error occurred is ", svcErr.Err)
			} else {
				errorResponse(w, http.StatusInternalServerError, err.Error())
				log.error("Error occurred is ", err.Error())
			}
		}
	})
}

// sLog adds fields to a request's log lines, like core's sLog.
type sLog struct {
	ctx context.Context
	f   logrus.Fields
}

// add adds a field to this module's log lines and to core's, so it also
// reaches the admin audit line, as it did before the move.
func (slog sLog) add(key string, value any) {
	slog.f[key] = value
	module.LogField(slog.ctx, key, value)
}

func (slog sLog) error(args ...any) {
	logrus.WithFields(slog.f).Error(args...)
}

// serviceError is an error with the status and message a handler answers
// with.
type serviceError struct {
	Status  int
	Message string
	Err     error
}

func (svcErr serviceError) Error() string {
	return fmt.Sprintf("service error: %s", svcErr.Err.Error())
}

func newServiceError(err error, status int, msg string) serviceError {
	svcErr := serviceError{Status: status, Message: msg, Err: err}
	if dbErr, ok := err.(dbError); ok {
		svcErr.Err = errors.Join(errors.New(dbErr.Message), dbErr.InternalErr)
	}
	return svcErr
}

func newBadRequestServiceError(err error, msg string) serviceError {
	return newServiceError(err, http.StatusBadRequest, msg)
}

func newResponseServiceError(err error) serviceError {
	return newServiceError(err, http.StatusInternalServerError, "error writing response")
}

func newJSONReqBodyServiceError(err error) serviceError {
	return newServiceError(err, http.StatusBadRequest, "error reading json request")
}

func newInvalidIdServiceError(err error, idName string) serviceError {
	return newServiceError(err, http.StatusBadRequest, fmt.Sprintf("invalid %s", idName))
}

// newDatabaseServiceError answers with a dbError's status and message, and
// with a 500 and the error's text for any other error.
func newDatabaseServiceError(err error) serviceError {
	if dbErr, ok := err.(dbError); ok {
		return serviceError{Status: dbErr.Status, Message: dbErr.Message, Err: dbErr.InternalErr}
	}
	return serviceError{Status: http.StatusInternalServerError, Message: err.Error(), Err: err}
}

// dbError is a failed query, with the status and message to answer with.
type dbError struct {
	Status      int
	Message     string
	InternalErr error
}

func (e dbError) Error() string {
	return fmt.Sprintf("db error: %s", e.InternalErr.Error())
}

func newDBError(err error, msg string) dbError {
	dbErr := dbError{Message: msg, InternalErr: err}
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound),
		errors.Is(err, gorm.ErrInvalidData),
		errors.Is(err, gorm.ErrEmptySlice),
		errors.Is(err, gorm.ErrInvalidField),
		errors.Is(err, gorm.ErrInvalidValueOfLength):
		dbErr.Status = http.StatusBadRequest
	case errors.Is(err, gorm.ErrPrimaryKeyRequired),
		errors.Is(err, gorm.ErrForeignKeyViolated),
		errors.Is(err, gorm.ErrDuplicatedKey),
		errors.Is(err, gorm.ErrCheckConstraintViolated):
		dbErr.Status = http.StatusUnprocessableEntity
	default:
		dbErr.Status = http.StatusInternalServerError
	}
	return dbErr
}

func newGetRecordsDBError(err error, table string) dbError {
	return newDBError(err, fmt.Sprintf("error getting %s", table))
}

func newDeleteDBError(err error, table string) dbError {
	return newDBError(err, fmt.Sprintf("error deleting %s", table))
}

func newCreateDBError(err error, table string) dbError {
	return newDBError(err, fmt.Sprintf("error creating %s", table))
}

func newUpdateDBError(err error, table string) dbError {
	return newDBError(err, fmt.Sprintf("error updating %s", table))
}

// claimsFrom is the caller of a request. Core only hands a module requests
// it has authenticated, so the claims are always there.
func claimsFrom(r *http.Request) *identity.Claims {
	c, _ := identity.FromContext(r.Context())
	return c
}
