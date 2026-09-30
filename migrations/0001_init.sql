-- Initial schema for the MVP. Timestamps are Unix milliseconds.

CREATE TABLE sources (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    type            TEXT NOT NULL,
    base_url        TEXT NOT NULL,
    api_key_enc     TEXT NOT NULL DEFAULT '',
    headers_enc     TEXT NOT NULL DEFAULT '',
    extra_body      TEXT NOT NULL DEFAULT '{}',
    timeout_ms      INTEGER NOT NULL DEFAULT 0,
    tls_skip_verify INTEGER NOT NULL DEFAULT 0,
    models          TEXT NOT NULL DEFAULT '[]',
    models_auto     INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

CREATE TABLE chat_sessions (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL DEFAULT '',
    source_id  TEXT NOT NULL DEFAULT '',
    model      TEXT NOT NULL DEFAULT '',
    params     TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX idx_chat_sessions_updated ON chat_sessions (updated_at DESC);

CREATE TABLE chat_messages (
    id         TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES chat_sessions (id) ON DELETE CASCADE,
    role       TEXT NOT NULL,
    content    TEXT NOT NULL DEFAULT '',
    reasoning  TEXT NOT NULL DEFAULT '',
    source_id  TEXT NOT NULL DEFAULT '',
    model      TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_chat_messages_session ON chat_messages (session_id, created_at);

-- Every request made by the app (chat or test). Deliberately not linked to
-- chat_sessions by foreign key: request history outlives deleted chats.
CREATE TABLE requests (
    id               TEXT PRIMARY KEY,
    kind             TEXT NOT NULL,
    source_id        TEXT NOT NULL,
    source_name      TEXT NOT NULL,
    model            TEXT NOT NULL,
    session_id       TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL,
    http_status      INTEGER NOT NULL DEFAULT 0,
    error_type       TEXT NOT NULL DEFAULT '',
    error_message    TEXT NOT NULL DEFAULT '',
    started_at       INTEGER NOT NULL,
    ttft_ms          REAL,
    ttfat_ms         REAL,
    e2e_ms           REAL NOT NULL,
    tpot_ms          REAL,
    output_tps       REAL,
    prefill_tps      REAL,
    input_tokens     INTEGER NOT NULL DEFAULT 0,
    output_tokens    INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    cached_tokens    INTEGER NOT NULL DEFAULT 0,
    tokens_estimated INTEGER NOT NULL DEFAULT 0,
    chunk_count      INTEGER NOT NULL DEFAULT 0,
    itl              TEXT NOT NULL DEFAULT '{}',
    params           TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_requests_started ON requests (started_at DESC);
CREATE INDEX idx_requests_model ON requests (model, started_at DESC);
CREATE INDEX idx_requests_session ON requests (session_id);

-- Per-chunk timestamps for detailed ITL analysis (gzip-compressed JSON).
CREATE TABLE request_timelines (
    request_id TEXT PRIMARY KEY REFERENCES requests (id) ON DELETE CASCADE,
    data       BLOB NOT NULL
);
