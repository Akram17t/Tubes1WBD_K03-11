package migration

import (
	"net/http"
	"strconv"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// history memvalidasi filter sebelum membaca timeline database yang boleh diakses user.
func (h handlers) history(c *gin.Context) {
	databaseID, ok := api.PathID(c, "database_id")
	if !ok {
		return
	}
	page, ok := api.ParsePage(c)
	if !ok {
		return
	}
	hideRollback, err := strconv.ParseBool(c.DefaultQuery("hide_rollback", "false"))
	if err != nil {
		api.Fail(c, http.StatusBadRequest, api.CodeInvalidInput, "hide_rollback harus berupa boolean.", nil)
		return
	}
	user, _ := auth.CurrentUser(c)
	executions, hasMore, err := GetHistory(c.Request.Context(), h.db, user, databaseID, hideRollback, page.Limit, page.Offset)
	if err != nil {
		workflowRespondError(c, err)
		return
	}
	data := make([]ExecutionResponse, 0, len(executions))
	for _, execution := range executions {
		data = append(data, executionResponse(databaseID, execution))
	}
	api.List(c, data, page, hasMore)
}

// executionDetail membatasi execution ke database URL dan memaginasi batch agar tidak memuat seluruh scope.
func (h handlers) executionDetail(c *gin.Context) {
	databaseID, ok := api.PathID(c, "database_id")
	if !ok {
		return
	}
	executionID, ok := api.PathID(c, "execution_id")
	if !ok {
		return
	}
	page, ok := api.ParsePage(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	detail, err := GetExecutionDetail(c.Request.Context(), h.db, user, databaseID, executionID, page.Limit, page.Offset)
	workflowRespond(c, http.StatusOK, detail, err)
}
