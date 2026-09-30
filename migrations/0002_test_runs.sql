-- Benchmark runs (v0.2). A run has one cell per target x concurrency level.

CREATE TABLE test_runs (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    type        TEXT NOT NULL,
    status      TEXT NOT NULL,
    config      TEXT NOT NULL DEFAULT '{}',
    error       TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    started_at  INTEGER,
    finished_at INTEGER
);

CREATE INDEX idx_test_runs_created ON test_runs (created_at DESC);

CREATE TABLE run_cells (
    id             TEXT PRIMARY KEY,
    run_id         TEXT NOT NULL REFERENCES test_runs (id) ON DELETE CASCADE,
    idx            INTEGER NOT NULL,
    source_id      TEXT NOT NULL,
    source_name    TEXT NOT NULL,
    model          TEXT NOT NULL,
    concurrency    INTEGER NOT NULL,
    status         TEXT NOT NULL,
    summary        TEXT,
    client_cpu_pct REAL,
    error          TEXT NOT NULL DEFAULT '',
    started_at     INTEGER,
    finished_at    INTEGER
);

CREATE INDEX idx_run_cells_run ON run_cells (run_id, idx);

ALTER TABLE requests ADD COLUMN run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE requests ADD COLUMN cell_id TEXT NOT NULL DEFAULT '';
ALTER TABLE requests ADD COLUMN warmup INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_requests_run ON requests (run_id, cell_id);
