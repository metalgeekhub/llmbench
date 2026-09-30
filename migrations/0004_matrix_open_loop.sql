-- v0.3: matrix dimensions per cell, open-loop load, sample outputs, and
-- saved run comparisons.

-- Context length (synthetic input tokens) of the cell; 0 when not swept.
ALTER TABLE run_cells ADD COLUMN context_tokens INTEGER NOT NULL DEFAULT 0;
-- Thinking level override of the cell; '' when not swept.
ALTER TABLE run_cells ADD COLUMN thinking TEXT NOT NULL DEFAULT '';
-- Open-loop target arrival rate (requests/s); NULL for closed-loop cells.
ALTER TABLE run_cells ADD COLUMN arrival_rate REAL;
-- Open loop: p95 delay between scheduled and actual send (client limits).
ALTER TABLE run_cells ADD COLUMN schedule_lag_p95_ms REAL;
-- First successful response of the cell, for side-by-side output review.
ALTER TABLE run_cells ADD COLUMN sample_output TEXT NOT NULL DEFAULT '';
ALTER TABLE run_cells ADD COLUMN sample_reasoning TEXT NOT NULL DEFAULT '';

CREATE TABLE comparisons (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    run_ids    TEXT NOT NULL DEFAULT '[]',
    settings   TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
