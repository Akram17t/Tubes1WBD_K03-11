package database

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router gin.IRoutes, db *sql.DB, targets TargetProvider) {
	router.GET("/databases", auth.RequireLogin(), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		databases, err := GetDatabases(c.Request.Context(), db, user)
		if err != nil {
			respondError(c, err)
			return
		}
		render(c, "databases.html", gin.H{"Databases": databases})
	})
	router.GET("/databases/:dbID", auth.RequireLogin(), func(c *gin.Context) {
		dbID, err := strconv.ParseInt(c.Param("dbID"), 10, 64)
		if err != nil || dbID < 1 {
			respondError(c, ErrInvalidDatabaseID)
			return
		}
		user, _ := auth.CurrentUser(c)
		database, err := GetDatabase(c.Request.Context(), db, user, dbID)
		if err != nil {
			respondError(c, err)
			return
		}
		render(c, "databases.html", gin.H{"Database": database})
	})
	h := viewerHandler{db: db, targets: targets}
	router.GET("/databases/:dbID/schema", auth.RequireLogin(), h.schema)
	router.GET("/databases/:dbID/tables/:table/data", auth.RequireLogin(), h.data)
}

type viewerHandler struct {
	db      *sql.DB
	targets TargetProvider
}

func (h viewerHandler) database(c *gin.Context) (*Database, bool) {
	id, err := strconv.ParseInt(c.Param("dbID"), 10, 64)
	if err != nil || id < 1 {
		respondError(c, ErrInvalidDatabaseID)
		return nil, false
	}
	user, _ := auth.CurrentUser(c)
	db, err := GetDatabase(c.Request.Context(), h.db, user, id)
	if err != nil {
		respondError(c, err)
		return nil, false
	}
	return db, true
}

func (h viewerHandler) schema(c *gin.Context) {
	db, ok := h.database(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	target, err := TargetConnection(ctx, h.db, h.targets, db.ID)
	if err != nil {
		respondError(c, err)
		return
	}
	schemas, err := GetSchemas(ctx, target)
	if err != nil {
		respondError(c, err)
		return
	}
	page := gin.H{"Page": "schema", "Database": db, "Schemas": schemas}
	if len(schemas) == 0 {
		render(c, "viewer.html", page)
		return
	}
	schema := c.Query("schema")
	if schema == "" {
		schema = schemas[0]
		if slices.Contains(schemas, "public") {
			schema = "public"
		}
	}
	tables, err := GetTables(ctx, target, schema)
	if err != nil {
		respondError(c, err)
		return
	}
	page["Schema"], page["Tables"] = schema, tables
	table := c.Query("table")
	if table == "" && len(tables) > 0 {
		table = tables[0]
	}
	if table != "" {
		columns, err := GetColumns(ctx, target, schema, table)
		if err != nil {
			respondError(c, err)
			return
		}
		constraints, err := GetConstraints(ctx, target, schema, table)
		if err != nil {
			respondError(c, err)
			return
		}
		indexes, err := GetIndexes(ctx, target, schema, table)
		if err != nil {
			respondError(c, err)
			return
		}
		page["Table"], page["Columns"], page["Constraints"], page["Indexes"] = table, columns, constraints, indexes
		page["DataURL"] = tableDataURL(db.ID, schema, table, 50, 0)
	}
	render(c, "viewer.html", page)
}

func (h viewerHandler) data(c *gin.Context) {
	db, ok := h.database(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()
	if err := auth.Authorize(ctx, h.db, user, auth.ViewData, db.ID); err != nil {
		respondError(c, err)
		return
	}
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, offsetErr := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limitErr != nil || offsetErr != nil || limit < 1 || limit > MaxPageSize || offset < 0 {
		respondError(c, ErrInvalidPagination)
		return
	}
	target, err := TargetConnection(ctx, h.db, h.targets, db.ID)
	if err != nil {
		respondError(c, err)
		return
	}
	schema, table := c.DefaultQuery("schema", "public"), c.Param("table")
	data, err := GetTableData(ctx, target, schema, table, limit, offset)
	if err != nil {
		respondError(c, err)
		return
	}
	page := gin.H{"Page": "data", "Database": db, "Schema": schema, "Table": table, "Data": data}
	if offset > 0 {
		page["PreviousURL"] = tableDataURL(db.ID, schema, table, limit, max(0, offset-limit))
	}
	if next := offset + limit; len(data.Rows) == limit && next > offset {
		page["NextURL"] = tableDataURL(db.ID, schema, table, limit, next)
	}
	render(c, "viewer.html", page)
}

func tableDataURL(id int64, schema, table string, limit, offset int) string {
	query := url.Values{"schema": {schema}, "limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	return fmt.Sprintf("/databases/%d/tables/%s/data?%s", id, url.PathEscape(table), query.Encode())
}

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidDatabaseID), errors.Is(err, ErrInvalidIdentifier), errors.Is(err, ErrInvalidPagination):
		c.String(http.StatusBadRequest, "%s", err)
	case errors.Is(err, auth.ErrForbidden):
		c.String(http.StatusForbidden, "Forbidden")
	case errors.Is(err, ErrDatabaseNotFound), errors.Is(err, ErrSchemaNotFound), errors.Is(err, ErrTableNotFound):
		c.String(http.StatusNotFound, "%s", err)
	case errors.Is(err, ErrTargetUnavailable):
		c.String(http.StatusServiceUnavailable, "%s", ErrTargetUnavailable)
	default:
		c.String(http.StatusInternalServerError, "Terjadi kesalahan internal")
	}
}

func render(c *gin.Context, name string, data gin.H) {
	data["User"], _ = auth.CurrentUser(c)
	data["CSRFToken"] = auth.CSRFToken(c)
	c.HTML(http.StatusOK, name, data)
}
