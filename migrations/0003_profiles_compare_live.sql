-- v0.2: model profiles, chat compare groups, benchmark targets from
-- profiles, and the persisted live-dashboard timeline of a run.

CREATE TABLE model_profiles (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    source_id  TEXT NOT NULL,
    model      TEXT NOT NULL,
    params     TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

ALTER TABLE chat_sessions ADD COLUMN profile_id TEXT NOT NULL DEFAULT '';
-- Sessions sharing a compare_id are the columns of one side-by-side comparison.
ALTER TABLE chat_sessions ADD COLUMN compare_id TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_chat_sessions_compare ON chat_sessions (compare_id);

ALTER TABLE run_cells ADD COLUMN profile_id TEXT NOT NULL DEFAULT '';
ALTER TABLE run_cells ADD COLUMN profile_name TEXT NOT NULL DEFAULT '';

-- Per-second live metrics (JSON), kept after the run for the dashboard charts.
ALTER TABLE test_runs ADD COLUMN timeline TEXT;
