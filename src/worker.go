package main

import (
	"context"
	"database/sql"
	"fmt"

	"tubes-wbd/src/database"
	"tubes-wbd/src/migration"
	"tubes-wbd/src/telemetry"
)

// runWorker menjalankan role worker sampai proses diminta berhenti.
func runWorker(ctx context.Context, config Config, platformDB *sql.DB) error {
	// Tiap job memegang satu koneksi platform khusus, ditambah heartbeat, eksekutor, dan query pendukung.
	platformDB.SetMaxOpenConns(max(10, 3*config.WorkerConcurrency+4))

	connections, err := database.NewConnectionService(platformDB, config.ConnectionEncryptionKey)
	if err != nil {
		return err
	}
	defer connections.Close()
	if config.MetricsAddr != "" {
		telemetry.RegisterPoolMetrics(platformDB, connections.PoolStats)
		if err := telemetry.ServeMetrics(ctx, config.MetricsAddr); err != nil {
			return err
		}
	}
	worker, err := migration.NewWorker(migration.WorkerConfig{
		DB:      platformDB,
		Targets: connections.Target,
		Definitions: migration.DefinitionService{
			DB:               platformDB,
			Targets:          connections.Target,
			BatchSizeDefault: config.BatchSizeDefault,
			BatchSizeMax:     config.BatchSizeMax,
		},
		Executor:          migration.NewExecutor(platformDB),
		Concurrency:       config.WorkerConcurrency,
		PollInterval:      config.WorkerPollInterval,
		HeartbeatInterval: config.WorkerHeartbeatInterval,
		StaleAfter:        config.JobStaleAfter,
	})
	if err != nil {
		return fmt.Errorf("build worker: %w", err)
	}
	worker.Run(ctx)
	return nil
}
