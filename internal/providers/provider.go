// Package providers contains adapters that talk to LLM endpoints and emit
// normalized, timestamped streaming events.
package providers

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Supported source types.
const (
	TypeOpenAI = "openai"
)

// SupportedTypes lists the source types that have an adapter.
var SupportedTypes = []string{TypeOpenAI}

// DefaultTimeout bounds a whole request (including streaming) when the
// source does not set one.
const DefaultTimeout = 10 * time.Minute

// Config is what an adapter needs to reach an endpoint.
type Config struct {
	Type          string
	BaseURL       string
	APIKey        string
	Headers       map[string]string
	ExtraBody     map[string]any
	Timeout       time.Duration
	TLSSkipVerify bool
}

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is a provider-neutral chat completion request.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Temperature *float64
	MaxTokens   *int
	// ExtraBody is deep-merged over the source's extra body.
	ExtraBody map[string]any
}

// EventType identifies a normalized stream event.
type EventType int

const (
	EventContent EventType = iota
	EventReasoning
	EventUsage
)

// Usage is provider-reported token usage.
type Usage struct {
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
	CachedTokens    int
}

// Event is emitted for every meaningful piece of a streamed response. At is
// the receive time (carries a monotonic clock reading).
type Event struct {
	Type  EventType
	Text  string
	Usage *Usage
	At    time.Time
}

// Provider is implemented by every adapter.
type Provider interface {
	// ListModels returns model IDs available on the endpoint.
	ListModels(ctx context.Context) ([]string, error)
	// StreamChat sends req and calls emit for each event in order. It returns
	// when the stream is complete; a non-nil error is always an *Error.
	StreamChat(ctx context.Context, req ChatRequest, emit func(Event)) error
}

// New constructs the adapter for cfg.Type.
func New(cfg Config) (Provider, error) {
	switch cfg.Type {
	case TypeOpenAI:
		return newOpenAI(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported source type %q", cfg.Type)
	}
}

// Error classes, used for error-rate breakdowns.
const (
	ErrTimeout    = "timeout"
	ErrConnection = "connection"
	ErrCanceled   = "canceled"
	ErrRateLimit  = "http_429"
	ErrHTTP4xx    = "http_4xx"
	ErrHTTP5xx    = "http_5xx"
	ErrStream     = "stream"
	ErrRequest    = "request"
)

// Error is a classified request failure.
type Error struct {
	Type       string
	HTTPStatus int
	Message    string
}

func (e *Error) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("%s (HTTP %d): %s", e.Type, e.HTTPStatus, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// AsError converts any error into a classified *Error.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	switch {
	case errors.Is(err, context.Canceled):
		return &Error{Type: ErrCanceled, Message: "request canceled"}
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Type: ErrTimeout, Message: "request timed out"}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &Error{Type: ErrTimeout, Message: err.Error()}
	}
	var oe *net.OpError
	if errors.As(err, &oe) {
		return &Error{Type: ErrConnection, Message: err.Error()}
	}
	return &Error{Type: ErrConnection, Message: err.Error()}
}

func httpStatusError(status int, body string) *Error {
	t := ErrHTTP4xx
	switch {
	case status == http.StatusTooManyRequests:
		t = ErrRateLimit
	case status >= 500:
		t = ErrHTTP5xx
	}
	if body == "" {
		body = http.StatusText(status)
	}
	return &Error{Type: t, HTTPStatus: status, Message: body}
}

func newHTTPClient(cfg Config) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.TLSSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // user opt-in per source
	}
	// Timeouts are applied per request through the context so that
	// long streaming responses are bounded by the source timeout only.
	return &http.Client{Transport: tr}
}

// MergeJSON deep-merges src into dst (maps are merged recursively, other
// values replaced) and returns dst. A nil dst is allocated.
func MergeJSON(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				dst[k] = MergeJSON(dm, sm)
				continue
			}
			dst[k] = MergeJSON(nil, sm)
			continue
		}
		dst[k] = v
	}
	return dst
}
