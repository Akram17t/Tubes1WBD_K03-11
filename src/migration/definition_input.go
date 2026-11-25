package migration

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"tubes-wbd/src/api"
)

const (
	ModeNormal = "normal"
	ModeBatch  = "batch"

	defaultBatchSize = 1000
	defaultBatchMax  = 10000
	maxTitleRunes    = 200
	// Batas identifier PostgreSQL (NAMEDATALEN - 1); nama lebih panjang dipotong diam-diam oleh server.
	maxIdentifierBytes = 63
)

// BatchConfig menentukan tabel dan key yang cakupan ID-nya dipilih platform saat eksekusi batch.
type BatchConfig struct {
	Schema    string `json:"schema"`
	Table     string `json:"table"`
	KeyColumn string `json:"key_column"`
	// Pointer membedakan "tidak dikirim" (pakai default) dari nilai nol yang harus ditolak.
	BatchSize *int `json:"batch_size"`
}

// DefinitionInput adalah field definisi yang boleh diisi client.
type DefinitionInput struct {
	Title              string       `json:"title"`
	Description        *string      `json:"description"`
	SQLUp              string       `json:"sql_up"`
	SQLDown            *string      `json:"sql_down"`
	Irreversible       bool         `json:"irreversible"`
	IrreversibleReason *string      `json:"irreversible_reason"`
	Mode               string       `json:"mode"`
	BatchConfig        *BatchConfig `json:"batch_config"`
}

// DefinitionError membawa status dan code kontrak API. Unwrap mempertahankan jenis error lama
// sehingga errors.Is(err, ErrInvalidInput / ErrConflict) tetap berlaku bagi pemanggil.
type DefinitionError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
	kind    error
}

func (e *DefinitionError) Error() string { return e.Message }

func (e *DefinitionError) Unwrap() error { return e.kind }

func fieldDetails(field string) map[string]any {
	if field == "" {
		return nil
	}
	return map[string]any{"field": field}
}

func invalidField(field, message string) error {
	return &DefinitionError{http.StatusBadRequest, api.CodeInvalidInput, message, fieldDetails(field), ErrInvalidInput}
}

func invalidSQL(field, message string) error {
	return &DefinitionError{http.StatusBadRequest, api.CodeInvalidSQL, message, fieldDetails(field), ErrInvalidInput}
}

func stateConflict(message string) error {
	return &DefinitionError{http.StatusConflict, api.CodeStateConflict, message, nil, ErrConflict}
}

func immutableMigration() error {
	return &DefinitionError{http.StatusConflict, api.CodeImmutableMigration,
		"Migration sudah terkunci dan tidak dapat diubah. Buat migration baru untuk perbaikan.", nil, ErrConflict}
}

func notReversible(reason string) error {
	return &DefinitionError{http.StatusConflict, api.CodeNotReversible,
		"Migration ini irreversible: " + reason, nil, ErrConflict}
}

func targetUnavailable() error {
	return &DefinitionError{http.StatusServiceUnavailable, api.CodeTargetUnavailable,
		ErrTargetUnavailable.Error(), nil, ErrTargetUnavailable}
}

// blankToNil menyamakan string kosong dengan null agar penyimpanan konsisten.
func blankToNil(value *string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return value
}

// validText menolak byte NUL dan UTF-8 rusak yang tidak dapat disimpan PostgreSQL.
func validText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

// normalizeDefinition memvalidasi definisi tanpa menyentuh database: kelengkapan field, aturan
// up/down/irreversible, mode, batas batch, dan sintaks SQL. SQL tidak pernah dieksekusi di sini.
func normalizeDefinition(input DefinitionInput, batchDefault, batchMax int) (DefinitionInput, error) {
	if batchDefault < 1 {
		batchDefault = defaultBatchSize
	}
	if batchMax < 1 {
		batchMax = defaultBatchMax
	}

	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || utf8.RuneCountInString(input.Title) > maxTitleRunes || !validText(input.Title) {
		return input, invalidField("title", "Judul wajib diisi dan maksimal 200 karakter.")
	}
	input.Description = blankToNil(input.Description)
	if input.Description != nil && !validText(*input.Description) {
		return input, invalidField("description", "Deskripsi mengandung karakter yang tidak didukung.")
	}
	if strings.TrimSpace(input.SQLUp) == "" || !validText(input.SQLUp) {
		return input, invalidField("sql_up", "SQL up wajib diisi.")
	}
	input.SQLDown = blankToNil(input.SQLDown)
	if input.SQLDown != nil && !validText(*input.SQLDown) {
		return input, invalidField("sql_down", "SQL down mengandung karakter yang tidak didukung.")
	}

	if input.Irreversible {
		input.IrreversibleReason = blankToNil(input.IrreversibleReason)
		if input.IrreversibleReason == nil || !validText(*input.IrreversibleReason) {
			return input, invalidField("irreversible_reason", "Alasan wajib diisi untuk migration irreversible.")
		}
	} else {
		input.IrreversibleReason = nil
		if input.SQLDown == nil {
			return input, invalidField("sql_down", "SQL down wajib diisi, atau tandai migration irreversible beserta alasannya.")
		}
	}

	if input.Mode == "" {
		input.Mode = ModeNormal
	}
	switch input.Mode {
	case ModeNormal:
		if input.BatchConfig != nil {
			return input, invalidField("batch_config", "batch_config hanya dipakai pada mode batch.")
		}
	case ModeBatch:
		config, err := normalizeBatchConfig(input.BatchConfig, batchDefault, batchMax)
		if err != nil {
			return input, err
		}
		input.BatchConfig = config
	default:
		return input, invalidField("mode", "Mode harus normal atau batch.")
	}

	statements := []struct {
		field string
		query *string
	}{{"sql_up", &input.SQLUp}, {"sql_down", input.SQLDown}}
	for _, statement := range statements {
		if statement.query == nil {
			continue
		}
		var err error
		if input.Mode == ModeBatch {
			err = checkBatchSQL(statement.field, *statement.query, *input.BatchConfig)
		} else {
			err = checkNormalSQL(statement.field, *statement.query)
		}
		if err != nil {
			return input, err
		}
	}
	return input, nil
}

// normalizeBatchConfig menyalin konfigurasi agar default tidak mengubah objek milik pemanggil.
func normalizeBatchConfig(config *BatchConfig, batchDefault, batchMax int) (*BatchConfig, error) {
	if config == nil {
		return nil, invalidField("batch_config", "Mode batch membutuhkan batch_config.")
	}
	result := *config
	names := []struct{ field, value string }{
		{"batch_config.schema", result.Schema},
		{"batch_config.table", result.Table},
		{"batch_config.key_column", result.KeyColumn},
	}
	for _, name := range names {
		if name.value == "" || len(name.value) > maxIdentifierBytes || !validText(name.value) {
			return nil, invalidField(name.field, "Nama schema, tabel, dan kolom key wajib diisi (maksimal 63 byte).")
		}
	}
	if result.Schema == "information_schema" || strings.HasPrefix(result.Schema, "pg_") ||
		mentionsInternalMetadata(result.Schema) || mentionsInternalMetadata(result.Table) {
		return nil, invalidField("batch_config.table", "Tabel sistem dan metadata internal tidak boleh dipakai.")
	}
	size := batchDefault
	if result.BatchSize != nil {
		size = *result.BatchSize
	}
	if size < 1 || size > batchMax {
		return nil, invalidField("batch_config.batch_size", "Ukuran batch harus positif dan tidak melebihi batas maksimum.")
	}
	result.BatchSize = &size
	return &result, nil
}
