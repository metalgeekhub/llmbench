-- v0.4: saved, versioned test definitions; runs linked to the definition
-- version they came from; imported runs.

CREATE TABLE test_definitions (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    version     INTEGER NOT NULL,          -- latest version
    config      TEXT NOT NULL,             -- latest config (JSON)
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- Every saved version, so a run can always be traced to the exact config.
CREATE TABLE test_definition_versions (
    definition_id TEXT NOT NULL REFERENCES test_definitions (id) ON DELETE CASCADE,
    version       INTEGER NOT NULL,
    config        TEXT NOT NULL,
    created_at    INTEGER NOT NULL,
    PRIMARY KEY (definition_id, version)
);

ALTER TABLE test_runs ADD COLUMN definition_id TEXT NOT NULL DEFAULT '';
ALTER TABLE test_runs ADD COLUMN definition_version INTEGER NOT NULL DEFAULT 0;
-- Set on runs imported from an export file (Unix ms); NULL for local runs.
ALTER TABLE test_runs ADD COLUMN imported_at INTEGER;
