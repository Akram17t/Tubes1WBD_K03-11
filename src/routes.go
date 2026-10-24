package main

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"tubes-wbd/src/admin"
	"tubes-wbd/src/auth"
	"tubes-wbd/src/database"
	"tubes-wbd/src/migration"

	"github.com/gin-gonic/gin"
)

type Dependencies struct {
	Config     Config
	PlatformDB *sql.DB
}

func newRouter(deps Dependencies) (*gin.Engine, error) {
	if deps.PlatformDB == nil {
		return nil, errors.New("platform database is required")
	}

	pageTemplates, err := template.New("").ParseGlob(filepath.Join("templates", "*.html"))
	if err != nil {
		return nil, fmt.Errorf("load templates: %w", err)
	}
	staticDir := "static"
	info, err := os.Stat(staticDir)
	if err != nil {
		return nil, fmt.Errorf("open static directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("static path is not a directory")
	}

	if deps.Config.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.Use(
		gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
			path := c.FullPath()
			if path == "" {
				path = "<unmatched>"
			}
			log.Printf("recovered panic: %s %s", c.Request.Method, path)
			c.AbortWithStatus(http.StatusInternalServerError)
		}),
		requestLogger(),
	)
	router.SetHTMLTemplate(pageTemplates)
	router.Static("/static", staticDir)

	auth.RegisterRoutes(router, deps.PlatformDB, auth.SessionConfig{
		IdleTimeout:      deps.Config.SessionIdleTimeout,
		AbsoluteTimeout:  deps.Config.SessionAbsoluteTimeout,
		RotationInterval: deps.Config.SessionRotationInterval,
		GracePeriod:      deps.Config.SessionGracePeriod,
		SecureCookie:     deps.Config.AppEnv == "production",
	})
	database.RegisterRoutes(router, deps.PlatformDB)
	migration.RegisterRoutes(router, deps.PlatformDB)
	admin.RegisterRoutes(router, deps.PlatformDB)
	return router, nil
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		path := c.FullPath()
		if path == "" {
			path = "<unmatched>"
		}
		log.Printf("%s %s %d %s", c.Request.Method, path, c.Writer.Status(), time.Since(started))
	}
}
