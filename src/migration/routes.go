package migration

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/database"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router gin.IRoutes, db *sql.DB, targets database.TargetProvider) {
	h := handlers{db: db, targets: targets}
	login := auth.RequireLogin()
	csrf := auth.CSRF()
	router.GET("/databases/:dbID/migrations", login, h.list)
	router.GET("/databases/:dbID/migrations/new", login, h.new)
	router.POST("/databases/:dbID/migrations", login, csrf, h.create)
	router.GET("/databases/:dbID/migrations/:migrationID", login, h.detail)
	router.GET("/databases/:dbID/migrations/:migrationID/edit", login, h.edit)
	router.POST("/databases/:dbID/migrations/:migrationID/update", login, csrf, h.update)
	router.POST("/databases/:dbID/migrations/:migrationID/execute", login, csrf, h.execute)
	router.GET("/databases/:dbID/migrations/:migrationID/executions/:executionID", login, h.execution)
}

type handlers struct {
	db      *sql.DB
	targets database.TargetProvider
}

func (h handlers) execute(c *gin.Context) {
	dbID, migrationID, ok := migrationIDs(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	user, _ := auth.CurrentUser(c)
	if err := auth.Authorize(ctx, h.db, user, auth.ExecuteMigration, dbID); err != nil {
		respondError(c, err)
		return
	}
	migration, err := GetMigration(ctx, h.db, user, dbID, migrationID)
	if err != nil {
		respondError(c, err)
		return
	}
	if migration.Status == "success" {
		respondError(c, ErrConflict)
		return
	}
	target, err := database.TargetConnection(ctx, h.db, h.targets, dbID)
	if err != nil {
		respondError(c, err)
		return
	}
	execution, err := ExecuteMigration(ctx, h.db, target, user, dbID, migrationID)
	if errors.Is(err, ErrOutcomeUnknown) {
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/databases/%d/migrations/%d", dbID, migrationID))
		return
	}
	if err != nil && !errors.Is(err, ErrExecutionFailed) {
		respondError(c, err)
		return
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/databases/%d/migrations/%d/executions/%d", dbID, migrationID, execution.ID))
}

func (h handlers) list(c *gin.Context) {
	dbID, _, ok := migrationIDs(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	migrations, err := GetMigrations(c.Request.Context(), h.db, user, dbID)
	if err != nil {
		respondError(c, err)
		return
	}
	editErr := auth.Authorize(c.Request.Context(), h.db, user, auth.EditMigration, dbID)
	if editErr != nil && !errors.Is(editErr, auth.ErrForbidden) {
		respondError(c, editErr)
		return
	}
	render(c, gin.H{"Page": "list", "DatabaseID": dbID, "Migrations": migrations, "CanEdit": editErr == nil})
}

func (h handlers) new(c *gin.Context) {
	dbID, _, ok := migrationIDs(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := auth.Authorize(c.Request.Context(), h.db, user, auth.CreateMigration, dbID); err != nil {
		respondError(c, err)
		return
	}
	render(c, gin.H{"Page": "new", "DatabaseID": dbID})
}

func (h handlers) create(c *gin.Context) {
	dbID, _, ok := migrationIDs(c)
	if !ok {
		return
	}
	if err := c.Request.ParseForm(); err != nil {
		respondError(c, ErrInvalidInput)
		return
	}
	user, _ := auth.CurrentUser(c)
	form := c.Request.PostForm
	migration, err := CreateMigration(c.Request.Context(), h.db, user, dbID, form.Get("title"), form.Get("description"), form.Get("sql_content"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/databases/%d/migrations/%d", dbID, migration.ID))
}

func (h handlers) detail(c *gin.Context) {
	dbID, migrationID, ok := migrationIDs(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	migration, err := GetMigration(c.Request.Context(), h.db, user, dbID, migrationID)
	if err != nil {
		respondError(c, err)
		return
	}
	executions, err := GetExecutions(c.Request.Context(), h.db, user, dbID, migrationID)
	if err != nil {
		respondError(c, err)
		return
	}
	editErr := auth.Authorize(c.Request.Context(), h.db, user, auth.EditMigration, dbID)
	if editErr != nil && !errors.Is(editErr, auth.ErrForbidden) {
		respondError(c, editErr)
		return
	}
	render(c, gin.H{"Page": "detail", "DatabaseID": dbID, "Migration": migration, "Executions": executions, "CanEdit": editErr == nil})
}

func (h handlers) edit(c *gin.Context) {
	dbID, migrationID, ok := migrationIDs(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := auth.Authorize(c.Request.Context(), h.db, user, auth.EditMigration, dbID); err != nil {
		respondError(c, err)
		return
	}
	migration, err := GetMigration(c.Request.Context(), h.db, user, dbID, migrationID)
	if err != nil {
		respondError(c, err)
		return
	}
	if migration.Status != "draft" && migration.Status != "failed" {
		respondError(c, ErrConflict)
		return
	}
	render(c, gin.H{"Page": "edit", "DatabaseID": dbID, "Migration": migration})
}

func (h handlers) update(c *gin.Context) {
	dbID, migrationID, ok := migrationIDs(c)
	if !ok {
		return
	}
	if err := c.Request.ParseForm(); err != nil {
		respondError(c, ErrInvalidInput)
		return
	}
	user, _ := auth.CurrentUser(c)
	form := c.Request.PostForm
	migration, err := UpdateMigration(c.Request.Context(), h.db, user, dbID, migrationID, form.Get("title"), form.Get("description"), form.Get("sql_content"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/databases/%d/migrations/%d", dbID, migration.ID))
}

func (h handlers) execution(c *gin.Context) {
	dbID, migrationID, ok := migrationIDs(c)
	if !ok {
		return
	}
	executionID, err := strconv.ParseInt(c.Param("executionID"), 10, 64)
	if err != nil || executionID < 1 {
		respondError(c, ErrInvalidInput)
		return
	}
	user, _ := auth.CurrentUser(c)
	execution, err := GetExecution(c.Request.Context(), h.db, user, dbID, migrationID, executionID)
	if err != nil {
		respondError(c, err)
		return
	}
	render(c, gin.H{"Page": "execution", "DatabaseID": dbID, "MigrationID": migrationID, "Execution": execution})
}

func migrationIDs(c *gin.Context) (int64, int64, bool) {
	dbID, err := strconv.ParseInt(c.Param("dbID"), 10, 64)
	if err != nil || dbID < 1 {
		respondError(c, ErrInvalidInput)
		return 0, 0, false
	}
	var migrationID int64
	if raw := c.Param("migrationID"); raw != "" {
		migrationID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || migrationID < 1 {
			respondError(c, ErrInvalidInput)
			return 0, 0, false
		}
	}
	return dbID, migrationID, true
}

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		c.String(http.StatusBadRequest, "%s", ErrInvalidInput)
	case errors.Is(err, auth.ErrForbidden):
		c.String(http.StatusForbidden, "Forbidden")
	case errors.Is(err, ErrNotFound):
		c.String(http.StatusNotFound, "%s", ErrNotFound)
	case errors.Is(err, ErrConflict):
		c.String(http.StatusConflict, "%s", ErrConflict)
	case errors.Is(err, database.ErrTargetUnavailable), errors.Is(err, ErrTargetUnavailable):
		c.String(http.StatusServiceUnavailable, "%s", database.ErrTargetUnavailable)
	default:
		c.String(http.StatusInternalServerError, "Terjadi kesalahan internal")
	}
}

func render(c *gin.Context, data gin.H) {
	data["User"], _ = auth.CurrentUser(c)
	data["CSRFToken"] = auth.CSRFToken(c)
	c.HTML(http.StatusOK, "migrations.html", data)
}
