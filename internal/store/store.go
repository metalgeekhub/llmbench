// Package store defines the persistence interface and its data types. The
// only implementation for v1 is SQLite (see sqlite.go).
package store

import (
	"context"
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

	ListSessions(ctx context.Context) ([]ChatSession, error)
	GetSession(ctx context.Context, id string) (ChatSession, error)
	CreateSession(ctx context.Context, s *ChatSession) error
	UpdateSession(ctx context.Context, s *ChatSession) error
	DeleteSession(ctx context.Context, id string) error

	ListMessages(ctx context.Context, sessionID string) ([]ChatMessage, error)
	AddMessage(ctx context.Context, m *ChatMessage) error

	// SaveRequest persists a request and, if tl is non-nil, its timeline.
	SaveRequest(ctx context.Context, r *Request, tl *Timeline) error
	GetRequest(ctx context.Context, id string) (Request, error)
	ListRequests(ctx context.Context, f RequestFilter) ([]Request, int, error)
	GetTimeline(ctx context.Context, requestID string) (*Timeline, error)
	// ListRequestModels returns the distinct models seen in requests.
	ListRequestModels(ctx context.Context) ([]string, error)

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

// ChatParams are the per-session generation parameters.
type ChatParams struct {
	Temperature  *float64       `json:"temperature"`
	MaxTokens    *int           `json:"max_tokens"`
	SystemPrompt string         `json:"system_prompt"`
	ExtraBody    map[string]any `json:"extra_body"`
}

type ChatSession struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	SourceID  string     `json:"source_id"`
	Model     string     `json:"model"`
	Params    ChatParams `json:"params"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type ChatMessage struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Reasoning string    `json:"reasoning"`
	SourceID  string    `json:"source_id"`
	Model     string    `json:"model"`
	RequestID string    `json:"request_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Request kinds.
const (
	KindChat = "chat"
)

// Request is one measured LLM request.
type Request struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	SourceID     string         `json:"source_id"`
	SourceName   string         `json:"source_name"`
	Model        string         `json:"model"`
	SessionID    string         `json:"session_id"`
	Status       string         `json:"status"`
	HTTPStatus   int            `json:"http_status"`
	ErrorType    string         `json:"error_type"`
	ErrorMessage string         `json:"error_message"`
	StartedAt    time.Time      `json:"started_at"`
	Metrics      metrics.Result `json:"metrics"`
	Params       map[string]any `json:"params"`
}

// RequestFilter narrows ListRequests. Zero values mean "any".
type RequestFilter struct {
	Kind      string
	SourceID  string
	Model     string
	Status    string
	SessionID string
	Limit     int
	Offset    int
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
