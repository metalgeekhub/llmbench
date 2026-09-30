package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

func toNullMillis(t *time.Time) any {
	if t == nil {
		return nil
	}
	return toMillis(*t)
}

func fromNullMillis(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := fromMillis(n.Int64)
	return &t
}

func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

// --- runs ---

const runColumns = `id, name, type, status, config, error, created_at, started_at, finished_at, timeline,
	definition_id, definition_version, imported_at`

// runListColumns omits the (potentially large) timeline.
const runListColumns = `id, name, type, status, config, error, created_at, started_at, finished_at, NULL,
	definition_id, definition_version, imported_at`

func scanRun(sc scanner) (TestRun, error) {
	var (
		r                             TestRun
		config                        string
		timeline                      sql.NullString
		createdAt                     int64
		startedAt, finished, imported sql.NullInt64
	)
	if err := sc.Scan(&r.ID, &r.Name, &r.Type, &r.Status, &config, &r.Error, &createdAt, &startedAt, &finished,
		&timeline, &r.DefinitionID, &r.DefinitionVersion, &imported); err != nil {
		return TestRun{}, mapErr(err)
	}
	r.Config = json.RawMessage(config)
	if timeline.Valid {
		r.Timeline = json.RawMessage(timeline.String)
	}
	r.CreatedAt = fromMillis(createdAt)
	r.StartedAt = fromNullMillis(startedAt)
	r.FinishedAt = fromNullMillis(finished)
	r.ImportedAt = fromNullMillis(imported)
	return r, nil
}

func insertRun(ctx context.Context, tx *sql.Tx, r *TestRun, cells []RunCell) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	config := string(r.Config)
	if config == "" {
		config = "{}"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO test_runs (`+runColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Name, r.Type, r.Status, config, r.Error, toMillis(r.CreatedAt),
		toNullMillis(r.StartedAt), toNullMillis(r.FinishedAt), nullJSON(r.Timeline),
		r.DefinitionID, r.DefinitionVersion, toNullMillis(r.ImportedAt)); err != nil {
		return mapErr(err)
	}
	for i := range cells {
		c := &cells[i]
		c.RunID = r.ID
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_cells (`+cellColumns+`)
			VALUES (`+cellPlaceholders+`)`, cellArgs(c)...); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

func (s *SQLite) CreateRun(ctx context.Context, r *TestRun, cells []RunCell) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertRun(ctx, tx, r, cells); err != nil {
		return err
	}
	return tx.Commit()
}

// ImportRun stores a run, its cells and requests from an export in one
// transaction. It fails with ErrConflict if the run ID already exists.
func (s *SQLite) ImportRun(ctx context.Context, r *TestRun, cells []RunCell, requests []RequestRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertRun(ctx, tx, r, cells); err != nil {
		return err
	}
	for i := range requests {
		rec := &requests[i]
		rec.Request.RunID = r.ID
		if err := insertRequest(ctx, tx, &rec.Request, rec.Timeline); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) UpdateRunConfig(ctx context.Context, runID string, config json.RawMessage) error {
	if !json.Valid(config) {
		return fmt.Errorf("config is not valid JSON")
	}
	return checkAffected(s.db.ExecContext(ctx, `UPDATE test_runs SET config = ? WHERE id = ?`, string(config), runID))
}

// UpdateRun saves a run's mutable fields. An empty Timeline keeps the stored one.
func (s *SQLite) UpdateRun(ctx context.Context, r *TestRun) error {
	return checkAffected(s.db.ExecContext(ctx, `UPDATE test_runs SET
		name = ?, status = ?, error = ?, started_at = ?, finished_at = ?, timeline = COALESCE(?, timeline)
		WHERE id = ?`,
		r.Name, r.Status, r.Error, toNullMillis(r.StartedAt), toNullMillis(r.FinishedAt), nullJSON(r.Timeline), r.ID))
}

func (s *SQLite) GetRun(ctx context.Context, id string) (TestRun, error) {
	return scanRun(s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM test_runs WHERE id = ?`, id))
}

func (s *SQLite) ListRuns(ctx context.Context, limit int) ([]TestRun, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+runListColumns+` FROM test_runs
		ORDER BY created_at DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TestRun{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteRun(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Timelines cascade from requests; cells cascade from the run.
	if _, err := tx.ExecContext(ctx, `DELETE FROM requests WHERE run_id = ?`, id); err != nil {
		return err
	}
	if err := checkAffected(tx.ExecContext(ctx, `DELETE FROM test_runs WHERE id = ?`, id)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) MarkInterruptedRuns(ctx context.Context) (int, error) {
	now := toMillis(time.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE run_cells SET status = ?, finished_at = COALESCE(finished_at, ?)
		WHERE status IN (?, ?)`, StatusInterrupted, now, StatusPending, StatusRunning); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE test_runs SET status = ?, finished_at = ? WHERE status = ?`,
		StatusInterrupted, now, StatusRunning)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), tx.Commit()
}

// --- cells ---

const cellColumns = `id, run_id, idx, source_id, source_name, model, profile_id, profile_name, concurrency,
	status, summary, client_cpu_pct, error, started_at, finished_at, context_tokens, thinking, arrival_rate,
	schedule_lag_p95_ms, sample_output, sample_reasoning`

const cellPlaceholders = `?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?`

func cellArgs(c *RunCell) []any {
	var summary any
	if c.Summary != nil {
		summary = toJSON(c.Summary)
	}
	return []any{c.ID, c.RunID, c.Index, c.SourceID, c.SourceName, c.Model, c.ProfileID, c.ProfileName,
		c.Concurrency, c.Status, summary, c.ClientCPUPct, c.Error, toNullMillis(c.StartedAt), toNullMillis(c.FinishedAt),
		c.ContextTokens, c.Thinking, c.ArrivalRate, c.ScheduleLagP95Ms, c.SampleOutput, c.SampleReasoning}
}

func (s *SQLite) UpdateCell(ctx context.Context, c *RunCell) error {
	args := cellArgs(c)
	// Move the ID from the front to the WHERE clause.
	return checkAffected(s.db.ExecContext(ctx, `UPDATE run_cells SET
		run_id = ?, idx = ?, source_id = ?, source_name = ?, model = ?, profile_id = ?, profile_name = ?,
		concurrency = ?, status = ?, summary = ?, client_cpu_pct = ?, error = ?, started_at = ?, finished_at = ?,
		context_tokens = ?, thinking = ?, arrival_rate = ?, schedule_lag_p95_ms = ?, sample_output = ?,
		sample_reasoning = ?
		WHERE id = ?`,
		append(args[1:], args[0])...))
}

func (s *SQLite) ListCells(ctx context.Context, runID string) ([]RunCell, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cellColumns+` FROM run_cells WHERE run_id = ? ORDER BY idx`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunCell{}
	for rows.Next() {
		var (
			c                   RunCell
			summary             sql.NullString
			cpu, rate, lag      sql.NullFloat64
			startedAt, finished sql.NullInt64
		)
		if err := rows.Scan(&c.ID, &c.RunID, &c.Index, &c.SourceID, &c.SourceName, &c.Model, &c.ProfileID,
			&c.ProfileName, &c.Concurrency, &c.Status, &summary, &cpu, &c.Error, &startedAt, &finished,
			&c.ContextTokens, &c.Thinking, &rate, &lag, &c.SampleOutput, &c.SampleReasoning); err != nil {
			return nil, err
		}
		c.ArrivalRate = nullPtr(rate)
		c.ScheduleLagP95Ms = nullPtr(lag)
		if summary.Valid {
			if err := fromJSON(summary.String, &c.Summary); err != nil {
				return nil, fmt.Errorf("cell %s summary: %w", c.ID, err)
			}
		}
		c.ClientCPUPct = nullPtr(cpu)
		c.StartedAt = fromNullMillis(startedAt)
		c.FinishedAt = fromNullMillis(finished)
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- saved comparisons ---

const comparisonColumns = `id, name, run_ids, settings, created_at, updated_at`

func scanComparison(sc scanner) (Comparison, error) {
	var (
		c                    Comparison
		runIDs, settings     string
		createdAt, updatedAt int64
	)
	if err := sc.Scan(&c.ID, &c.Name, &runIDs, &settings, &createdAt, &updatedAt); err != nil {
		return Comparison{}, mapErr(err)
	}
	if err := fromJSON(runIDs, &c.RunIDs); err != nil {
		return Comparison{}, fmt.Errorf("comparison %s run_ids: %w", c.ID, err)
	}
	c.Settings = json.RawMessage(settings)
	c.CreatedAt = fromMillis(createdAt)
	c.UpdatedAt = fromMillis(updatedAt)
	return c, nil
}

func (s *SQLite) ListComparisons(ctx context.Context) ([]Comparison, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+comparisonColumns+` FROM comparisons ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Comparison{}
	for rows.Next() {
		c, err := scanComparison(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *SQLite) GetComparison(ctx context.Context, id string) (Comparison, error) {
	return scanComparison(s.db.QueryRowContext(ctx, `SELECT `+comparisonColumns+` FROM comparisons WHERE id = ?`, id))
}

func comparisonSettings(c *Comparison) string {
	if len(c.Settings) == 0 {
		return "{}"
	}
	return string(c.Settings)
}

func (s *SQLite) CreateComparison(ctx context.Context, c *Comparison) error {
	now := time.Now().UTC()
	c.CreatedAt, c.UpdatedAt = now, now
	_, err := s.db.ExecContext(ctx, `INSERT INTO comparisons (`+comparisonColumns+`) VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, toJSON(orEmptySlice(c.RunIDs)), comparisonSettings(c), toMillis(now), toMillis(now))
	return mapErr(err)
}

func (s *SQLite) UpdateComparison(ctx context.Context, c *Comparison) error {
	c.UpdatedAt = time.Now().UTC()
	return checkAffected(s.db.ExecContext(ctx, `UPDATE comparisons SET name = ?, run_ids = ?, settings = ?, updated_at = ?
		WHERE id = ?`, c.Name, toJSON(orEmptySlice(c.RunIDs)), comparisonSettings(c), toMillis(c.UpdatedAt), c.ID))
}

func (s *SQLite) DeleteComparison(ctx context.Context, id string) error {
	return checkAffected(s.db.ExecContext(ctx, `DELETE FROM comparisons WHERE id = ?`, id))
}

// DistributionMetrics are the request columns RunMetricValues accepts.
var DistributionMetrics = []string{"ttft_ms", "ttfat_ms", "e2e_ms", "tpot_ms", "output_tps"}

func (s *SQLite) RunMetricValues(ctx context.Context, runID, metric string) (map[string][]float64, error) {
	if !slices.Contains(DistributionMetrics, metric) {
		return nil, fmt.Errorf("unknown metric %q", metric)
	}
	// metric is whitelisted above, so it is safe to splice into the query.
	rows, err := s.db.QueryContext(ctx, `SELECT cell_id, `+metric+` FROM requests
		WHERE run_id = ? AND warmup = 0 AND status = 'ok' AND `+metric+` IS NOT NULL
		ORDER BY started_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]float64{}
	for rows.Next() {
		var cell string
		var v float64
		if err := rows.Scan(&cell, &v); err != nil {
			return nil, err
		}
		out[cell] = append(out[cell], v)
	}
	return out, rows.Err()
}
