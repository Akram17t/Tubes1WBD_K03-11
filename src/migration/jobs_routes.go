package migration

import (
	"net/http"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// RegisterJobRoutes dipanggil setelah middleware Session terpasang.
func RegisterJobRoutes(router gin.IRouter, service JobService, csrf gin.HandlerFunc) error {
	if service.DB == nil || csrf == nil {
		return ErrInvalidInput
	}
	routes := router.Group("/api/databases/:database_id/jobs", auth.RequireLogin(), csrf)
	routes.GET("/:job_id", func(c *gin.Context) {
		databaseID, jobID, ok := jobIDs(c)
		if !ok {
			return
		}
		user, _ := auth.CurrentUser(c)
		job, err := service.Get(c.Request.Context(), user, databaseID, jobID)
		jobRespond(c, http.StatusOK, job, err)
	})
	routes.POST("/:job_id/resume", func(c *gin.Context) {
		databaseID, jobID, ok := jobIDs(c)
		if !ok {
			return
		}
		// Body harus kosong: resume tidak menerima SQL atau konfigurasi.
		if err := api.Decode(c, new(struct{}), true); err != nil {
			workflowRespondError(c, err)
			return
		}
		user, _ := auth.CurrentUser(c)
		job, err := service.Resume(c.Request.Context(), user, databaseID, jobID)
		jobRespond(c, http.StatusAccepted, job, err)
	})
	return nil
}

func jobIDs(c *gin.Context) (int64, int64, bool) {
	databaseID, ok := api.PathID(c, "database_id")
	if !ok {
		return 0, 0, false
	}
	jobID, ok := api.PathID(c, "job_id")
	return databaseID, jobID, ok
}

func jobRespond(c *gin.Context, status int, job *JobDetail, err error) {
	if err != nil {
		workflowRespondError(c, err)
		return
	}
	api.JSON(c, status, job)
}
