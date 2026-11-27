package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/database"
	"tubes-wbd/src/migration"
	"tubes-wbd/src/platformdb"
	"tubes-wbd/src/telemetry"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	config, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	// log JSON ke stdout, span console ke stderr biar stream log tetap rapi
	serviceName := "tubes-wbd-" + config.AppRole
	logExport, shutdownLogs, err := telemetry.SetupLogs(serviceName, config.LogExporter, config.LogLevel)
	if err != nil {
		return err
	}
	defer flushTelemetry("logs", shutdownLogs)
	if logExport != nil {
		slog.SetDefault(telemetry.NewLogger(os.Stdout, config.LogLevel, logExport))
	} else {
		slog.SetDefault(telemetry.NewLogger(os.Stdout, config.LogLevel))
	}
	shutdownTracing, err := telemetry.SetupTracing(serviceName, config.TraceExporter, os.Stderr)
	if err != nil {
		return err
	}
	defer flushTelemetry("tracing", shutdownTracing)

	platformDB, err := platformdb.Connect(ctx, config.PlatformDatabaseURL)
	if err != nil {
		return err
	}
	defer platformDB.Close()

	// Role worker tidak membuat admin awal dan tidak membuka HTTP listener.
	if config.AppRole == RoleWorker {
		return runWorker(ctx, config, platformDB)
	}

	if err := auth.EnsureAdmin(ctx, platformDB, config.AdminUsername, config.AdminPassword); err != nil {
		return fmt.Errorf("seed platform admin: %w", err)
	}

	connections, err := database.NewConnectionService(platformDB, config.ConnectionEncryptionKey)
	if err != nil {
		return err
	}
	defer connections.Close()
	if config.MetricsAddr != "" {
		// status job cuma diekspor API biar ga dobel kalau worker-nya lebih dari satu
		telemetry.RegisterPoolMetrics(platformDB, connections.PoolStats)
		telemetry.RegisterJobStatusMetrics(platformDB)
		if err := telemetry.ServeMetrics(ctx, config.MetricsAddr); err != nil {
			return err
		}
	}
	deps := Dependencies{Config: config, PlatformDB: platformDB, TargetDB: connections.Target, TargetConnections: connections}
	recoveryCtx, cancelRecovery := context.WithTimeout(ctx, 10*time.Second)
	if err := migration.RecoverRunning(recoveryCtx, platformDB, deps.TargetDB); err != nil {
		slog.Warn("migration.recovery_incomplete", "error", telemetry.SafeError(err))
	}
	cancelRecovery()
	router, err := newRouter(deps)
	if err != nil {
		return fmt.Errorf("build router: %w", err)
	}
	server := &http.Server{
		Addr:              config.AppAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("http.server_started", "addr", config.AppAddr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		return nil
	}
}

// flush sisa span/log sebelum proses keluar, dibatasi 5 detik biar shutdown ga nyangkut
func flushTelemetry(name string, shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		slog.Error(name+".shutdown_failed", "error", err.Error())
	}
}
