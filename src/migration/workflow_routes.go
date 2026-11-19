package migration

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// RegisterWorkflowRoutes memasang API workflow setelah Reinsen menyiapkan session, CSRF JSON, dan request_id.
// Route ini belum dipasang pada router M1 selama dependensi integrasinya belum tersedia.
func RegisterWorkflowRoutes(router gin.IRouter, service WorkflowService, csrf gin.HandlerFunc) error {
	if service.DB == nil || csrf == nil {
		return ErrInvalidInput
	}
	h := workflowHandler{service: service}
	routes := router.Group("/api/databases/:database_id/requests", func(c *gin.Context) {
		if _, ok := auth.CurrentUser(c); !ok {
			workflowError(c, http.StatusUnauthorized, "unauthenticated", "Silakan login terlebih dahulu.")
			return
		}
		c.Next()
	}, csrf)
	routes.GET("", h.list)
	routes.POST("", h.create)
	routes.GET("/:request_id", h.get)
	for _, action := range []string{"submit", "approve", "cancel"} {
		routes.POST("/:request_id/"+action, h.action(action))
	}
	return nil
}

type workflowHandler struct{ service WorkflowService }

// workflowIDs membaca ID positif dari URL dan menolak parameter yang tidak valid.
func workflowIDs(c *gin.Context) (int64, int64, bool) {
	databaseID, err := strconv.ParseInt(c.Param("database_id"), 10, 64)
	if err != nil || databaseID < 1 {
		workflowRespondError(c, ErrInvalidInput)
		return 0, 0, false
	}
	var requestID int64
	if raw := c.Param("request_id"); raw != "" {
		requestID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || requestID < 1 {
			workflowRespondError(c, ErrInvalidInput)
			return 0, 0, false
		}
	}
	return databaseID, requestID, true
}

// workflowDecode membatasi ukuran JSON dan menolak field asing atau lebih dari satu payload.
func workflowDecode(c *gin.Context, target any, allowEmpty bool) error {
	if c.Request.ContentLength != 0 {
		contentType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || contentType != "application/json" {
			return ErrInvalidInput
		}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return ErrInvalidInput
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrInvalidInput
	}
	return nil
}

// create membuat draft dari referensi migration tanpa menerima SQL dari client.
func (h workflowHandler) create(c *gin.Context) {
	databaseID, _, ok := workflowIDs(c)
	if !ok {
		return
	}
	var input RequestInput
	if err := workflowDecode(c, &input, false); err != nil {
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
	limit, errLimit := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, errOffset := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if errLimit != nil || errOffset != nil {
		workflowRespondError(c, ErrInvalidInput)
		return
	}
	user, _ := auth.CurrentUser(c)
	requests, hasMore, err := h.service.List(c.Request.Context(), user, databaseID, limit, offset)
	if err != nil {
		workflowRespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": requests, "meta": gin.H{
		"request_id": c.GetString("request_id"), "limit": limit, "offset": offset, "has_more": hasMore,
	}})
}

// action menjalankan submit, approve, atau cancel dengan body kosong tanpa SQL tambahan.
func (h workflowHandler) action(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		databaseID, requestID, ok := workflowIDs(c)
		if !ok {
			return
		}
		if err := workflowDecode(c, new(struct{}), true); err != nil {
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
	c.JSON(status, gin.H{"data": data, "meta": gin.H{"request_id": c.GetString("request_id")}})
}

// workflowError menghentikan request dengan format error JSON yang konsisten.
func workflowError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{"code": code, "message": message}, "meta": gin.H{"request_id": c.GetString("request_id")},
	})
}

// workflowRespondError memetakan error workflow tanpa membocorkan pesan SQL atau koneksi.
func workflowRespondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		workflowError(c, http.StatusForbidden, "forbidden", "Akses ditolak.")
	case errors.Is(err, ErrInvalidInput):
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
