package main

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"

	"tubes-wbd/src/admin"
	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"
	"tubes-wbd/src/database"
	"tubes-wbd/src/migration"
	"tubes-wbd/src/telemetry"

	"github.com/gin-gonic/gin"
)

type Dependencies struct {
	Config            Config
	PlatformDB        *sql.DB
	TargetDB          database.TargetProvider
	TargetConnections *database.ConnectionService
}

func newRouter(deps Dependencies) (*gin.Engine, error) {
	if deps.PlatformDB == nil {
		return nil, errors.New("platform database is required")
	}
	if deps.TargetConnections == nil {
		return nil, errors.New("target connection service is required")
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
	router.UseRawPath = true
	// urutan penting: request ID dulu, lalu span, access log, baru recovery
	// biar panic tetap kecatat di log dan span dengan request ID yang sama
	router.Use(
		telemetry.RequestIDMiddleware(),
		telemetry.TraceMiddleware(),
		telemetry.AccessLogMiddleware(),
		api.Recovery(),
	)
	router.NoRoute(api.NoRoute())
	router.SetHTMLTemplate(pageTemplates)
	router.Static("/static", staticDir)

	auth.RegisterRoutes(router, deps.PlatformDB, auth.SessionConfig{
		IdleTimeout:      deps.Config.SessionIdleTimeout,
		AbsoluteTimeout:  deps.Config.SessionAbsoluteTimeout,
		RotationInterval: deps.Config.SessionRotationInterval,
		GracePeriod:      deps.Config.SessionGracePeriod,
		SecureCookie:     deps.Config.AppEnv == "production",
	})
	database.RegisterRoutes(router, deps.PlatformDB, deps.TargetDB)
	database.RegisterAdminRoutes(router, deps.TargetConnections)
	migration.RegisterRoutes(router, deps.PlatformDB, deps.TargetDB)
	admin.RegisterRoutes(router, deps.PlatformDB)
	return router, nil
}
