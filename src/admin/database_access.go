package admin

import (
	"errors"
	"net/http"
)

// databaseAccessHTTPError memetakan error grant menjadi status HTTP dan pesan yang aman.
func databaseAccessHTTPError(err error) (int, string) {
	switch {
	case errors.Is(err, errInvalidDatabaseID), errors.Is(err, errInvalidAccessLevel):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, errAccessDatabaseNotFound), errors.Is(err, errDatabaseAccessNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, errAccessDatabaseInactive):
		return http.StatusConflict, err.Error()
	default:
		return groupHTTPError(err)
	}
}
