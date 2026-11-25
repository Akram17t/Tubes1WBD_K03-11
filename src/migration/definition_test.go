package migration

import (
	"errors"
	"reflect"
	"testing"

	"tubes-wbd/src/api"
)

func text(value string) *string { return &value }

func expectDefinitionError(t *testing.T, err error, code, field string) {
	t.Helper()
	definitionErr, ok := errors.AsType[*DefinitionError](err)
	if !ok || definitionErr.Code != code {
		t.Fatalf("seharusnya error %s, dapat %v", code, err)
	}
	if field != "" && definitionErr.Details["field"] != field {
		t.Fatalf("field seharusnya %s, dapat %v (%s)", field, definitionErr.Details["field"], definitionErr.Message)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error input harus tetap cocok dengan ErrInvalidInput: %v", err)
	}
}

func TestNormalSQLAcceptsTransactionalStatements(t *testing.T) {
	valid := []string{
		`CREATE TABLE public.a (id bigint PRIMARY KEY, note text)`,
		`ALTER TABLE public.a ADD COLUMN extra int; CREATE INDEX a_extra_idx ON public.a (extra)`,
		// titik koma dan kata COMMIT di dalam literal atau body function bukan statement
		`INSERT INTO public.a (note) VALUES ('halo; COMMIT; ROLLBACK')`,
		`CREATE FUNCTION public.f() RETURNS int LANGUAGE sql AS $$ SELECT 1; $$`,
		`WITH moved AS (DELETE FROM public.a RETURNING *) INSERT INTO public.b SELECT * FROM moved`,
		`SET LOCAL lock_timeout = '2s'; UPDATE public.a SET note = 'x' WHERE id = 1`,
		`DROP TABLE public.a; DROP SCHEMA reporting CASCADE; REINDEX TABLE public.b; GRANT SELECT ON public.b TO PUBLIC`,
		"-- komentar\nSELECT 1;",
	}
	for _, query := range valid {
		if err := checkNormalSQL("sql_up", query); err != nil {
			t.Errorf("SQL valid ditolak: %q: %v", query, err)
		}
	}
}

func TestNormalSQLRejectsInvalidAndUnsafe(t *testing.T) {
	invalid := map[string]string{
		"sintaks rusak":               `CREATE TABL public.a (id int)`,
		"hanya komentar":              `-- kosong`,
		"hanya titik koma":            `;`,
		"begin":                       `BEGIN; SELECT 1`,
		"commit di tengah":            `CREATE TABLE a(id int); COMMIT; SELECT 1`,
		"rollback":                    `ROLLBACK`,
		"savepoint":                   `SAVEPOINT s`,
		"end":                         `END`,
		"set transaction":             `SET TRANSACTION READ ONLY`,
		"set role":                    `SET ROLE postgres`,
		"session authorization":       `SET SESSION AUTHORIZATION postgres`,
		"reset all":                   `RESET ALL`,
		"discard":                     `DISCARD ALL`,
		"create role":                 `CREATE ROLE hacker`,
		"alter role":                  `ALTER ROLE hacker SUPERUSER`,
		"grant role":                  `GRANT admin TO hacker`,
		"create database":             `CREATE DATABASE other`,
		"alter system":                `ALTER SYSTEM SET work_mem = '1GB'`,
		"do block":                    `DO $$ BEGIN COMMIT; END $$`,
		"call":                        `CALL public.proc()`,
		"copy":                        `COPY public.a TO '/tmp/a'`,
		"vacuum":                      `VACUUM public.a`,
		"index concurrently":          `CREATE INDEX CONCURRENTLY i ON public.a (id)`,
		"drop index concurrently":     `DROP INDEX CONCURRENTLY i`,
		"reindex database":            `REINDEX DATABASE app`,
		"unlock executor":             `SELECT pg_advisory_unlock_all()`,
		"set_config":                  `SELECT set_config('role', 'postgres', false)`,
		"fungsi tersembunyi di cte":   `WITH x AS (SELECT pg_catalog.pg_terminate_backend(1)) SELECT * FROM x`,
		"drop ledger":                 `DROP TABLE public._dbplatform_migrations`,
		"ubah ledger lewat quote":     `DELETE FROM public."_dbplatform_batch_ledger"`,
		"ledger lewat regclass":       `SELECT 'public._dbplatform_batch_scope'::regclass`,
		"ledger dalam body function":  `CREATE FUNCTION public.f() RETURNS void LANGUAGE sql AS $$ DELETE FROM _dbplatform_migrations $$`,
		"drop schema public":          `DROP SCHEMA public CASCADE`,
		"rename schema public":        `ALTER SCHEMA public RENAME TO old_public`,
		"parameter pada mode normal":  `UPDATE public.a SET note = $1`,
		"commit tersembunyi di akhir": "SELECT ';';\nCOMMIT",
	}
	for name, query := range invalid {
		t.Run(name, func(t *testing.T) {
			expectDefinitionError(t, checkNormalSQL("sql_up", query), api.CodeInvalidSQL, "sql_up")
		})
	}
}

func TestBatchSQLShape(t *testing.T) {
	config := BatchConfig{Schema: "public", Table: "batch_demo", KeyColumn: "id"}
	valid := []string{
		`UPDATE public.batch_demo SET value = value + 1 WHERE id = ANY($1::bigint[])`,
		`UPDATE public.batch_demo AS d SET value = d.value - 1 WHERE d.id = ANY($1::int8[])`,
		`UPDATE public.batch_demo SET value = value + 1 WHERE batch_demo.id = ANY(CAST($1 AS bigint[]));`,
	}
	for _, query := range valid {
		if err := checkBatchSQL("sql_up", query, config); err != nil {
			t.Errorf("SQL batch valid ditolak: %q: %v", query, err)
		}
	}
	invalid := map[string]string{
		"tanpa scope":      `UPDATE public.batch_demo SET value = value + 1`,
		"scope diperlebar": `UPDATE public.batch_demo SET value = value + 1 WHERE id = ANY($1::bigint[]) OR true`,
		"kondisi tambahan": `UPDATE public.batch_demo SET value = value + 1 WHERE id = ANY($1::bigint[]) AND value > 0`,
		"bukan ANY":        `UPDATE public.batch_demo SET value = value + 1 WHERE id <> ALL($1::bigint[])`,
		"operator lain":    `UPDATE public.batch_demo SET value = value + 1 WHERE id > ANY($1::bigint[])`,
		"tipe salah":       `UPDATE public.batch_demo SET value = value + 1 WHERE id = ANY($1::int[])`,
		"tanpa cast":       `UPDATE public.batch_demo SET value = value + 1 WHERE id = ANY($1)`,
		"parameter lain":   `UPDATE public.batch_demo SET value = value + $2 WHERE id = ANY($1::bigint[])`,
		"parameter ganda":  `UPDATE public.batch_demo SET value = cardinality($1::bigint[]) WHERE id = ANY($1::bigint[])`,
		"kolom lain":       `UPDATE public.batch_demo SET value = value + 1 WHERE value = ANY($1::bigint[])`,
		"ubah key":         `UPDATE public.batch_demo SET id = id + 1 WHERE id = ANY($1::bigint[])`,
		"tabel lain":       `UPDATE public.other SET value = value + 1 WHERE id = ANY($1::bigint[])`,
		"tanpa schema":     `UPDATE batch_demo SET value = value + 1 WHERE id = ANY($1::bigint[])`,
		"pakai from":       `UPDATE public.batch_demo SET value = o.value FROM public.other o WHERE id = ANY($1::bigint[])`,
		"returning":        `UPDATE public.batch_demo SET value = 1 WHERE id = ANY($1::bigint[]) RETURNING id`,
		"with":             `WITH d AS (DELETE FROM public.other RETURNING 1) UPDATE public.batch_demo SET value = 1 WHERE id = ANY($1::bigint[])`,
		"dua statement":    `UPDATE public.batch_demo SET value = 1 WHERE id = ANY($1::bigint[]); DELETE FROM public.other`,
		"bukan update":     `DELETE FROM public.batch_demo WHERE id = ANY($1::bigint[])`,
	}
	for name, query := range invalid {
		t.Run(name, func(t *testing.T) {
			expectDefinitionError(t, checkBatchSQL("sql_up", query, config), api.CodeInvalidSQL, "sql_up")
		})
	}
}

func TestNormalizeDefinition(t *testing.T) {
	base := func() DefinitionInput {
		return DefinitionInput{Title: "  Tambah kolom  ", Description: text("   "),
			SQLUp: "ALTER TABLE public.a ADD COLUMN b int", SQLDown: text("ALTER TABLE public.a DROP COLUMN b")}
	}
	result, err := normalizeDefinition(base(), 1000, 10000)
	if err != nil || result.Title != "Tambah kolom" || result.Description != nil || result.Mode != ModeNormal {
		t.Fatalf("normalisasi dasar: %+v %v", result, err)
	}

	irreversible := base()
	irreversible.SQLDown, irreversible.Irreversible, irreversible.IrreversibleReason = nil, true, text("data dihapus")
	if _, err := normalizeDefinition(irreversible, 1000, 10000); err != nil {
		t.Fatalf("irreversible dengan alasan: %v", err)
	}
	reversible := base()
	reversible.IrreversibleReason = text("sisa isian form")
	if result, err = normalizeDefinition(reversible, 1000, 10000); err != nil || result.IrreversibleReason != nil {
		t.Fatalf("alasan pada migration reversible harus dibuang: %+v %v", result, err)
	}

	batch := func(size *int) DefinitionInput {
		input := base()
		input.Mode = ModeBatch
		input.SQLUp = "UPDATE public.demo SET v = v + 1 WHERE id = ANY($1::bigint[])"
		input.SQLDown = text("UPDATE public.demo SET v = v - 1 WHERE id = ANY($1::bigint[])")
		input.BatchConfig = &BatchConfig{Schema: "public", Table: "demo", KeyColumn: "id", BatchSize: size}
		return input
	}
	original := batch(nil)
	result, err = normalizeDefinition(original, 500, 2000)
	if err != nil || *result.BatchConfig.BatchSize != 500 || original.BatchConfig.BatchSize != nil {
		t.Fatalf("default batch size: %+v %v", result.BatchConfig, err)
	}

	size := func(value int) *int { return &value }
	tests := []struct {
		name, code, field string
		input             func() DefinitionInput
	}{
		{"judul kosong", api.CodeInvalidInput, "title", func() DefinitionInput { i := base(); i.Title = " "; return i }},
		{"sql up kosong", api.CodeInvalidInput, "sql_up", func() DefinitionInput { i := base(); i.SQLUp = "\n"; return i }},
		{"byte nul", api.CodeInvalidInput, "sql_up", func() DefinitionInput { i := base(); i.SQLUp = "SELECT 1\x00"; return i }},
		{"reversible tanpa down", api.CodeInvalidInput, "sql_down", func() DefinitionInput { i := base(); i.SQLDown = text("  "); return i }},
		{"irreversible tanpa alasan", api.CodeInvalidInput, "irreversible_reason", func() DefinitionInput { i := base(); i.Irreversible = true; return i }},
		{"sql down invalid", api.CodeInvalidSQL, "sql_down", func() DefinitionInput { i := base(); i.SQLDown = text("COMMIT"); return i }},
		{"mode tidak dikenal", api.CodeInvalidInput, "mode", func() DefinitionInput { i := base(); i.Mode = "stream"; return i }},
		{"normal dengan config", api.CodeInvalidInput, "batch_config", func() DefinitionInput { i := base(); i.BatchConfig = &BatchConfig{}; return i }},
		{"batch tanpa config", api.CodeInvalidInput, "batch_config", func() DefinitionInput { i := batch(nil); i.BatchConfig = nil; return i }},
		{"batch size nol", api.CodeInvalidInput, "batch_config.batch_size", func() DefinitionInput { return batch(size(0)) }},
		{"batch size negatif", api.CodeInvalidInput, "batch_config.batch_size", func() DefinitionInput { return batch(size(-5)) }},
		{"batch size lewat maksimum", api.CodeInvalidInput, "batch_config.batch_size", func() DefinitionInput { return batch(size(10001)) }},
		{"tabel kosong", api.CodeInvalidInput, "batch_config.table", func() DefinitionInput { i := batch(nil); i.BatchConfig.Table = ""; return i }},
		{"tabel sistem", api.CodeInvalidInput, "batch_config.table", func() DefinitionInput { i := batch(nil); i.BatchConfig.Schema = "pg_catalog"; return i }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeDefinition(test.input(), 1000, 10000)
			expectDefinitionError(t, err, test.code, test.field)
		})
	}
}

// event(id, migration, operasi, target) dengan target 0 berarti tanpa target
func event(id, migrationID int64, operation string, target int64) successEvent {
	result := successEvent{ExecutionID: id, MigrationID: migrationID, Operation: operation}
	if target != 0 {
		result.TargetExecutionID = &target
	}
	return result
}

func TestLinearPosition(t *testing.T) {
	const a, b, c, d = 1, 2, 3, 4
	positions := func(p databasePosition) []string { return []string{p.of(a), p.of(b), p.of(c), p.of(d)} }
	events := []successEvent{event(10, a, "up", 0), event(11, b, "up", 0), event(12, c, "up", 0),
		event(13, c, "rollback", 12), event(14, b, "rollback", 11)}

	p, err := buildPosition(events)
	if err != nil {
		t.Fatal(err)
	}
	if got := positions(p); !reflect.DeepEqual(got, []string{PositionActive, PositionRolledBack, PositionRolledBack, PositionNeverApplied}) {
		t.Fatalf("posisi setelah rollback C lalu B: %v", got)
	}
	// forward harus B dahulu dan targetnya execution yang dibatalkan
	if next, ok := p.forwardTop(); !ok || next != (stackEntry{11, b}) {
		t.Fatalf("kandidat forward: %+v", next)
	}

	p, err = buildPosition(append(events[:len(events):len(events)], event(15, b, "forward", 0), event(16, c, "forward", 0)))
	if err != nil {
		t.Fatal(err)
	}
	// rollback atas hasil forward menunjuk execution forward yang terbaru
	if top, _ := p.activeTop(); top != (stackEntry{16, c}) || len(p.forward) != 0 {
		t.Fatalf("setelah forward B lalu C: %+v", p)
	}

	p, err = buildPosition(append(events[:len(events):len(events)], event(17, d, "up", 0), event(18, a, "compensate", 0)))
	if err != nil {
		t.Fatal(err)
	}
	if got := positions(p); !reflect.DeepEqual(got, []string{PositionActive, PositionSuperseded, PositionSuperseded, PositionActive}) {
		t.Fatalf("up baru harus membuang forward stack: %v", got)
	}

	broken := map[string][]successEvent{
		"rollback bukan puncak":  {event(1, a, "up", 0), event(2, b, "up", 0), event(3, a, "rollback", 1)},
		"rollback tanpa aktif":   {event(1, a, "rollback", 9)},
		"forward bukan kandidat": {event(1, a, "up", 0), event(2, a, "rollback", 1), event(3, b, "forward", 0)},
		"operasi asing":          {event(1, a, "squash", 0)},
	}
	for name, events := range broken {
		if _, err := buildPosition(events); !errors.Is(err, errInconsistentHistory) {
			t.Errorf("%s harus dianggap tidak konsisten: %v", name, err)
		}
	}
}
