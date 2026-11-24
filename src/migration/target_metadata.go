package migration

import (
	"context"
	"database/sql"
)

// Metadata internal di database target. Ledger ditulis dalam transaction yang sama dengan perubahan
// data pengguna, sehingga bukti commit tidak bergantung pada database platform.
const (
	LedgerTable     = "_dbplatform_migrations"
	BatchLedger     = "_dbplatform_batch_ledger"
	BatchScopeTable = "_dbplatform_batch_scope"
)

// Kunci advisory transaction-level agar dua proses tidak membuat tabel yang sama bersamaan.
const bootstrapLockKey = 773102512

type txBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// Pernyataan idempotent; urutannya penting karena ledger batch tidak bergantung pada tabel lain.
var targetMetadataStatements = []string{
	// Ledger eksekusi dikembangkan dari tabel M1. Kolom M1 tetap; kolom M2 nullable agar baris lama valid.
	`CREATE TABLE IF NOT EXISTS public._dbplatform_migrations (
		execution_id BIGINT PRIMARY KEY,
		migration_id BIGINT NOT NULL,
		version BIGINT NOT NULL UNIQUE CHECK (version > 0),
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`ALTER TABLE public._dbplatform_migrations
		ADD COLUMN IF NOT EXISTS job_id BIGINT,
		ADD COLUMN IF NOT EXISTS revision INTEGER,
		ADD COLUMN IF NOT EXISTS operation VARCHAR(20) NOT NULL DEFAULT 'up',
		ADD COLUMN IF NOT EXISTS target_execution_id BIGINT`,
	// Satu rollback sukses hanya boleh membatalkan satu execution.
	`CREATE UNIQUE INDEX IF NOT EXISTS _dbplatform_migrations_target_key
		ON public._dbplatform_migrations (target_execution_id) WHERE target_execution_id IS NOT NULL`,

	// Satu baris per batch committed. origin_job_id adalah job yang efeknya dikerjakan: sama dengan job_id
	// untuk up, dan job sumber untuk down. Dengan begitu satu batch asli tidak dapat dikompensasi dua kali.
	`CREATE TABLE IF NOT EXISTS public._dbplatform_batch_ledger (
		job_id BIGINT NOT NULL,
		origin_job_id BIGINT NOT NULL,
		execution_id BIGINT NOT NULL,
		direction VARCHAR(4) NOT NULL CHECK (direction IN ('up', 'down')),
		batch_number INTEGER NOT NULL CHECK (batch_number > 0),
		first_id BIGINT NOT NULL,
		last_id BIGINT NOT NULL,
		row_count BIGINT NOT NULL CHECK (row_count >= 0),
		cursor_after BIGINT,
		committed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (origin_job_id, direction, batch_number),
		CHECK (first_id <= last_id)
	)`,
	`CREATE INDEX IF NOT EXISTS _dbplatform_batch_ledger_job_idx
		ON public._dbplatform_batch_ledger (job_id, direction, batch_number)`,

	// Daftar ID yang dibekukan saat job batch pertama kali mulai. batch_number diisi saat batch dikerjakan
	// sehingga cakupan batch committed dapat dibaca ulang setelah restart tanpa menghitung ulang dari tabel sumber.
	`CREATE TABLE IF NOT EXISTS public._dbplatform_batch_scope (
		job_id BIGINT NOT NULL,
		id BIGINT NOT NULL,
		batch_number INTEGER CHECK (batch_number > 0),
		PRIMARY KEY (job_id, id)
	)`,
	`CREATE INDEX IF NOT EXISTS _dbplatform_batch_scope_batch_idx
		ON public._dbplatform_batch_scope (job_id, batch_number) WHERE batch_number IS NOT NULL`,
}

// EnsureTargetMetadata membuat atau melengkapi metadata internal pada database target.
// Aman dipanggil berulang dan dari beberapa proses. Dipakai dengan *sql.Conn yang sama dengan eksekutor
// atau dengan *sql.DB; transaction dikelola di sini.
func EnsureTargetMetadata(ctx context.Context, target txBeginner) error {
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_catalog.pg_advisory_xact_lock($1)`, bootstrapLockKey); err != nil {
		return err
	}
	for _, statement := range targetMetadataStatements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}
