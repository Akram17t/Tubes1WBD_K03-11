package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"
	"tubes-wbd/src/database"
)

// DefinitionService menyediakan CRUD definisi migration dan validator yang dipakai workflow.
type DefinitionService struct {
	DB *sql.DB
	// Targets dipakai untuk memeriksa konfigurasi batch pada catalog database target.
	Targets          database.TargetProvider
	BatchSizeDefault int
	BatchSizeMax     int
}

// Definition adalah detail satu migration beserta posisi dan aksi yang dihitung backend.
type Definition struct {
	ID                 int64           `json:"id,string"`
	DatabaseID         int64           `json:"database_id,string"`
	Revision           int             `json:"revision"`
	Title              string          `json:"title"`
	Description        *string         `json:"description"`
	SQLUp              string          `json:"sql_up"`
	SQLDown            *string         `json:"sql_down"`
	Irreversible       bool            `json:"irreversible"`
	IrreversibleReason *string         `json:"irreversible_reason"`
	Mode               string          `json:"mode"`
	BatchConfig        json.RawMessage `json:"batch_config"`
	CreatedBy          int64           `json:"created_by,string"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	LockedAt           *time.Time      `json:"locked_at"`
	Position           string          `json:"position"`
	// Kedua ID ini hanya terisi bila migration ini kandidat rollback atau forward berikutnya.
	ActiveExecutionID  *int64   `json:"active_execution_id,string"`
	ForwardExecutionID *int64   `json:"forward_execution_id,string"`
	AllowedActions     []string `json:"allowed_actions"`

	hasActiveReview bool
}

const definitionColumns = `m.id, m.database_id, m.revision, m.title, m.description, m.sql_content,
	m.sql_down, m.irreversible, m.irreversible_reason, m.mode, m.batch_config, m.created_by,
	m.created_at, m.updated_at, m.locked_at,
	EXISTS (SELECT 1 FROM migration_requests r WHERE r.migration_id = m.id
		AND r.review_status IN ('pending_review', 'approved'))`

func scanDefinition(row interface{ Scan(...any) error }) (*Definition, error) {
	var definition Definition
	var batchConfig []byte
	err := row.Scan(&definition.ID, &definition.DatabaseID, &definition.Revision, &definition.Title,
		&definition.Description, &definition.SQLUp, &definition.SQLDown, &definition.Irreversible,
		&definition.IrreversibleReason, &definition.Mode, &batchConfig, &definition.CreatedBy,
		&definition.CreatedAt, &definition.UpdatedAt, &definition.LockedAt, &definition.hasActiveReview)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if batchConfig != nil {
		definition.BatchConfig = json.RawMessage(batchConfig)
	}
	definition.CreatedAt = definition.CreatedAt.UTC()
	definition.UpdatedAt = definition.UpdatedAt.UTC()
	if definition.LockedAt != nil {
		utc := definition.LockedAt.UTC()
		definition.LockedAt = &utc
	}
	definition.AllowedActions = []string{}
	return &definition, nil
}

// loadDefinition membaca satu definisi yang harus dimiliki database pada URL.
func loadDefinition(ctx context.Context, db workflowQueryer, databaseID, migrationID int64) (*Definition, error) {
	return scanDefinition(db.QueryRowContext(ctx, `SELECT `+definitionColumns+`
		FROM migrations m WHERE m.database_id = $1 AND m.id = $2`, databaseID, migrationID))
}

func (d *Definition) input() (DefinitionInput, error) {
	input := DefinitionInput{
		Title: d.Title, Description: d.Description, SQLUp: d.SQLUp, SQLDown: d.SQLDown,
		Irreversible: d.Irreversible, IrreversibleReason: d.IrreversibleReason, Mode: d.Mode,
	}
	if d.BatchConfig != nil {
		input.BatchConfig = &BatchConfig{}
		if err := json.Unmarshal(d.BatchConfig, input.BatchConfig); err != nil {
			return input, err
		}
	}
	return input, nil
}

func (d *Definition) snapshot() DefinitionSnapshot {
	return DefinitionSnapshot{
		MigrationID: d.ID, DatabaseID: d.DatabaseID, Revision: d.Revision, Title: d.Title,
		Description: d.Description, SQLUp: d.SQLUp, SQLDown: d.SQLDown, Irreversible: d.Irreversible,
		IrreversibleReason: d.IrreversibleReason, Mode: d.Mode, BatchConfig: d.BatchConfig,
	}
}

// encodeBatchConfig menghasilkan NULL SQL pada mode normal.
func encodeBatchConfig(config *BatchConfig) (any, error) {
	if config == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	return string(encoded), nil
}

// decorate mengisi posisi dan allowed_actions. Aksi hanya petunjuk tampilan; mutasi diperiksa ulang.
func decorate(definition *Definition, position databasePosition, blockers []jobBlocker, level auth.Level) {
	definition.Position = position.of(definition.ID)
	activeTop, hasActive := position.activeTop()
	isActiveTop := hasActive && activeTop.MigrationID == definition.ID
	if isActiveTop {
		definition.ActiveExecutionID = &activeTop.ExecutionID
	}
	forwardTop, hasForward := position.forwardTop()
	isForwardTop := hasForward && forwardTop.MigrationID == definition.ID
	if isForwardTop {
		definition.ForwardExecutionID = &forwardTop.ExecutionID
	}
	if level != auth.LevelEdit {
		return
	}
	if definition.LockedAt == nil && !definition.hasActiveReview {
		definition.AllowedActions = append(definition.AllowedActions, "edit")
	}
	if len(blockers) != 0 {
		return
	}
	if definition.Position == PositionNeverApplied && (definition.Irreversible || definition.SQLDown != nil) {
		definition.AllowedActions = append(definition.AllowedActions, "request_up")
	}
	if isActiveTop && !definition.Irreversible {
		definition.AllowedActions = append(definition.AllowedActions, "request_rollback")
	}
	if isForwardTop {
		definition.AllowedActions = append(definition.AllowedActions, "request_forward")
	}
}

// databaseState membaca posisi, blocker, dan level actor sekali untuk dipakai banyak definisi.
func databaseState(ctx context.Context, tx *sql.Tx, user *auth.User, databaseID int64) (databasePosition, []jobBlocker, auth.Level, error) {
	position, err := loadPosition(ctx, tx, databaseID)
	if err != nil {
		return position, nil, auth.LevelNone, err
	}
	blockers, err := loadBlockers(ctx, tx, databaseID, 0)
	if err != nil {
		return position, nil, auth.LevelNone, err
	}
	level, err := auth.EffectiveLevel(ctx, tx, user.ID, databaseID)
	return position, blockers, level, err
}

// List mengembalikan satu halaman definisi database beserta tanda adanya halaman berikutnya.
func (s DefinitionService) List(ctx context.Context, user *auth.User, databaseID int64, limit, offset int) ([]Definition, bool, error) {
	if databaseID < 1 || limit < 1 || limit > api.MaxLimit || offset < 0 {
		return nil, false, ErrInvalidInput
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if err := workflowAuthorize(ctx, tx, user, auth.ViewMigration, databaseID); err != nil {
		return nil, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+definitionColumns+`
		FROM migrations m WHERE m.database_id = $1
		ORDER BY m.created_at DESC, m.id DESC LIMIT $2 OFFSET $3`, databaseID, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	definitions := []Definition{}
	for rows.Next() {
		definition, err := scanDefinition(rows)
		if err != nil {
			return nil, false, err
		}
		definitions = append(definitions, *definition)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	rows.Close()
	hasMore := len(definitions) > limit
	if hasMore {
		definitions = definitions[:limit]
	}
	position, blockers, level, err := databaseState(ctx, tx, user, databaseID)
	if err != nil {
		return nil, false, err
	}
	for i := range definitions {
		decorate(&definitions[i], position, blockers, level)
	}
	return definitions, hasMore, tx.Commit()
}

// Get mengembalikan detail definisi dari satu snapshot database yang konsisten.
func (s DefinitionService) Get(ctx context.Context, user *auth.User, databaseID, migrationID int64) (*Definition, error) {
	if databaseID < 1 || migrationID < 1 {
		return nil, ErrInvalidInput
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := workflowAuthorize(ctx, tx, user, auth.ViewMigration, databaseID); err != nil {
		return nil, err
	}
	definition, err := loadDefinition(ctx, tx, databaseID, migrationID)
	if err != nil {
		return nil, err
	}
	position, blockers, level, err := databaseState(ctx, tx, user, databaseID)
	if err != nil {
		return nil, err
	}
	decorate(definition, position, blockers, level)
	return definition, tx.Commit()
}

// Create menyimpan draft definisi baru. SQL hanya di-parse, tidak dijalankan.
func (s DefinitionService) Create(ctx context.Context, user *auth.User, databaseID int64, input DefinitionInput) (*Definition, error) {
	if databaseID < 1 {
		return nil, ErrInvalidInput
	}
	if err := workflowAuthorize(ctx, s.DB, user, auth.CreateMigration, databaseID); err != nil {
		return nil, err
	}
	input, err := normalizeDefinition(input, s.BatchSizeDefault, s.BatchSizeMax)
	if err != nil {
		return nil, err
	}
	batchConfig, err := encodeBatchConfig(input.BatchConfig)
	if err != nil {
		return nil, err
	}
	var id int64
	err = s.DB.QueryRowContext(ctx, `
		INSERT INTO migrations (database_id, title, description, sql_content, sql_down,
			irreversible, irreversible_reason, mode, batch_config, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10) RETURNING id`,
		databaseID, input.Title, input.Description, input.SQLUp, input.SQLDown,
		input.Irreversible, input.IrreversibleReason, input.Mode, batchConfig, user.ID).Scan(&id)
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "migration.definition.created", "actor_id", user.ID,
		"database_id", databaseID, "migration_id", id, "revision", 1, "status", "success")
	return s.Get(ctx, user, databaseID, id)
}

// Update mengganti isi definisi bila belum terkunci, tidak sedang direview, dan revisinya masih sama.
// Row dikunci FOR UPDATE, urutan yang sama dengan submit, sehingga approval tidak dapat menunjuk SQL yang berubah.
func (s DefinitionService) Update(ctx context.Context, user *auth.User, databaseID, migrationID int64, revision int, input DefinitionInput) (*Definition, error) {
	if databaseID < 1 || migrationID < 1 {
		return nil, ErrInvalidInput
	}
	if err := workflowAuthorize(ctx, s.DB, user, auth.EditMigration, databaseID); err != nil {
		return nil, err
	}
	if revision < 1 {
		return nil, invalidField("revision", "revision wajib diisi dengan revisi yang sedang diedit.")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockRequestMigration(ctx, tx, databaseID, migrationID); err != nil {
		return nil, err
	}
	current, err := loadDefinition(ctx, tx, databaseID, migrationID)
	if err != nil {
		return nil, err
	}
	if current.LockedAt != nil {
		return nil, immutableMigration()
	}
	active, err := HasActiveReview(ctx, tx, migrationID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, stateConflict("Definisi sedang direview. Tarik pengajuannya terlebih dahulu.")
	}
	if current.Revision != revision {
		return nil, stateConflict("Revisi definisi sudah berubah. Muat ulang sebelum mengedit.")
	}
	input, err = normalizeDefinition(input, s.BatchSizeDefault, s.BatchSizeMax)
	if err != nil {
		return nil, err
	}
	batchConfig, err := encodeBatchConfig(input.BatchConfig)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE migrations SET title = $2, description = $3, sql_content = $4, sql_down = $5,
			irreversible = $6, irreversible_reason = $7, mode = $8, batch_config = $9::jsonb,
			revision = revision + 1, updated_at = now()
		WHERE id = $1`,
		migrationID, input.Title, input.Description, input.SQLUp, input.SQLDown,
		input.Irreversible, input.IrreversibleReason, input.Mode, batchConfig); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "migration.definition.updated", "actor_id", user.ID,
		"database_id", databaseID, "migration_id", migrationID, "revision", revision+1, "status", "success")
	return s.Get(ctx, user, databaseID, migrationID)
}
