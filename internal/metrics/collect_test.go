package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/metalgeekhub/llmbench/internal/providers"
)

// fakeProvider replays scripted events with fixed offsets from call time.
type fakeProvider struct {
	events []providers.Event // At is interpreted as an offset from time zero
	err    error
}

func (f *fakeProvider) ListModels(context.Context) ([]string, error) { return nil, nil }

func (f *fakeProvider) StreamChat(_ context.Context, _ providers.ChatRequest, emit func(providers.Event)) error {
	base := time.Now()
	for _, e := range f.events {
		e.At = base.Add(time.Duration(e.At.UnixNano()))
		emit(e)
	}
	return f.err
}

func at(ms int) time.Time { return time.Unix(0, int64(ms)*int64(time.Millisecond)) }

func TestCollectWithUsage(t *testing.T) {
	p := &fakeProvider{events: []providers.Event{
		{Type: providers.EventReasoning, Text: "think", At: at(100)},
		{Type: providers.EventContent, Text: "Hel", At: at(300)},
		{Type: providers.EventContent, Text: "lo", At: at(350)},
		{Type: providers.EventUsage, Usage: &providers.Usage{InputTokens: 10, OutputTokens: 5, ReasoningTokens: 2}, At: at(400)},
	}}
	var forwarded int
	m := Collect(context.Background(), p, providers.ChatRequest{}, func(providers.Event) { forwarded++ })

	if forwarded != 4 {
		t.Errorf("forwarded %d events", forwarded)
	}
	if m.Status != StatusOK || m.Err != nil {
		t.Fatalf("status = %s err = %v", m.Status, m.Err)
	}
	if m.Content != "Hello" || m.Reasoning != "think" {
		t.Errorf("content=%q reasoning=%q", m.Content, m.Reasoning)
	}
	r := m.Result
	if r.TokensEstimated || r.InputTokens != 10 || r.OutputTokens != 5 || r.ReasoningTokens != 2 {
		t.Errorf("tokens = %+v", r)
	}
	if r.TTFTMs == nil || *r.TTFTMs < 100 || *r.TTFTMs > 150 {
		t.Errorf("TTFT = %v", r.TTFTMs)
	}
	if r.TTFATMs == nil || *r.TTFATMs < 300 || *r.TTFATMs > 350 {
		t.Errorf("TTFAT = %v", r.TTFATMs)
	}
	// E2E ends at the last chunk (usage at 400ms), not at connection close.
	if r.E2EMs < 400 || r.E2EMs > 450 {
		t.Errorf("E2E = %v", r.E2EMs)
	}
	if len(m.Chunks) != 3 {
		t.Errorf("chunks = %d", len(m.Chunks))
	}
}

func TestCollectEstimatesTokens(t *testing.T) {
	p := &fakeProvider{events: []providers.Event{
		{Type: providers.EventContent, Text: "hello world", At: at(10)},
	}}
	m := Collect(context.Background(), p, providers.ChatRequest{
		Messages: []providers.Message{{Role: "user", Content: "hello world"}},
	}, nil)
	if !m.Result.TokensEstimated || m.Result.OutputTokens != 2 || m.Result.InputTokens == 0 {
		t.Errorf("estimated tokens = %+v", m.Result)
	}
}

func TestCollectErrors(t *testing.T) {
	m := Collect(context.Background(), &fakeProvider{err: &providers.Error{Type: providers.ErrRateLimit, HTTPStatus: 429}}, providers.ChatRequest{}, nil)
	if m.Status != StatusError || m.Err.HTTPStatus != 429 || m.Result.TTFTMs != nil || m.Result.OutputTokens != 0 {
		t.Errorf("error measurement = %+v", m)
	}

	m = Collect(context.Background(), &fakeProvider{err: context.Canceled, events: []providers.Event{
		{Type: providers.EventContent, Text: "partial", At: at(5)},
	}}, providers.ChatRequest{}, nil)
	if m.Status != StatusCanceled || m.Content != "partial" || m.Result.TTFTMs == nil {
		t.Errorf("canceled measurement = %+v", m)
	}
}
