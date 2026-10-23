CREATE TABLE group_database_access (
    group_id BIGINT NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    database_id BIGINT NOT NULL REFERENCES database_connections (id) ON DELETE RESTRICT,
    access_level VARCHAR(10) NOT NULL CHECK (access_level IN ('view', 'edit')),
    granted_by BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, database_id)
);

CREATE INDEX group_database_access_database_id_idx ON group_database_access (database_id);
CREATE INDEX group_database_access_granted_by_idx ON group_database_access (granted_by);
