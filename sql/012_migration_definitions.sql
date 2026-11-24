BEGIN;

ALTER TABLE migrations
    ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    ADD COLUMN sql_down TEXT,
    ADD COLUMN irreversible BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN irreversible_reason TEXT,
    ADD COLUMN mode VARCHAR(10) NOT NULL DEFAULT 'normal' CHECK (mode IN ('normal', 'batch')),
    ADD COLUMN batch_config JSONB,
    ADD COLUMN locked_at TIMESTAMPTZ;

-- Data M1 tidak punya SQL down dan tidak boleh dikarang. Semua definisi lama ditandai irreversible;
-- yang pernah dieksekusi (berhasil maupun gagal) langsung dikunci agar SQL-nya immutable.
UPDATE migrations m
SET irreversible = true,
    irreversible_reason = 'SQL down tidak tersedia pada data migration M1',
    locked_at = CASE
        WHEN EXISTS (SELECT 1 FROM migration_executions e WHERE e.migration_id = m.id) THEN now()
    END;

-- Status success tidak lagi sama dengan "punya version": posisi aktif diturunkan dari ledger eksekusi.
DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    FOR constraint_name IN
        SELECT c.conname
        FROM pg_constraint c
        WHERE c.conrelid = 'public.migrations'::regclass
          AND c.contype = 'c'
          AND pg_get_constraintdef(c.oid) ILIKE '%status%success%version%'
    LOOP
        EXECUTE format('ALTER TABLE public.migrations DROP CONSTRAINT %I', constraint_name);
    END LOOP;
END
$$;

-- Invarian "reversible wajib punya SQL down" diperiksa validator aplikasi, bukan CHECK,
-- supaya draft yang baru dibuat dan fixture M1 tetap dapat disimpan.
ALTER TABLE migrations
    ADD CONSTRAINT migrations_irreversible_reason_check
        CHECK (NOT irreversible OR btrim(COALESCE(irreversible_reason, '')) <> ''),
    ADD CONSTRAINT migrations_sql_down_not_blank_check
        CHECK (sql_down IS NULL OR btrim(sql_down) <> ''),
    ADD CONSTRAINT migrations_batch_config_check
        CHECK ((mode = 'batch') = (batch_config IS NOT NULL)
               AND (batch_config IS NULL OR jsonb_typeof(batch_config) = 'object'));

-- Setelah locked_at terisi, SQL dan konfigurasi eksekusi tidak boleh berubah dan kunci tidak boleh dilepas.
CREATE FUNCTION migrations_enforce_lock() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.locked_at IS NOT NULL AND (
        NEW.locked_at IS DISTINCT FROM OLD.locked_at
        OR NEW.sql_content IS DISTINCT FROM OLD.sql_content
        OR NEW.sql_down IS DISTINCT FROM OLD.sql_down
        OR NEW.irreversible IS DISTINCT FROM OLD.irreversible
        OR NEW.irreversible_reason IS DISTINCT FROM OLD.irreversible_reason
        OR NEW.mode IS DISTINCT FROM OLD.mode
        OR NEW.batch_config IS DISTINCT FROM OLD.batch_config
        OR NEW.revision IS DISTINCT FROM OLD.revision
        OR NEW.database_id IS DISTINCT FROM OLD.database_id
    ) THEN
        RAISE EXCEPTION 'migration % sudah terkunci dan tidak dapat diubah', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER migrations_enforce_lock
    BEFORE UPDATE ON migrations
    FOR EACH ROW EXECUTE FUNCTION migrations_enforce_lock();

COMMIT;
