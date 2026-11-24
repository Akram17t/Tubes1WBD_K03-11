package migration

import (
	"errors"
	"net/http"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// RegisterWorkflowRoutes dipanggil setelah middleware Session terpasang.
func RegisterWorkflowRoutes(router gin.IRouter, service WorkflowService, csrf gin.HandlerFunc) error {
	if service.DB == nil || csrf == nil {
		return ErrInvalidInput
	}
	h := workflowHandler{service: service}
	routes := router.Group("/api/databases/:database_id/requests", auth.RequireLogin(), csrf)
	routes.GET("", h.list)
	routes.POST("", h.create)
	routes.GET("/:request_id", h.get)
	for _, action := range []string{"submit", "approve", "cancel"} {
		routes.POST("/:request_id/"+action, h.action(action))
	}
	routes.POST("/:request_id/execute", h.execute)
	return nil
}

type workflowHandler struct{ service WorkflowService }

// workflowIDs membaca ID positif dari URL dan menolak parameter yang tidak valid.
func workflowIDs(c *gin.Context) (int64, int64, bool) {
	databaseID, err := api.ParseID(c.Param("database_id"))
	if err != nil {
		workflowRespondError(c, ErrInvalidInput)
		return 0, 0, false
	}
	var requestID int64
	if raw := c.Param("request_id"); raw != "" {
		requestID, err = api.ParseID(raw)
		if err != nil {
			workflowRespondError(c, ErrInvalidInput)
			return 0, 0, false
		}
	}
	return databaseID, requestID, true
}

// create membuat draft dari referensi migration tanpa menerima SQL dari client.
func (h workflowHandler) create(c *gin.Context) {
	databaseID, _, ok := workflowIDs(c)
	if !ok {
		return
	}
	var input RequestInput
	if err := api.Decode(c, &input, false); err != nil {
		workflowRespondError(c, err)
		return
	}
	user, _ := auth.CurrentUser(c)
	request, err := h.service.Create(c.Request.Context(), user, databaseID, input)
	workflowRespond(c, http.StatusCreated, request, err)
}

// get mengembalikan detail pengajuan yang dimiliki database pada URL.
func (h workflowHandler) get(c *gin.Context) {
	databaseID, requestID, ok := workflowIDs(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	request, err := h.service.Get(c.Request.Context(), user, databaseID, requestID)
	workflowRespond(c, http.StatusOK, request, err)
}

// list memvalidasi pagination sebelum mengembalikan daftar pengajuan yang dapat dibaca aktor.
func (h workflowHandler) list(c *gin.Context) {
	databaseID, _, ok := workflowIDs(c)
	if !ok {
		return
	}
	page, ok := api.ParsePage(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	requests, hasMore, err := h.service.List(c.Request.Context(), user, databaseID, page.Limit, page.Offset)
	if err != nil {
		workflowRespondError(c, err)
		return
	}
	api.List(c, requests, page, hasMore)
}

// execute menerima referensi URL saja; SQL dan konfigurasi berasal dari snapshot approved.
func (h workflowHandler) execute(c *gin.Context) {
	databaseID, requestID, ok := workflowIDs(c)
	if !ok {
		return
	}
	if err := api.Decode(c, new(struct{}), true); err != nil {
		workflowRespondError(c, err)
		return
	}
	user, _ := auth.CurrentUser(c)
	job, err := h.service.Execute(c.Request.Context(), user, databaseID, requestID)
	workflowRespond(c, http.StatusAccepted, job, err)
}

// action menjalankan submit, approve, atau cancel dengan body kosong tanpa SQL tambahan.
func (h workflowHandler) action(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		databaseID, requestID, ok := workflowIDs(c)
		if !ok {
			return
		}
		if err := api.Decode(c, new(struct{}), true); err != nil {
			workflowRespondError(c, err)
			return
		}
		user, _ := auth.CurrentUser(c)
		var data any
		var err error
		switch action {
		case "submit":
			data, err = h.service.Submit(c.Request.Context(), user, databaseID, requestID)
		case "approve":
			data, err = h.service.Approve(c.Request.Context(), user, databaseID, requestID)
		case "cancel":
			data, err = h.service.Cancel(c.Request.Context(), user, databaseID, requestID)
		}
		workflowRespond(c, http.StatusOK, data, err)
	}
}

// workflowRespond membungkus hasil service dengan metadata request HTTP.
func workflowRespond(c *gin.Context, status int, data any, err error) {
	if err != nil {
		workflowRespondError(c, err)
		return
	}
	api.JSON(c, status, data)
}

// workflowError menghentikan request dengan format error JSON yang konsisten.
func workflowError(c *gin.Context, status int, code, message string) {
	api.Fail(c, status, code, message, nil)
}

// workflowRespondError memetakan error workflow tanpa membocorkan pesan SQL atau koneksi.
func workflowRespondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		workflowError(c, http.StatusForbidden, "forbidden", "Akses ditolak.")
	case errors.Is(err, ErrInvalidInput), errors.Is(err, api.ErrInvalidInput):
		workflowError(c, http.StatusBadRequest, "invalid_input", ErrInvalidInput.Error())
	case errors.Is(err, ErrNotFound):
		workflowError(c, http.StatusNotFound, "not_found", "Pengajuan atau migration tidak ditemukan.")
	case errors.Is(err, ErrApprovalIncomplete):
		workflowError(c, http.StatusConflict, "approval_incomplete", ErrApprovalIncomplete.Error())
	case errors.Is(err, ErrConflict):
		workflowError(c, http.StatusConflict, "state_conflict", "Status atau revisi pengajuan tidak sesuai.")
	case errors.Is(err, ErrWorkflowUnavailable):
		workflowError(c, http.StatusServiceUnavailable, "workflow_unavailable", ErrWorkflowUnavailable.Error())
	default:
		workflowError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal.")
	}
}
