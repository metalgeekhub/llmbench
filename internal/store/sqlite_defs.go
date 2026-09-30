package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/metalgeekhub/llmbench/internal/metrics"
)

// --- goodput & export helpers ---

func (s *SQLite) RunSLOSamples(ctx context.Context, runID string) (map[string][]metrics.SLOSample, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT cell_id, status, ttft_ms, tpot_ms, e2e_ms, output_tps FROM requests
		WHERE run_id = ? AND warmup = 0 AND status IN ('ok', 'error') ORDER BY started_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]metrics.SLOSample{}
	for rows.Next() {
		var (
			cell, status     string
			ttft, tpot, otps sql.NullFloat64
			e2e              float64
		)
		if err := rows.Scan(&cell, &status, &ttft, &tpot, &e2e, &otps); err != nil {
			return nil, err
		}
		out[cell] = append(out[cell], metrics.SLOSample{
			OK: status == "ok", TTFTMs: nullPtr(ttft), TPOTMs: nullPtr(tpot), E2EMs: e2e, OutputTPS: nullPtr(otps),
		})
	}
	return out, rows.Err()
}

func (s *SQLite) EachRunRequest(ctx context.Context, runID string, fn func(Request, *Timeline) error) error {
	rows, err := s.db.QueryContext(ctx, `SELECT `+prefixed("r.", requestColumns)+`, t.data FROM requests r
		LEFT JOIN request_timelines t ON t.request_id = r.id
		WHERE r.run_id = ? ORDER BY r.started_at, r.rowid`, runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		req, err := scanRequest(scannerFunc(func(dest ...any) error {
			return rows.Scan(append(dest, &data)...)
		}))
		if err != nil {
			return err
		}
		var tl *Timeline
		if len(data) > 0 {
			tl = &Timeline{}
			if err := decompressJSON(data, tl); err != nil {
				return fmt.Errorf("timeline %s: %w", req.ID, err)
			}
		}
		if err := fn(req, tl); err != nil {
			return err
		}
	}
	return rows.Err()
}

// scannerFunc adapts a function to the scanner interface.
type scannerFunc func(dest ...any) error

func (f scannerFunc) Scan(dest ...any) error { return f(dest...) }

// prefixed qualifies each comma-separated column with a table alias.
func prefixed(alias, columns string) string {
	cols := strings.Split(columns, ",")
	for i, c := range cols {
		cols[i] = alias + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}

// --- test definitions ---

const definitionColumns = `id, name, description, version, config, created_at, updated_at`

func scanDefinition(sc scanner) (Definition, error) {
	var (
		d                    Definition
		config               string
		createdAt, updatedAt int64
	)
	if err := sc.Scan(&d.ID, &d.Name, &d.Description, &d.Version, &config, &createdAt, &updatedAt); err != nil {
		return Definition{}, mapErr(err)
	}
	d.Config = json.RawMessage(config)
	d.CreatedAt = fromMillis(createdAt)
	d.UpdatedAt = fromMillis(updatedAt)
	return d, nil
}

func (s *SQLite) ListDefinitions(ctx context.Context) ([]Definition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+definitionColumns+` FROM test_definitions ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		d, err := scanDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *SQLite) GetDefinition(ctx context.Context, id string) (Definition, error) {
	return scanDefinition(s.db.QueryRowContext(ctx, `SELECT `+definitionColumns+` FROM test_definitions WHERE id = ?`, id))
}

// SaveDefinition inserts a new definition at version 1, or bumps an existing
// one to the next version. Every version's config is kept.
func (s *SQLite) SaveDefinition(ctx context.Context, d *Definition) error {
	if !json.Valid(d.Config) {
		return fmt.Errorf("definition config is not valid JSON")
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var current int
	err = tx.QueryRowContext(ctx, `SELECT version FROM test_definitions WHERE id = ?`, d.ID).Scan(&current)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		d.Version = 1
		d.CreatedAt = now
		if _, err := tx.ExecContext(ctx, `INSERT INTO test_definitions (`+definitionColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			d.ID, d.Name, d.Description, d.Version, string(d.Config), toMillis(now), toMillis(now)); err != nil {
			return mapErr(err)
		}
	case err != nil:
		return err
	default:
		d.Version = current + 1
		if _, err := tx.ExecContext(ctx, `UPDATE test_definitions SET name = ?, description = ?, version = ?, config = ?,
			updated_at = ? WHERE id = ?`, d.Name, d.Description, d.Version, string(d.Config), toMillis(now), d.ID); err != nil {
			return mapErr(err)
		}
	}
	d.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `INSERT INTO test_definition_versions (definition_id, version, config, created_at)
		VALUES (?, ?, ?, ?)`, d.ID, d.Version, string(d.Config), toMillis(now)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) ListDefinitionVersions(ctx context.Context, id string) ([]DefinitionVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version, config, created_at FROM test_definition_versions
		WHERE definition_id = ? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DefinitionVersion{}
	for rows.Next() {
		var (
			v         DefinitionVersion
			config    string
			createdAt int64
		)
		if err := rows.Scan(&v.Version, &config, &createdAt); err != nil {
			return nil, err
		}
		v.Config = json.RawMessage(config)
		v.CreatedAt = fromMillis(createdAt)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteDefinition(ctx context.Context, id string) error {
	return checkAffected(s.db.ExecContext(ctx, `DELETE FROM test_definitions WHERE id = ?`, id))
}
