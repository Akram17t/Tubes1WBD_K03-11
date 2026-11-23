package main

import (
	"strings"
	"testing"
	"time"
)

// siapkan env minimum yang valid; test mengubah satu variabel lalu memeriksa hasilnya
func validEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PLATFORM_DATABASE_URL", "postgres://u:p@localhost:5432/db_test")
	t.Setenv("CONNECTION_ENCRYPTION_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
}

func TestWorkerConfigDefaults(t *testing.T) {
	validEnv(t)
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.AppRole != RoleAPI || config.WorkerConcurrency != 2 ||
		config.WorkerPollInterval != time.Second || config.WorkerHeartbeatInterval != 5*time.Second ||
		config.JobStaleAfter != 30*time.Second || config.BatchSizeDefault != 1000 || config.BatchSizeMax != 10000 {
		t.Fatalf("default worker config salah: %+v", config)
	}
}

func TestWorkerConfigOverrides(t *testing.T) {
	validEnv(t)
	t.Setenv("APP_ROLE", "worker")
	t.Setenv("WORKER_CONCURRENCY", "4")
	t.Setenv("JOB_STALE_AFTER", "1m")
	config, err := LoadConfig()
	if err != nil || config.AppRole != RoleWorker || config.WorkerConcurrency != 4 || config.JobStaleAfter != time.Minute {
		t.Fatalf("override worker config: %+v %v", config, err)
	}
}

func TestWorkerConfigRejectsInvalid(t *testing.T) {
	tests := []struct {
		name, key, value, message string
	}{
		{"role tidak dikenal", "APP_ROLE", "scheduler", "APP_ROLE"},
		{"concurrency nol", "WORKER_CONCURRENCY", "0", "WORKER_CONCURRENCY"},
		{"concurrency negatif", "WORKER_CONCURRENCY", "-1", "WORKER_CONCURRENCY"},
		{"concurrency bukan angka", "WORKER_CONCURRENCY", "dua", "WORKER_CONCURRENCY"},
		{"poll nol", "WORKER_POLL_INTERVAL", "0s", "WORKER_POLL_INTERVAL"},
		{"stale tidak melebihi heartbeat", "JOB_STALE_AFTER", "5s", "JOB_STALE_AFTER"},
		{"batch default melebihi maksimum", "BATCH_SIZE_DEFAULT", "20000", "BATCH_SIZE_DEFAULT"},
		{"batch maksimum nol", "BATCH_SIZE_MAX", "0", "BATCH_SIZE_MAX"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validEnv(t)
			t.Setenv(test.key, test.value)
			_, err := LoadConfig()
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("seharusnya ditolak dengan pesan %q, dapat %v", test.message, err)
			}
		})
	}
}
