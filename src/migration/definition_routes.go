package migration

import (
	"errors"
	"net/http"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// RegisterDefinitionRoutes memasang API definisi migration; dipanggil setelah middleware Session terpasang.
func RegisterDefinitionRoutes(router gin.IRouter, service DefinitionService, csrf gin.HandlerFunc) error {
	if service.DB == nil || csrf == nil {
		return ErrInvalidInput
	}
	h := definitionHandler{service: service}
	routes := router.Group("/api/databases/:database_id/migrations", auth.RequireLogin(), csrf)
	routes.GET("", h.list)
	routes.POST("", h.create)
	routes.GET("/:migration_id", h.get)
	routes.PATCH("/:migration_id", h.update)
	return nil
}

type definitionHandler struct{ service DefinitionService }

func (h definitionHandler) list(c *gin.Context) {
	databaseID, ok := api.PathID(c, "database_id")
	if !ok {
		return
	}
	page, ok := api.ParsePage(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	definitions, hasMore, err := h.service.List(c.Request.Context(), user, databaseID, page.Limit, page.Offset)
	if err != nil {
		definitionRespondError(c, err)
		return
	}
	api.List(c, definitions, page, hasMore)
}

func (h definitionHandler) get(c *gin.Context) {
	databaseID, migrationID, ok := definitionIDs(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	definition, err := h.service.Get(c.Request.Context(), user, databaseID, migrationID)
	definitionRespond(c, http.StatusOK, definition, err)
}

func (h definitionHandler) create(c *gin.Context) {
	databaseID, ok := api.PathID(c, "database_id")
	if !ok {
		return
	}
	var input DefinitionInput
	if !api.BindJSON(c, &input) {
		return
	}
	user, _ := auth.CurrentUser(c)
	definition, err := h.service.Create(c.Request.Context(), user, databaseID, input)
	definitionRespond(c, http.StatusCreated, definition, err)
}

// update menimpa field yang dikirim saja: body di-decode di atas isi definisi saat ini.
// Bila definisi berubah di antara pembacaan dan penyimpanan, pemeriksaan revision di Update menolaknya.
func (h definitionHandler) update(c *gin.Context) {
	databaseID, migrationID, ok := definitionIDs(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	current, err := h.service.Get(c.Request.Context(), user, databaseID, migrationID)
	if err != nil {
		definitionRespondError(c, err)
		return
	}
	input, err := current.input()
	if err != nil {
		definitionRespondError(c, err)
		return
	}
	patch := struct {
		Revision int `json:"revision"`
		DefinitionInput
	}{DefinitionInput: input}
	if !api.BindJSON(c, &patch) {
		return
	}
	definition, err := h.service.Update(c.Request.Context(), user, databaseID, migrationID, patch.Revision, patch.DefinitionInput)
	definitionRespond(c, http.StatusOK, definition, err)
}

func definitionIDs(c *gin.Context) (int64, int64, bool) {
	databaseID, ok := api.PathID(c, "database_id")
	if !ok {
		return 0, 0, false
	}
	migrationID, ok := api.PathID(c, "migration_id")
	return databaseID, migrationID, ok
}

func definitionRespond(c *gin.Context, status int, data any, err error) {
	if err != nil {
		definitionRespondError(c, err)
		return
	}
	api.JSON(c, status, data)
}

// definitionRespondError meneruskan error definisi apa adanya; error lain mengikuti pemetaan workflow.
// Error tak dikenal dicatat lewat api.Internal tanpa dikirim ke client.
func definitionRespondError(c *gin.Context, err error) {
	if definitionErr, ok := errors.AsType[*DefinitionError](err); ok {
		api.Fail(c, definitionErr.Status, definitionErr.Code, definitionErr.Message, definitionErr.Details)
		return
	}
	switch {
	case errors.Is(err, auth.ErrForbidden):
		api.Fail(c, http.StatusForbidden, api.CodeForbidden, "Akses ditolak.", nil)
	case errors.Is(err, ErrInvalidInput):
		api.Fail(c, http.StatusBadRequest, api.CodeInvalidInput, ErrInvalidInput.Error(), nil)
	case errors.Is(err, ErrNotFound):
		api.Fail(c, http.StatusNotFound, api.CodeNotFound, ErrNotFound.Error(), nil)
	default:
		api.Internal(c, err)
	}
}
