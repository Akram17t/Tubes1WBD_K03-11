package migration

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"tubes-wbd/src/database"
)

// Batas waktu pemeriksaan catalog agar target lambat tidak menahan transaction platform terlalu lama.
const catalogTimeout = 10 * time.Second

// loadPosition membaca event sukses yang sudah direkonsiliasi ke platform. Baris M1 yang dibuat
// setelah upgrade belum punya history_sequence sehingga memakai version migration sebagai urutan.
func loadPosition(ctx context.Context, tx *sql.Tx, databaseID int64) (databasePosition, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT e.id, e.migration_id, e.operation, e.target_execution_id
		FROM migration_executions e JOIN migrations m ON m.id = e.migration_id
		WHERE e.database_id = $1 AND e.status = 'success'
		ORDER BY COALESCE(e.history_sequence, m.version), e.finished_at, e.id`, databaseID)
	if err != nil {
		return databasePosition{}, err
	}
	defer rows.Close()
	events := []successEvent{}
	for rows.Next() {
		var event successEvent
		if err := rows.Scan(&event.ExecutionID, &event.MigrationID, &event.Operation, &event.TargetExecutionID); err != nil {
			return databasePosition{}, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return databasePosition{}, err
	}
	return buildPosition(events)
}

// loadBlockers mengambil job dengan efek belum tuntas, termasuk yang sedang antre retry setelah
// sebagian batch committed. excludeJobID dipakai worker agar job yang sedang ia kerjakan tidak menahan dirinya.
func loadBlockers(ctx context.Context, tx *sql.Tx, databaseID, excludeJobID int64) ([]jobBlocker, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, status FROM migration_jobs
		WHERE database_id = $1 AND id <> $2
			AND (status IN ('partial', 'partially_reverted', 'outcome_unknown')
				OR (status IN ('queued', 'running') AND CASE WHEN operation IN ('rollback', 'compensate')
					THEN reverted_batches > 0 ELSE committed_batches > reverted_batches END))
		ORDER BY id`, databaseID, excludeJobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blockers := []jobBlocker{}
	for rows.Next() {
		var blocker jobBlocker
		if err := rows.Scan(&blocker.JobID, &blocker.Status); err != nil {
			return nil, err
		}
		blockers = append(blockers, blocker)
	}
	return blockers, rows.Err()
}

// Validate memenuhi RequestValidator. Dipanggil saat membuat, submit, dan enqueue pengajuan dengan
// transaction milik caller yang sudah mengunci migration. Tidak ada SQL pengguna yang dieksekusi.
func (s DefinitionService) Validate(ctx context.Context, tx *sql.Tx, databaseID int64, input RequestInput) (DefinitionSnapshot, error) {
	return s.validateOperation(ctx, tx, databaseID, input, 0)
}

// validateOperation juga dipakai worker setelah memperoleh lock, dengan ownJobID job yang dikerjakan.
func (s DefinitionService) validateOperation(ctx context.Context, tx *sql.Tx, databaseID int64, input RequestInput, ownJobID int64) (DefinitionSnapshot, error) {
	var empty DefinitionSnapshot
	if databaseID < 1 || !validRequestInput(input) {
		return empty, invalidField("", "Referensi operasi migration tidak valid.")
	}
	definition, err := loadDefinition(ctx, tx, databaseID, input.MigrationID)
	if err != nil {
		return empty, err
	}
	if definition.Revision != input.Revision {
		return empty, stateConflict("Revisi definisi sudah berubah.")
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT is_active FROM database_connections WHERE id = $1`, databaseID).Scan(&active); err != nil {
		return empty, err
	}
	if !active {
		return empty, stateConflict("Koneksi database tidak aktif.")
	}
	if definition.Irreversible && (input.Operation == "rollback" || input.Operation == "compensate") {
		reason := ""
		if definition.IrreversibleReason != nil {
			reason = *definition.IrreversibleReason
		}
		return empty, notReversible(reason)
	}
	// Definisi tersimpan diperiksa ulang karena bisa berasal dari data M1 atau jalur lama.
	stored, err := definition.input()
	if err != nil {
		return empty, err
	}
	// Hasil normalisasi hanya dipakai untuk pemeriksaan; snapshot tetap berasal dari nilai tersimpan.
	normalized, err := normalizeDefinition(stored, s.BatchSizeDefault, s.BatchSizeMax)
	if err != nil {
		return empty, err
	}
	if err := checkOperation(ctx, tx, databaseID, input, ownJobID); err != nil {
		return empty, err
	}
	if normalized.Mode == ModeBatch {
		if err := s.checkBatchCatalog(ctx, databaseID, *normalized.BatchConfig); err != nil {
			return empty, err
		}
	}
	return definition.snapshot(), nil
}

// checkOperation menerapkan aturan linear dan blocker. Tidak ada rebase diam-diam: operasi yang
// targetnya sudah bukan puncak stack ditolak.
func checkOperation(ctx context.Context, tx *sql.Tx, databaseID int64, input RequestInput, ownJobID int64) error {
	blockers, err := loadBlockers(ctx, tx, databaseID, ownJobID)
	if err != nil {
		return err
	}
	for _, blocker := range blockers {
		// Hanya kompensasi atas job penahan itu sendiri yang boleh lewat.
		if input.Operation == "compensate" && input.SourceJobID != nil && blocker.JobID == *input.SourceJobID {
			continue
		}
		return databaseBlocked(blocker)
	}
	if input.Operation == "compensate" {
		return checkCompensation(ctx, tx, databaseID, input, ownJobID)
	}
	position, err := loadPosition(ctx, tx, databaseID)
	if errors.Is(err, errInconsistentHistory) {
		return stateConflict(errInconsistentHistory.Error())
	}
	if err != nil {
		return err
	}
	switch input.Operation {
	case "up":
		if position.of(input.MigrationID) != PositionNeverApplied {
			return stateConflict("Up hanya untuk migration yang belum pernah berhasil diterapkan. Gunakan forward atau buat migration baru.")
		}
	case "rollback":
		top, ok := position.activeTop()
		if !ok || input.TargetExecutionID == nil || top.ExecutionID != *input.TargetExecutionID || top.MigrationID != input.MigrationID {
			return stateConflict("Rollback hanya untuk execution aktif yang terakhir diterapkan.")
		}
		return checkExecutionRevision(ctx, tx, top.ExecutionID, input.Revision)
	case "forward":
		next, ok := position.forwardTop()
		if !ok || input.TargetExecutionID == nil || next.ExecutionID != *input.TargetExecutionID || next.MigrationID != input.MigrationID {
			return stateConflict("Forward hanya untuk migration yang paling akhir di-rollback dan belum digantikan.")
		}
		return checkExecutionRevision(ctx, tx, next.ExecutionID, input.Revision)
	}
	return nil
}

// checkExecutionRevision memastikan rollback/forward memakai revisi yang sama dengan execution targetnya.
func checkExecutionRevision(ctx context.Context, tx *sql.Tx, executionID int64, revision int) error {
	var applied int
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM migration_executions WHERE id = $1`, executionID).Scan(&applied); err != nil {
		return err
	}
	if applied != revision {
		return stateConflict("Revisi pengajuan berbeda dengan revisi yang diterapkan.")
	}
	return nil
}

// checkCompensation memastikan source job masih partial dan belum punya job kompensasi lain.
func checkCompensation(ctx context.Context, tx *sql.Tx, databaseID int64, input RequestInput, ownJobID int64) error {
	if input.SourceJobID == nil {
		return invalidField("source_job_id", "source_job_id wajib diisi.")
	}
	var migrationID int64
	var revision int
	var operation, status string
	var compensationJobID *int64
	err := tx.QueryRowContext(ctx, `
		SELECT migration_id, revision, operation, status, compensation_job_id
		FROM migration_jobs WHERE id = $1 AND database_id = $2`, *input.SourceJobID, databaseID).
		Scan(&migrationID, &revision, &operation, &status, &compensationJobID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if migrationID != input.MigrationID || revision != input.Revision {
		return stateConflict("Source job tidak cocok dengan migration atau revisi pengajuan.")
	}
	if compensationJobID != nil && *compensationJobID != ownJobID {
		return compensationExists(*compensationJobID)
	}
	if status != "partial" || (operation != "up" && operation != "forward") {
		return stateConflict("Kompensasi hanya untuk job up atau forward yang berstatus partial.")
	}
	return nil
}

// checkBatchCatalog memastikan tabel ada dan key-nya BIGINT primary key tunggal pada database target.
func (s DefinitionService) checkBatchCatalog(ctx context.Context, databaseID int64, config BatchConfig) error {
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	target, err := database.TargetConnection(ctx, s.DB, s.Targets, databaseID)
	if err != nil {
		return targetUnavailable()
	}
	var columnType sql.NullString
	var primaryKey bool
	// Identifier dikirim sebagai parameter ke catalog, tidak dirangkai ke teks SQL.
	err = target.QueryRowContext(ctx, `
		SELECT pg_catalog.format_type(a.atttypid, NULL),
			EXISTS (SELECT 1 FROM pg_catalog.pg_index i
				WHERE i.indrelid = c.oid AND i.indisprimary AND i.indisvalid
					AND i.indnkeyatts = 1 AND i.indkey[0] = a.attnum)
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_catalog.pg_attribute a
			ON a.attrelid = c.oid AND a.attname = $3 AND a.attnum > 0 AND NOT a.attisdropped
		WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p')`,
		config.Schema, config.Table, config.KeyColumn).Scan(&columnType, &primaryKey)
	if errors.Is(err, sql.ErrNoRows) {
		return invalidField("batch_config.table", "Tabel batch tidak ditemukan pada database target.")
	}
	if err != nil {
		return targetUnavailable()
	}
	if !columnType.Valid {
		return invalidField("batch_config.key_column", "Kolom key tidak ditemukan pada tabel batch.")
	}
	if columnType.String != "bigint" || !primaryKey {
		return invalidField("batch_config.key_column", "Kolom key harus BIGINT dan menjadi primary key tunggal.")
	}
	return nil
}
