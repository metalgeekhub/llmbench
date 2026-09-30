package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go)

	"github.com/metalgeekhub/llmbench/migrations"
)

// SQLite implements Store on a single SQLite database file.
type SQLite struct {
	db *sql.DB
}

var _ Store = (*SQLite)(nil)

// OpenSQLite opens (creating if needed) the database at path and applies
// pending migrations.
func OpenSQLite(ctx context.Context, path string) (*SQLite, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening database %s: %w", path, err)
	}
	s := &SQLite{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) Close() error { return s.db.Close() }

func (s *SQLite) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()

	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		version := strings.TrimSuffix(f, ".sql")
		if applied[version] {
			continue
		}
		body, err := migrations.FS.ReadFile(f)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("applying migration %s: %w", f, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, toMillis(time.Now())); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// --- helpers ---

func toMillis(t time.Time) int64 { return t.UnixMilli() }

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func fromJSON(s string, v any) error {
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), v)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return err
}

func checkAffected(res sql.Result, err error) error {
	if err != nil {
		return mapErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

// --- sources ---

const sourceColumns = `id, name, type, base_url, api_key_enc, headers_enc, extra_body,
	timeout_ms, tls_skip_verify, models, models_auto, created_at, updated_at`

func scanSource(sc scanner) (Source, error) {
	var (
		src                  Source
		extra, models        string
		timeoutMs            int64
		tls, auto            int
		createdAt, updatedAt int64
	)
	if err := sc.Scan(&src.ID, &src.Name, &src.Type, &src.BaseURL, &src.APIKeyEnc, &src.HeadersEnc,
		&extra, &timeoutMs, &tls, &models, &auto, &createdAt, &updatedAt); err != nil {
		return Source{}, mapErr(err)
	}
	if err := fromJSON(extra, &src.ExtraBody); err != nil {
		return Source{}, fmt.Errorf("source %s extra_body: %w", src.ID, err)
	}
	if err := fromJSON(models, &src.Models); err != nil {
		return Source{}, fmt.Errorf("source %s models: %w", src.ID, err)
	}
	src.Timeout = time.Duration(timeoutMs) * time.Millisecond
	src.TLSSkipVerify = tls != 0
	src.ModelsAuto = auto != 0
	src.CreatedAt = fromMillis(createdAt)
	src.UpdatedAt = fromMillis(updatedAt)
	return src, nil
}

func (s *SQLite) ListSources(ctx context.Context) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sourceColumns+` FROM sources ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

func (s *SQLite) GetSource(ctx context.Context, id string) (Source, error) {
	return scanSource(s.db.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM sources WHERE id = ?`, id))
}

func (s *SQLite) CreateSource(ctx context.Context, src *Source) error {
	now := time.Now().UTC()
	src.CreatedAt, src.UpdatedAt = now, now
	_, err := s.db.ExecContext(ctx, `INSERT INTO sources (`+sourceColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		src.ID, src.Name, src.Type, src.BaseURL, src.APIKeyEnc, src.HeadersEnc, toJSON(orEmptyMap(src.ExtraBody)),
		src.Timeout.Milliseconds(), boolInt(src.TLSSkipVerify), toJSON(orEmptySlice(src.Models)), boolInt(src.ModelsAuto),
		toMillis(now), toMillis(now))
	return mapErr(err)
}

func (s *SQLite) UpdateSource(ctx context.Context, src *Source) error {
	src.UpdatedAt = time.Now().UTC()
	return checkAffected(s.db.ExecContext(ctx, `UPDATE sources SET
		name = ?, type = ?, base_url = ?, api_key_enc = ?, headers_enc = ?, extra_body = ?,
		timeout_ms = ?, tls_skip_verify = ?, models = ?, models_auto = ?, updated_at = ?
		WHERE id = ?`,
		src.Name, src.Type, src.BaseURL, src.APIKeyEnc, src.HeadersEnc, toJSON(orEmptyMap(src.ExtraBody)),
		src.Timeout.Milliseconds(), boolInt(src.TLSSkipVerify), toJSON(orEmptySlice(src.Models)), boolInt(src.ModelsAuto),
		toMillis(src.UpdatedAt), src.ID))
}

func (s *SQLite) DeleteSource(ctx context.Context, id string) error {
	return checkAffected(s.db.ExecContext(ctx, `DELETE FROM sources WHERE id = ?`, id))
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// --- chat sessions & messages ---

const sessionColumns = `id, title, source_id, model, params, created_at, updated_at`

func scanSession(sc scanner) (ChatSession, error) {
	var (
		cs                   ChatSession
		params               string
		createdAt, updatedAt int64
	)
	if err := sc.Scan(&cs.ID, &cs.Title, &cs.SourceID, &cs.Model, &params, &createdAt, &updatedAt); err != nil {
		return ChatSession{}, mapErr(err)
	}
	if err := fromJSON(params, &cs.Params); err != nil {
		return ChatSession{}, fmt.Errorf("session %s params: %w", cs.ID, err)
	}
	cs.CreatedAt = fromMillis(createdAt)
	cs.UpdatedAt = fromMillis(updatedAt)
	return cs, nil
}

func (s *SQLite) ListSessions(ctx context.Context) ([]ChatSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionColumns+` FROM chat_sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatSession{}
	for rows.Next() {
		cs, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

func (s *SQLite) GetSession(ctx context.Context, id string) (ChatSession, error) {
	return scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM chat_sessions WHERE id = ?`, id))
}

func (s *SQLite) CreateSession(ctx context.Context, cs *ChatSession) error {
	now := time.Now().UTC()
	cs.CreatedAt, cs.UpdatedAt = now, now
	_, err := s.db.ExecContext(ctx, `INSERT INTO chat_sessions (`+sessionColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cs.ID, cs.Title, cs.SourceID, cs.Model, toJSON(cs.Params), toMillis(now), toMillis(now))
	return mapErr(err)
}

func (s *SQLite) UpdateSession(ctx context.Context, cs *ChatSession) error {
	cs.UpdatedAt = time.Now().UTC()
	return checkAffected(s.db.ExecContext(ctx, `UPDATE chat_sessions SET
		title = ?, source_id = ?, model = ?, params = ?, updated_at = ? WHERE id = ?`,
		cs.Title, cs.SourceID, cs.Model, toJSON(cs.Params), toMillis(cs.UpdatedAt), cs.ID))
}

func (s *SQLite) DeleteSession(ctx context.Context, id string) error {
	return checkAffected(s.db.ExecContext(ctx, `DELETE FROM chat_sessions WHERE id = ?`, id))
}

func (s *SQLite) ListMessages(ctx context.Context, sessionID string) ([]ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_id, role, content, reasoning, source_id, model,
		request_id, created_at FROM chat_messages WHERE session_id = ? ORDER BY created_at, rowid`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatMessage{}
	for rows.Next() {
		var m ChatMessage
		var createdAt int64
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Reasoning, &m.SourceID, &m.Model,
			&m.RequestID, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = fromMillis(createdAt)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *SQLite) AddMessage(ctx context.Context, m *ChatMessage) error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO chat_messages (id, session_id, role, content, reasoning,
		source_id, model, request_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.SessionID, m.Role, m.Content, m.Reasoning, m.SourceID, m.Model, m.RequestID, toMillis(m.CreatedAt))
	return mapErr(err)
}

// --- requests ---

const requestColumns = `id, kind, source_id, source_name, model, session_id, status, http_status,
	error_type, error_message, started_at, ttft_ms, ttfat_ms, e2e_ms, tpot_ms, output_tps, prefill_tps,
	input_tokens, output_tokens, reasoning_tokens, cached_tokens, tokens_estimated, chunk_count, itl, params`

func scanRequest(sc scanner) (Request, error) {
	var (
		r                                     Request
		startedAt                             int64
		ttft, ttfat, tpot, outTPS, prefillTPS sql.NullFloat64
		estimated                             int
		itl, params                           string
	)
	m := &r.Metrics
	if err := sc.Scan(&r.ID, &r.Kind, &r.SourceID, &r.SourceName, &r.Model, &r.SessionID, &r.Status, &r.HTTPStatus,
		&r.ErrorType, &r.ErrorMessage, &startedAt, &ttft, &ttfat, &m.E2EMs, &tpot, &outTPS, &prefillTPS,
		&m.InputTokens, &m.OutputTokens, &m.ReasoningTokens, &m.CachedTokens, &estimated, &m.ChunkCount,
		&itl, &params); err != nil {
		return Request{}, mapErr(err)
	}
	r.StartedAt = fromMillis(startedAt)
	m.TTFTMs, m.TTFATMs, m.TPOTMs = nullPtr(ttft), nullPtr(ttfat), nullPtr(tpot)
	m.OutputTPS, m.PrefillTPS = nullPtr(outTPS), nullPtr(prefillTPS)
	m.TokensEstimated = estimated != 0
	if err := fromJSON(itl, &m.ITL); err != nil {
		return Request{}, fmt.Errorf("request %s itl: %w", r.ID, err)
	}
	if err := fromJSON(params, &r.Params); err != nil {
		return Request{}, fmt.Errorf("request %s params: %w", r.ID, err)
	}
	return r, nil
}

func nullPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	return &n.Float64
}

func (s *SQLite) SaveRequest(ctx context.Context, r *Request, tl *Timeline) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	m := r.Metrics
	if _, err := tx.ExecContext(ctx, `INSERT INTO requests (`+requestColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Kind, r.SourceID, r.SourceName, r.Model, r.SessionID, r.Status, r.HTTPStatus,
		r.ErrorType, r.ErrorMessage, toMillis(r.StartedAt), m.TTFTMs, m.TTFATMs, m.E2EMs, m.TPOTMs,
		m.OutputTPS, m.PrefillTPS, m.InputTokens, m.OutputTokens, m.ReasoningTokens, m.CachedTokens,
		boolInt(m.TokensEstimated), m.ChunkCount, toJSON(m.ITL), toJSON(orEmptyMap(r.Params))); err != nil {
		return mapErr(err)
	}
	if tl != nil {
		data, err := compressJSON(tl)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO request_timelines (request_id, data) VALUES (?, ?)`,
			r.ID, data); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) GetRequest(ctx context.Context, id string) (Request, error) {
	return scanRequest(s.db.QueryRowContext(ctx, `SELECT `+requestColumns+` FROM requests WHERE id = ?`, id))
}

func (s *SQLite) ListRequests(ctx context.Context, f RequestFilter) ([]Request, int, error) {
	var where []string
	var args []any
	add := func(col, val string) {
		if val != "" {
			where = append(where, col+" = ?")
			args = append(args, val)
		}
	}
	add("kind", f.Kind)
	add("source_id", f.SourceID)
	add("model", f.Model)
	add("status", f.Status)
	add("session_id", f.SessionID)
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM requests`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+requestColumns+` FROM requests`+clause+
		` ORDER BY started_at DESC, rowid DESC LIMIT ? OFFSET ?`, append(args, limit, max(f.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (s *SQLite) GetTimeline(ctx context.Context, requestID string) (*Timeline, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM request_timelines WHERE request_id = ?`, requestID).Scan(&data)
	if err != nil {
		return nil, mapErr(err)
	}
	var tl Timeline
	if err := decompressJSON(data, &tl); err != nil {
		return nil, fmt.Errorf("timeline %s: %w", requestID, err)
	}
	return &tl, nil
}

func (s *SQLite) ListRequestModels(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT model FROM requests ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func compressJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(v); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressJSON(data []byte, v any) error {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer zr.Close()
	b, err := io.ReadAll(zr)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
