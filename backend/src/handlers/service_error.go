package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
)

type serviceError struct {
	Status  int
	Message string
	Err     error
}

// Can pass a DBError into this, but will only be on rare circumstances used to override status and message
func NewServiceError(err error, status int, msg string) serviceError {
	svcErr := serviceError{}
	if dbErr, ok := err.(database.DBError); ok {
		svcErr.Err = errors.Join(errors.New(dbErr.Message), dbErr.InternalErr)
	} else {
		svcErr.Err = err
	}
	svcErr.Status = status
	svcErr.Message = msg
	return svcErr
}

// The Error method below is used for implementation purposes only so that the serviceError can be used as an error type. The Error() function is not normally called on the serviceError type.
func (svcErr serviceError) Error() string {
	return fmt.Sprintf("service error: %s", svcErr.Err.Error())
}

func newBadRequestServiceError(err error, msg string) serviceError { //default is BadRequest
	return NewServiceError(err, http.StatusBadRequest, msg)
}

func newResponseServiceError(err error) serviceError {
	return NewServiceError(err, http.StatusInternalServerError, "error writing response")
}

func newJSONReqBodyServiceError(err error) serviceError {
	return NewServiceError(err, http.StatusBadRequest, "error reading json request")
}

func newUnauthorizedServiceError() serviceError {
	return NewServiceError(errors.New("user unauthorized"), http.StatusUnauthorized, "Unauthorized")
}

func newInvalidIdServiceError(err error, idName string) serviceError {
	return NewServiceError(err, http.StatusBadRequest, fmt.Sprintf("invalid %s", idName))
}

func newInvalidQueryParamServiceError(err error, paramName string) serviceError {
	return NewServiceError(err, http.StatusBadRequest, fmt.Sprintf("invalid %s parameter", paramName))
}

func newCreateRequestServiceError(err error) serviceError {
	return NewServiceError(err, http.StatusInternalServerError, "error creating request")
}

func newMarshallingBodyServiceError(err error) serviceError {
	return NewServiceError(err, http.StatusInternalServerError, "error marshalling body")
}

func newInternalServerServiceError(err error, msg string) serviceError {
	return NewServiceError(err, http.StatusInternalServerError, msg)
}

func newDatabaseServiceError(err error) serviceError {
	svcErr := serviceError{}
	if dbErr, ok := err.(database.DBError); ok {
		svcErr.Message = dbErr.Message
		svcErr.Status = dbErr.Status
		svcErr.Err = dbErr.InternalErr
	} else {
		svcErr.Message = err.Error()
		svcErr.Status = http.StatusInternalServerError
		svcErr.Err = err
	}
	return svcErr
}
