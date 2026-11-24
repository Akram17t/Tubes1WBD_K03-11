-- M2: migration_executions menjadi history seluruh attempt (sukses, gagal, retry, rollback, forward, batch)
-- dan migration_batch_attempts menyimpan detail per batch.
BEGIN;

ALTER TABLE migration_executions
    ADD COLUMN job_id BIGINT REFERENCES migration_jobs (id) ON DELETE RESTRICT,
    ADD COLUMN request_id BIGINT REFERENCES migration_requests (id) ON DELETE RESTRICT,
    ADD COLUMN database_id BIGINT REFERENCES database_connections (id) ON DELETE RESTRICT,
    ADD COLUMN revision INTEGER CHECK (revision > 0),
    ADD COLUMN operation VARCHAR(20) NOT NULL DEFAULT 'up'
        CHECK (operation IN ('up', 'rollback', 'forward', 'compensate')),
    ADD COLUMN attempt INTEGER CHECK (attempt > 0),
    -- Execution sukses yang dibatalkan; hanya terisi pada rollback. Dasar pasangan Hide rollback.
    ADD COLUMN target_execution_id BIGINT REFERENCES migration_executions (id) ON DELETE RESTRICT,
    -- Urutan event sukses per database, terpisah dari revision.
    ADD COLUMN history_sequence BIGINT CHECK (history_sequence > 0),
    ADD COLUMN error_code VARCHAR(50),
    ADD COLUMN sqlstate VARCHAR(5),
    ADD COLUMN total_rows BIGINT CHECK (total_rows >= 0),
    ADD COLUMN committed_rows BIGINT NOT NULL DEFAULT 0 CHECK (committed_rows >= 0),
    ADD COLUMN committed_batches BIGINT NOT NULL DEFAULT 0 CHECK (committed_batches >= 0),
    ADD COLUMN reverted_rows BIGINT NOT NULL DEFAULT 0 CHECK (reverted_rows >= 0),
    ADD COLUMN reverted_batches BIGINT NOT NULL DEFAULT 0 CHECK (reverted_batches >= 0),
    ADD COLUMN last_cursor BIGINT,
    ADD COLUMN worker_id TEXT,
    ADD COLUMN trace_id VARCHAR(32);

-- Backfill data M1: setiap attempt lama adalah up pada revision 1, attempt ke-N pada migration-nya.
UPDATE migration_executions e
SET database_id = m.database_id,
    revision = 1,
    attempt = numbered.attempt,
    history_sequence = CASE WHEN e.status = 'success' THEN m.version END
FROM migrations m,
     (SELECT id, row_number() OVER (PARTITION BY migration_id ORDER BY started_at, id) AS attempt
      FROM migration_executions) numbered
WHERE m.id = e.migration_id AND numbered.id = e.id;

ALTER TABLE migration_executions
    ALTER COLUMN database_id SET NOT NULL,
    ALTER COLUMN revision SET NOT NULL,
    ALTER COLUMN attempt SET NOT NULL;

-- Jalur insert M1 hanya mengisi migration_id/executed_by/sql_snapshot. Trigger ini melengkapi
-- database, revisi, dan nomor attempt dari migration agar kode lama tetap berjalan; kode M2 mengisinya eksplisit.
CREATE FUNCTION migration_executions_fill_defaults() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.database_id IS NULL OR NEW.revision IS NULL THEN
        SELECT m.database_id, m.revision INTO NEW.database_id, NEW.revision
        FROM migrations m WHERE m.id = NEW.migration_id;
    END IF;
    IF NEW.attempt IS NULL THEN
        SELECT COALESCE(MAX(e.attempt), 0) + 1 INTO NEW.attempt
        FROM migration_executions e
        WHERE e.migration_id = NEW.migration_id AND e.job_id IS NOT DISTINCT FROM NEW.job_id;
    END IF;
    RETURN NEW;
END
$$;

DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    FOR constraint_name IN
        SELECT c.conname
        FROM pg_constraint c
        WHERE c.conrelid = 'public.migration_executions'::regclass
          AND c.contype = 'c'
          AND pg_get_constraintdef(c.oid) ILIKE '%status%running%success%failed%'
    LOOP
        EXECUTE format('ALTER TABLE public.migration_executions DROP CONSTRAINT %I', constraint_name);
    END LOOP;
END
$$;

ALTER TABLE migration_executions
    ADD CONSTRAINT migration_executions_status_check CHECK (
        status IN ('running', 'success', 'failed', 'partial', 'partially_reverted', 'outcome_unknown')
    ),
    ADD CONSTRAINT migration_executions_target_operation_check CHECK (
        (operation = 'rollback') = (target_execution_id IS NOT NULL)
    ),
    -- Nama berbeda dari CHECK (history_sequence > 0) yang dibuat otomatis saat ADD COLUMN.
    ADD CONSTRAINT migration_executions_history_sequence_status_check CHECK (
        history_sequence IS NULL OR status = 'success'
    ),
    ADD CONSTRAINT migration_executions_target_not_self_check CHECK (
        target_execution_id IS NULL OR target_execution_id <> id
    );

-- Satu job tidak punya dua attempt dengan nomor sama; baris M1 (job_id NULL) tidak terkena.
CREATE UNIQUE INDEX migration_executions_job_attempt_key
    ON migration_executions (job_id, attempt) WHERE job_id IS NOT NULL;
-- Satu job hanya boleh punya satu attempt yang masih berjalan.
CREATE UNIQUE INDEX migration_executions_one_running_per_job_idx
    ON migration_executions (job_id) WHERE job_id IS NOT NULL AND status = 'running';
CREATE UNIQUE INDEX migration_executions_history_sequence_key
    ON migration_executions (database_id, history_sequence) WHERE history_sequence IS NOT NULL;
-- Satu execution sukses hanya boleh dibatalkan oleh satu rollback sukses.
CREATE UNIQUE INDEX migration_executions_one_successful_rollback_idx
    ON migration_executions (target_execution_id)
    WHERE target_execution_id IS NOT NULL AND status = 'success';
CREATE INDEX migration_executions_history_idx ON migration_executions (database_id, started_at, id);
CREATE INDEX migration_executions_request_id_idx ON migration_executions (request_id) WHERE request_id IS NOT NULL;
CREATE INDEX migration_executions_target_execution_id_idx
    ON migration_executions (target_execution_id) WHERE target_execution_id IS NOT NULL;

CREATE TRIGGER migration_executions_fill_defaults
    BEFORE INSERT ON migration_executions
    FOR EACH ROW EXECUTE FUNCTION migration_executions_fill_defaults();

CREATE TABLE migration_batch_attempts (
    id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    execution_id BIGINT NOT NULL REFERENCES migration_executions (id) ON DELETE RESTRICT,
    job_id BIGINT NOT NULL REFERENCES migration_jobs (id) ON DELETE RESTRICT,
    batch_number INTEGER NOT NULL CHECK (batch_number > 0),
    -- up menerapkan batch, down mengompensasi batch yang sudah committed
    direction VARCHAR(4) NOT NULL CHECK (direction IN ('up', 'down')),
    -- Cakupan ID sebenarnya disimpan di scope target; kolom ini merujuk batas dan jumlahnya.
    first_id BIGINT,
    last_id BIGINT,
    row_count BIGINT NOT NULL DEFAULT 0 CHECK (row_count >= 0),
    sql_snapshot TEXT NOT NULL,
    -- Parameter yang aman, misalnya {"ids": 1000}; bukan daftar nilai sensitif
    parameters JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(parameters) = 'object'),
    status VARCHAR(10) NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'success', 'failed')),
    error_code VARCHAR(50),
    sqlstate VARCHAR(5),
    error_message TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    CHECK (finished_at IS NULL OR finished_at >= started_at),
    CHECK (status = 'running' OR finished_at IS NOT NULL),
    CHECK ((first_id IS NULL) = (last_id IS NULL) AND (first_id IS NULL OR first_id <= last_id)),
    UNIQUE (execution_id, direction, batch_number)
);

CREATE INDEX migration_batch_attempts_job_id_idx ON migration_batch_attempts (job_id, direction, batch_number);

COMMIT;
