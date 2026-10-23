CREATE TABLE session_tokens (
    token_hash BYTEA PRIMARY KEY,
    session_id BIGINT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    grace_expires_at TIMESTAMPTZ CHECK (grace_expires_at >= issued_at)
);

CREATE INDEX session_tokens_session_id_idx ON session_tokens (session_id);

CREATE UNIQUE INDEX session_tokens_one_current_per_session
ON session_tokens (session_id)
WHERE grace_expires_at IS NULL;
