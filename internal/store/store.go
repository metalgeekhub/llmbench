// Package store defines the persistence interface and its data types. The
// only implementation for v1 is SQLite (see sqlite.go).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/metalgeekhub/llmbench/internal/metrics"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a unique constraint is violated.
var ErrConflict = errors.New("already exists")

// Store is the persistence interface. Implementations must be safe for
// concurrent use.
type Store interface {
	ListSources(ctx context.Context) ([]Source, error)
	GetSource(ctx context.Context, id string) (Source, error)
	CreateSource(ctx context.Context, s *Source) error
	UpdateSource(ctx context.Context, s *Source) error
	DeleteSource(ctx context.Context, id string) error

	// ListSessions returns sessions with the given compare ID; "" lists
	// plain chats (not part of a comparison).
	ListSessions(ctx context.Context, compareID string) ([]ChatSession, error)
	GetSession(ctx context.Context, id string) (ChatSession, error)
	CreateSession(ctx context.Context, s *ChatSession) error
	UpdateSession(ctx context.Context, s *ChatSession) error
	DeleteSession(ctx context.Context, id string) error
	// ListCompares returns comparison groups, most recently updated first.
	ListCompares(ctx context.Context) ([]CompareGroup, error)
	// DeleteCompare removes all sessions of a comparison.
	DeleteCompare(ctx context.Context, compareID string) error

	ListProfiles(ctx context.Context) ([]Profile, error)
	GetProfile(ctx context.Context, id string) (Profile, error)
	CreateProfile(ctx context.Context, p *Profile) error
	UpdateProfile(ctx context.Context, p *Profile) error
	DeleteProfile(ctx context.Context, id string) error

	ListMessages(ctx context.Context, sessionID string) ([]ChatMessage, error)
	AddMessage(ctx context.Context, m *ChatMessage) error

	// SaveRequest persists a request and, if tl is non-nil, its timeline.
	SaveRequest(ctx context.Context, r *Request, tl *Timeline) error
	// SaveRequests persists a batch of requests in one transaction.
	SaveRequests(ctx context.Context, batch []RequestRecord) error
	GetRequest(ctx context.Context, id string) (Request, error)
	ListRequests(ctx context.Context, f RequestFilter) ([]Request, int, error)
	GetTimeline(ctx context.Context, requestID string) (*Timeline, error)
	// ListRequestModels returns the distinct models seen in requests.
	ListRequestModels(ctx context.Context) ([]string, error)

	// CreateRun persists a run and its cells.
	CreateRun(ctx context.Context, r *TestRun, cells []RunCell) error
	UpdateRun(ctx context.Context, r *TestRun) error
	UpdateCell(ctx context.Context, c *RunCell) error
	GetRun(ctx context.Context, id string) (TestRun, error)
	ListRuns(ctx context.Context, limit int) ([]TestRun, error)
	ListCells(ctx context.Context, runID string) ([]RunCell, error)
	// DeleteRun removes a run, its cells, and its requests.
	DeleteRun(ctx context.Context, id string) error
	// MarkInterruptedRuns flags runs left running by a previous process.
	MarkInterruptedRuns(ctx context.Context) (int, error)
	// RunMetricValues returns a metric of every successful, measured
	// (non-warm-up) request of a run, grouped by cell ID. metric is one of
	// DistributionMetrics.
	RunMetricValues(ctx context.Context, runID, metric string) (map[string][]float64, error)
	// RunSLOSamples returns, per cell, the SLO-relevant metrics of every
	// completed (ok or failed), measured request of a run.
	RunSLOSamples(ctx context.Context, runID string) (map[string][]metrics.SLOSample, error)
	// UpdateRunConfig replaces a run's stored config (e.g. new SLO targets).
	UpdateRunConfig(ctx context.Context, runID string, config json.RawMessage) error
	// EachRunRequest calls fn for every request of a run, in start order,
	// with its timeline (nil if none).
	EachRunRequest(ctx context.Context, runID string, fn func(Request, *Timeline) error) error
	// ImportRun stores a complete run from an export. ErrConflict if the run
	// already exists.
	ImportRun(ctx context.Context, run *TestRun, cells []RunCell, requests []RequestRecord) error

	ListDefinitions(ctx context.Context) ([]Definition, error)
	GetDefinition(ctx context.Context, id string) (Definition, error)
	// SaveDefinition creates a definition (version 1) or, if it exists, adds
	// a new version with its config.
	SaveDefinition(ctx context.Context, d *Definition) error
	ListDefinitionVersions(ctx context.Context, id string) ([]DefinitionVersion, error)
	DeleteDefinition(ctx context.Context, id string) error

	ListComparisons(ctx context.Context) ([]Comparison, error)
	GetComparison(ctx context.Context, id string) (Comparison, error)
	CreateComparison(ctx context.Context, c *Comparison) error
	UpdateComparison(ctx context.Context, c *Comparison) error
	DeleteComparison(ctx context.Context, id string) error

	Close() error
}

// Source is a UI-managed endpoint. Secrets are stored encrypted; the store
// never sees plaintext.
type Source struct {
	ID            string
	Name          string
	Type          string
	BaseURL       string
	APIKeyEnc     string
	HeadersEnc    string
	ExtraBody     map[string]any
	Timeout       time.Duration
	TLSSkipVerify bool
	Models        []string
	ModelsAuto    bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ChatParams are generation parameters, used by chat sessions and model
// profiles.
type ChatParams struct {
	Temperature  *float64       `json:"temperature"`
	MaxTokens    *int           `json:"max_tokens"`
	SystemPrompt string         `json:"system_prompt"`
	ExtraBody    map[string]any `json:"extra_body"`
	// Thinking is the normalized reasoning control: "" (server default),
	// "off", "low", "medium" or "high".
	Thinking string `json:"thinking"`
	// ThinkingStyle selects how Thinking is sent: "reasoning_effort" or
	// "chat_template_kwargs" (see providers.Thinking).
	ThinkingStyle string `json:"thinking_style"`
}

type ChatSession struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	SourceID string     `json:"source_id"`
	Model    string     `json:"model"`
	Params   ChatParams `json:"params"`
	// ProfileID records the profile the settings were taken from, if any.
	ProfileID string `json:"profile_id"`
	// CompareID groups the columns of a side-by-side comparison.
	CompareID string    `json:"compare_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CompareGroup is one side-by-side comparison: a set of sessions that
// receive the same prompts.
type CompareGroup struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Sessions  []ChatSession `json:"sessions"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// Profile is a model on a source plus default parameters. Several profiles
// of one model (e.g. thinking on/off) can be compared like different models.
type Profile struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	SourceID  string     `json:"source_id"`
	Model     string     `json:"model"`
	Params    ChatParams `json:"params"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type ChatMessage struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Reasoning string `json:"reasoning"`
	SourceID  string `json:"source_id"`
	Model     string `json:"model"`
	RequestID string `json:"request_id"`
	// SyntheticTokens > 0 marks a synthetic prompt of that many tokens.
	SyntheticTokens int       `json:"synthetic_tokens"`
	CreatedAt       time.Time `json:"created_at"`
}

// Request kinds.
const (
	KindChat = "chat"
	KindTest = "test"
)

// Request is one measured LLM request.
type Request struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	SourceID     string         `json:"source_id"`
	SourceName   string         `json:"source_name"`
	Model        string         `json:"model"`
	SessionID    string         `json:"session_id"`
	RunID        string         `json:"run_id"`
	CellID       string         `json:"cell_id"`
	Warmup       bool           `json:"warmup"`
	Status       string         `json:"status"`
	HTTPStatus   int            `json:"http_status"`
	ErrorType    string         `json:"error_type"`
	ErrorMessage string         `json:"error_message"`
	StartedAt    time.Time      `json:"started_at"`
	Metrics      metrics.Result `json:"metrics"`
	Params       map[string]any `json:"params"`
}

// RequestRecord is a request with its optional timeline, for batch writes.
type RequestRecord struct {
	Request  Request
	Timeline *Timeline
}

// RequestFilter narrows ListRequests. Zero values mean "any".
type RequestFilter struct {
	Kind      string
	SourceID  string
	Model     string
	Status    string
	SessionID string
	RunID     string
	CellID    string
	Limit     int
	Offset    int
}

// Comparison is a saved side-by-side view of benchmark runs. Settings holds
// UI state (chart axis, metric, filters) as opaque JSON.
type Comparison struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	RunIDs    []string        `json:"run_ids"`
	Settings  json.RawMessage `json:"settings"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Run and cell statuses.
const (
	StatusPending     = "pending" // cell not started yet
	StatusRunning     = "running"
	StatusCompleted   = "completed"
	StatusStopped     = "stopped"     // stopped by the user
	StatusAborted     = "aborted"     // stop condition hit (error rate)
	StatusFailed      = "failed"      // could not run (e.g. source unavailable)
	StatusSkipped     = "skipped"     // cell never ran because the run ended early
	StatusInterrupted = "interrupted" // process exited while running
)

// TestRun is one execution of a benchmark. Config is the runner's
// configuration as JSON; the store does not interpret it.
type TestRun struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	Config     json.RawMessage `json:"config"`
	Error      string          `json:"error"`
	CreatedAt  time.Time       `json:"created_at"`
	StartedAt  *time.Time      `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at"`
	// Timeline is the per-second live metrics series (JSON), saved when the
	// run finishes. ListRuns leaves it empty.
	Timeline json.RawMessage `json:"timeline,omitempty"`
	// DefinitionID/DefinitionVersion link the run to the saved definition it
	// was started from (empty for ad-hoc runs).
	DefinitionID      string `json:"definition_id"`
	DefinitionVersion int    `json:"definition_version"`
	// ImportedAt is set on runs imported from an export file.
	ImportedAt *time.Time `json:"imported_at"`
}

// RunCell is one target x concurrency combination within a run.
type RunCell struct {
	ID          string `json:"id"`
	RunID       string `json:"run_id"`
	Index       int    `json:"index"`
	SourceID    string `json:"source_id"`
	SourceName  string `json:"source_name"`
	Model       string `json:"model"`
	ProfileID   string `json:"profile_id"`
	ProfileName string `json:"profile_name"`
	// ContextTokens is the synthetic input length; 0 when not swept.
	ContextTokens int `json:"context_tokens"`
	// Thinking is the cell's thinking level override; "" when not swept.
	Thinking string `json:"thinking"`
	// Concurrency is the number of virtual users (closed loop) or the
	// in-flight cap (open loop).
	Concurrency int `json:"concurrency"`
	// ArrivalRate is the open-loop target rate in requests/s; nil for
	// closed-loop cells.
	ArrivalRate *float64 `json:"arrival_rate"`
	// ScheduleLagP95Ms (open loop) is how late requests were sent versus
	// their schedule; high values mean the client could not keep up.
	ScheduleLagP95Ms *float64 `json:"schedule_lag_p95_ms"`
	// SampleOutput/SampleReasoning are the first successful response.
	SampleOutput    string `json:"sample_output"`
	SampleReasoning string `json:"sample_reasoning"`
	Status          string `json:"status"`
	// Summary is set once the cell has finished.
	Summary *metrics.Aggregate `json:"summary"`
	// ClientCPUPct is the load generator's own CPU use while measuring.
	ClientCPUPct *float64   `json:"client_cpu_pct"`
	Error        string     `json:"error"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

// Timeline holds per-chunk receive offsets (ms from request start) and the
// kind of each chunk ('c' content, 'r' reasoning).
type Timeline struct {
	OffsetsMs []float64 `json:"t"`
	Kinds     string    `json:"k"`
}

// TimelineFromChunks converts measured chunks to a Timeline.
func TimelineFromChunks(chunks []metrics.Chunk) *Timeline {
	if len(chunks) == 0 {
		return nil
	}
	tl := &Timeline{OffsetsMs: make([]float64, len(chunks))}
	kinds := make([]byte, len(chunks))
	for i, c := range chunks {
		tl.OffsetsMs[i] = float64(c.At.Microseconds()) / 1000
		kinds[i] = byte(c.Kind)
	}
	tl.Kinds = string(kinds)
	return tl
}

// Definition is a saved, reusable benchmark configuration. Config is the
// runner's config as JSON. Every save creates a new version.
type Definition struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Version     int             `json:"version"`
	Config      json.RawMessage `json:"config"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// DefinitionVersion is one saved version of a definition's config.
type DefinitionVersion struct {
	Version   int             `json:"version"`
	Config    json.RawMessage `json:"config"`
	CreatedAt time.Time       `json:"created_at"`
}
