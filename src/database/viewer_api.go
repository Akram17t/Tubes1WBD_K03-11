package database

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"
	"tubes-wbd/src/httpapi"
	"tubes-wbd/src/telemetry"

	"github.com/gin-gonic/gin"
)

func registerViewerAPIRoutes(router gin.IRouter, db *sql.DB, targets TargetProvider) {
	h := viewerHandler{db: db, targets: targets}
	r := router.Group("/api/databases", auth.RequireLogin())
	r.GET("", h.listAPI)
	r.GET("/:database_id", h.getAPI)
	for _, resource := range []string{"schemas", "tables", "table", "data"} {
		r.GET("/:database_id/"+resource, h.catalogAPI(resource))
	}
}

func (h viewerHandler) listAPI(c *gin.Context) {
	page, ok := api.ParsePage(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	databases, err := getDatabasesPage(c.Request.Context(), h.db, user, page.Limit+1, page.Offset)
	if err != nil {
		databaseAPIError(c, err)
		return
	}
	hasMore := len(databases) > page.Limit
	if hasMore {
		databases = databases[:page.Limit]
	}
	api.List(c, databases, page, hasMore)
}

func (h viewerHandler) databaseAPI(c *gin.Context) (*Database, bool) {
	id, ok := api.PathID(c, "database_id")
	if !ok {
		return nil, false
	}
	user, _ := auth.CurrentUser(c)
	db, err := GetDatabase(c.Request.Context(), h.db, user, id)
	if err != nil {
		databaseAPIError(c, err)
		return nil, false
	}
	return db, true
}

func (h viewerHandler) getAPI(c *gin.Context) {
	if db, ok := h.databaseAPI(c); ok {
		api.JSON(c, http.StatusOK, db)
	}
}

func (h viewerHandler) catalogAPI(resource string) gin.HandlerFunc {
	return func(c *gin.Context) {
		db, ok := h.databaseAPI(c)
		if !ok {
			return
		}
		page, ok := api.ParsePage(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		target, err := TargetConnection(ctx, h.db, h.targets, db.ID)
		if err != nil {
			databaseAPIError(c, err)
			return
		}
		schema, table := c.DefaultQuery("schema", "public"), c.Query("table")
		var data any
		var names []string
		switch resource {
		case "schemas":
			names, err = GetSchemas(ctx, target)
		case "tables":
			names, err = GetTables(ctx, target, schema)
		case "table":
			var columns []Column
			var constraints []Constraint
			var indexes []Index
			columns, err = GetColumns(ctx, target, schema, table)
			if err == nil {
				constraints, err = GetConstraints(ctx, target, schema, table)
			}
			if err == nil {
				indexes, err = GetIndexes(ctx, target, schema, table)
			}
			data = gin.H{"columns": columns, "constraints": constraints, "indexes": indexes}
		case "data":
			data, err = GetTableData(ctx, target, schema, table, page.Limit, page.Offset)
		}
		if err != nil {
			databaseAPIError(c, err)
			return
		}
		if resource == "schemas" || resource == "tables" {
			start := min(page.Offset, len(names))
			end := start + min(page.Limit, len(names)-start)
			api.List(c, names[start:end], page, end < len(names))
			return
		}
		api.JSON(c, http.StatusOK, data)
	}
}

func databaseAPIRespond(c *gin.Context, status int, data any, err error) {
	if err != nil {
		databaseAPIError(c, err)
		return
	}
	api.JSON(c, status, data)
}

func databaseAPIError(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, api.CodeInternal, "Operasi database tidak dapat diproses."
	switch {
	case errors.Is(err, auth.ErrForbidden):
		status, code, message = http.StatusForbidden, api.CodeForbidden, "Akses ditolak."
	case errors.Is(err, httpapi.ErrInvalidInput), errors.Is(err, ErrInvalidConnection),
		errors.Is(err, ErrInvalidIdentifier), errors.Is(err, ErrInvalidPagination):
		status, code, message = http.StatusBadRequest, api.CodeInvalidInput, "Input JSON atau parameter tidak valid."
	case errors.Is(err, ErrConnectionNotFound), errors.Is(err, ErrDatabaseNotFound),
		errors.Is(err, ErrSchemaNotFound), errors.Is(err, ErrTableNotFound):
		status, code, message = http.StatusNotFound, api.CodeNotFound, "Resource tidak ditemukan."
	case errors.Is(err, ErrConnectionNameExists), errors.Is(err, ErrConnectionTargetExists), errors.Is(err, ErrConnectionInactive):
		status, code, message = http.StatusConflict, api.CodeStateConflict, err.Error()
	case errors.Is(err, ErrTargetUnavailable), errors.Is(err, ErrTargetTimeout):
		status, code, message = http.StatusServiceUnavailable, api.CodeTargetUnavailable, "Koneksi target tidak tersedia."
	default:
		// Pesan driver bisa mengandung credential; cukup simpan kode SQLSTATE.
		slog.ErrorContext(c.Request.Context(), "database.api_error", "sqlstate", telemetry.SQLState(err))
	}
	api.Fail(c, status, code, message, nil)
}
