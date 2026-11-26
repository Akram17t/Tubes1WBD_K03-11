package migration

import (
	"context"
	"database/sql"
	"errors"

	"tubes-wbd/src/telemetry"

	"github.com/jackc/pgx/v5/pgconn"
)

// JobTask adalah pekerjaan yang sudah diklaim, dikunci, dan direvalidasi. SQL berasal dari snapshot
// request yang disetujui, bukan dari input browser.
type JobTask struct {
	JobID             int64
	RequestID         int64
	DatabaseID        int64
	MigrationID       int64
	Revision          int
	Operation         string
	Attempt           int
	ActorID           int64
	TargetExecutionID *int64
	SourceJobID       *int64
	Snapshot          DefinitionSnapshot
	Progress          JobProgress
	WorkerID          string
	// WorkerToken memagari update progres: sertakan pada setiap UPDATE migration_jobs.
	WorkerToken string
	// Target adalah koneksi khusus yang memegang advisory lock; seluruh SQL target harus lewat sini.
	Target *sql.Conn
}

// JobResult adalah hasil satu attempt. Status hanya boleh success, failed, partial,
// partially_reverted, atau outcome_unknown.
type JobResult struct {
	Status       string
	ErrorCode    string
	ErrorMessage string
	// Retryable hanya untuk kegagalan sementara yang pasti aman diulang.
	Retryable bool
	// Record memfinalisasi history dalam transaction yang sama dengan status job.
	Record func(context.Context, *sql.Tx) error
}

// JobExecutor menjalankan SQL pada target dan menulis history.
//
// Execute wajib meng-commit row migration_executions berstatus running (job_id, attempt) sebelum
// mengirim SQL apa pun ke target. Recovery mengandalkan ini: tanpa row running berarti SQL belum
// pernah dikirim. Jika hasil commit tidak pasti, kembalikan outcome_unknown tanpa memfinalisasi row.
//
// Reconcile dipakai untuk attempt yang terputus. Ia hanya membaca bukti ledger target, tidak
// menjalankan SQL pengguna, dan mengembalikan outcome_unknown bila bukti belum dapat dibaca.
// task.Attempt berisi nomor attempt row yang masih running.
type JobExecutor interface {
	Execute(context.Context, *JobTask) JobResult
	Reconcile(context.Context, *JobTask) JobResult
}

// SQLSTATE yang menjamin transaction dibatalkan dan layak dicoba ulang.
var transientSQLStates = map[string]bool{
	"40001": true, // serialization_failure
	"40P01": true, // deadlock_detected
	"55P03": true, // lock_not_available
}

// ClassifyExecutionError memetakan error satu transaction target. commitSent berarti COMMIT sudah
// dikirim, sehingga hanya ledger target yang dapat membuktikan hasilnya.
func ClassifyExecutionError(err error, commitSent bool) JobResult {
	if commitSent {
		return JobResult{Status: JobOutcomeUnknown, ErrorCode: JobErrorOutcomeUnknown,
			ErrorMessage: "Hasil commit belum pasti dan akan diperiksa lewat ledger target."}
	}
	if pgError, ok := errors.AsType[*pgconn.PgError](err); ok {
		// Kelas 08 dan 57P adalah koneksi atau server yang terputus, bukan kesalahan SQL.
		class := pgError.Code[:min(len(pgError.Code), 2)]
		switch {
		case transientSQLStates[pgError.Code]:
			return JobResult{Status: JobFailed, ErrorCode: JobErrorTransient, ErrorMessage: telemetry.SafeError(err), Retryable: true}
		case class != "08" && pgError.Code != "57P01" && pgError.Code != "57P02" && pgError.Code != "57P03":
			return JobResult{Status: JobFailed, ErrorCode: JobErrorSQL, ErrorMessage: telemetry.SafeError(err)}
		}
	}
	// COMMIT belum dikirim, jadi transaction yang terputus pasti dibatalkan server.
	// Pesan driver tidak diteruskan karena dapat memuat alamat atau credential.
	return JobResult{Status: JobFailed, ErrorCode: JobErrorConnectionLost,
		ErrorMessage: "Koneksi ke database target terputus sebelum commit.", Retryable: true}
}
