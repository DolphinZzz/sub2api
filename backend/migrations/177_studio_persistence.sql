CREATE TABLE IF NOT EXISTS studio_sessions (
    id VARCHAR(64) PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    mode VARCHAR(16) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    metadata_path VARCHAR(1024) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT studio_sessions_mode_check CHECK (mode IN ('chat', 'image')),
    CONSTRAINT studio_sessions_status_check CHECK (status IN ('active', 'deleting'))
);

CREATE INDEX IF NOT EXISTS studio_sessions_user_updated_idx
    ON studio_sessions (user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS studio_sessions_status_expires_idx
    ON studio_sessions (status, expires_at);

CREATE TABLE IF NOT EXISTS studio_messages (
    id VARCHAR(64) PRIMARY KEY,
    session_id VARCHAR(64) NOT NULL REFERENCES studio_sessions(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    turn_id VARCHAR(64),
    role VARCHAR(16) NOT NULL,
    message_type VARCHAR(16) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'completed',
    metadata_path VARCHAR(1024) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT studio_messages_role_check CHECK (role IN ('user', 'assistant')),
    CONSTRAINT studio_messages_type_check CHECK (message_type IN ('text', 'images', 'error'))
);

CREATE INDEX IF NOT EXISTS studio_messages_user_session_created_idx
    ON studio_messages (user_id, session_id, created_at);
CREATE INDEX IF NOT EXISTS studio_messages_session_turn_idx
    ON studio_messages (session_id, turn_id) WHERE turn_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS studio_requests (
    id VARCHAR(64) PRIMARY KEY,
    session_id VARCHAR(64) NOT NULL REFERENCES studio_sessions(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    turn_id VARCHAR(64) NOT NULL,
    api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    api_key_name VARCHAR(100) NOT NULL DEFAULT '',
    endpoint VARCHAR(2048) NOT NULL,
    model VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'running',
    request_path VARCHAR(1024) NOT NULL,
    response_path VARCHAR(1024),
    duration_ms BIGINT,
    error_code VARCHAR(128),
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT studio_requests_status_check CHECK (
        status IN ('running', 'completed', 'failed', 'cancelled', 'persistence_failed')
    )
);

CREATE INDEX IF NOT EXISTS studio_requests_user_session_created_idx
    ON studio_requests (user_id, session_id, created_at);
CREATE INDEX IF NOT EXISTS studio_requests_session_turn_idx
    ON studio_requests (session_id, turn_id, created_at);
CREATE INDEX IF NOT EXISTS studio_requests_running_idx
    ON studio_requests (session_id) WHERE status = 'running';

CREATE TABLE IF NOT EXISTS studio_generation_records (
    id VARCHAR(64) PRIMARY KEY,
    session_id VARCHAR(64) NOT NULL REFERENCES studio_sessions(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_id VARCHAR(64) NOT NULL REFERENCES studio_requests(id) ON DELETE CASCADE,
    message_id VARCHAR(64) REFERENCES studio_messages(id) ON DELETE SET NULL,
    status VARCHAR(32) NOT NULL,
    metadata_path VARCHAR(1024) NOT NULL,
    revised_prompt TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS studio_generations_user_session_created_idx
    ON studio_generation_records (user_id, session_id, created_at);
CREATE INDEX IF NOT EXISTS studio_generations_request_idx
    ON studio_generation_records (request_id, created_at);

CREATE TABLE IF NOT EXISTS studio_assets (
    id VARCHAR(64) PRIMARY KEY,
    session_id VARCHAR(64) NOT NULL REFERENCES studio_sessions(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_id VARCHAR(64) REFERENCES studio_requests(id) ON DELETE CASCADE,
    generation_id VARCHAR(64) REFERENCES studio_generation_records(id) ON DELETE CASCADE,
    kind VARCHAR(16) NOT NULL,
    sha256 CHAR(64) NOT NULL,
    mime_type VARCHAR(128) NOT NULL,
    byte_size BIGINT NOT NULL,
    relative_path VARCHAR(1024) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT studio_assets_kind_check CHECK (kind IN ('input', 'output')),
    CONSTRAINT studio_assets_byte_size_check CHECK (byte_size >= 0)
);

CREATE INDEX IF NOT EXISTS studio_assets_user_session_created_idx
    ON studio_assets (user_id, session_id, created_at);
CREATE INDEX IF NOT EXISTS studio_assets_request_idx
    ON studio_assets (request_id, created_at) WHERE request_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS studio_assets_generation_idx
    ON studio_assets (generation_id) WHERE generation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS studio_assets_input_sha_idx
    ON studio_assets (session_id, sha256) WHERE kind = 'input';
