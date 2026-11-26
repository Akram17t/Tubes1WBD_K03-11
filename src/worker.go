package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"tubes-wbd/src/database"
	"tubes-wbd/src/migration"
)

// newJobExecutor diganti mesin eksekusi (bagian E). Selama nil, worker menolak start agar tidak
// mengklaim job yang belum dapat dijalankan.
var newJobExecutor func(*sql.DB) migration.JobExecutor

// runWorker menjalankan role worker sampai proses diminta berhenti.
func runWorker(ctx context.Context, config Config, platformDB *sql.DB) error {
	if newJobExecutor == nil {
		return errors.New("mesin eksekusi migration belum tersedia; APP_ROLE=worker belum dapat dijalankan")
	}
	// Tiap job memegang satu koneksi platform khusus, ditambah heartbeat dan query pendukung.
	platformDB.SetMaxOpenConns(max(10, 3*config.WorkerConcurrency+4))

	connections, err := database.NewConnectionService(platformDB, config.ConnectionEncryptionKey)
	if err != nil {
		return err
	}
	defer connections.Close()
	worker, err := migration.NewWorker(migration.WorkerConfig{
		DB:      platformDB,
		Targets: connections.Target,
		Definitions: migration.DefinitionService{
			DB:               platformDB,
			Targets:          connections.Target,
			BatchSizeDefault: config.BatchSizeDefault,
			BatchSizeMax:     config.BatchSizeMax,
		},
		Executor:          newJobExecutor(platformDB),
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
