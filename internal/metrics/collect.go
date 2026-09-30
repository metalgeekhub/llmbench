package metrics

import (
	"context"
	"strings"
	"time"

	"github.com/metalgeekhub/llmbench/internal/clock"
	"github.com/metalgeekhub/llmbench/internal/providers"
	"github.com/metalgeekhub/llmbench/internal/tokenizer"
)

// Request status values.
const (
	StatusOK       = "ok"
	StatusError    = "error"
	StatusCanceled = "canceled"
)

// Measurement is the outcome of one measured LLM request.
type Measurement struct {
	StartedAt time.Time
	Status    string
	Err       *providers.Error
	Content   string
	Reasoning string
	Result    Result
	// Chunks are the token-bearing chunk timings (for request_timelines).
	Chunks []Chunk
}

// Collect sends req through p, forwards each event to onEvent (may be nil),
// and measures the request. It is the single measurement pipeline used for
// chat and tests alike.
func Collect(ctx context.Context, p providers.Provider, req providers.ChatRequest, onEvent func(providers.Event)) Measurement {
	var (
		content, reasoning strings.Builder
		chunks             []Chunk
		usage              *providers.Usage
		last               time.Time
	)

	start := clock.Now()
	err := p.StreamChat(ctx, req, func(e providers.Event) {
		switch e.Type {
		case providers.EventContent:
			content.WriteString(e.Text)
			chunks = append(chunks, Chunk{At: e.At.Sub(start), Kind: ChunkContent})
			last = e.At
		case providers.EventReasoning:
			reasoning.WriteString(e.Text)
			chunks = append(chunks, Chunk{At: e.At.Sub(start), Kind: ChunkReasoning})
			last = e.At
		case providers.EventUsage:
			usage = e.Usage
			last = e.At
		}
		if onEvent != nil {
			onEvent(e)
		}
	})
	end := clock.Now()
	if err == nil && !last.IsZero() {
		// E2E ends at the final chunk, not when the connection closed.
		end = last
	}

	m := Measurement{
		StartedAt: start,
		Status:    StatusOK,
		Content:   content.String(),
		Reasoning: reasoning.String(),
		Chunks:    chunks,
	}
	if err != nil {
		m.Err = providers.AsError(err)
		m.Status = StatusError
		if m.Err.Type == providers.ErrCanceled {
			m.Status = StatusCanceled
		}
	}

	in := Input{E2E: end.Sub(start), Chunks: chunks}
	if usage != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		in.InputTokens = usage.InputTokens
		in.OutputTokens = usage.OutputTokens
		in.ReasoningTokens = usage.ReasoningTokens
		in.CachedTokens = usage.CachedTokens
	} else if len(chunks) > 0 || err == nil {
		prompt := make([]string, len(req.Messages))
		for i, msg := range req.Messages {
			prompt[i] = msg.Content
		}
		in.InputTokens = tokenizer.CountMessages(prompt)
		in.ReasoningTokens = tokenizer.Count(m.Reasoning)
		in.OutputTokens = tokenizer.Count(m.Content) + in.ReasoningTokens
		in.TokensEstimated = true
	}
	m.Result = Compute(in)
	return m
}
