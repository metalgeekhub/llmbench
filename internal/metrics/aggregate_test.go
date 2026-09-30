package metrics

import (
	"testing"
	"time"

	"github.com/metalgeekhub/llmbench/internal/providers"
)

func okMeasurement(ttftMs, e2eMs float64, out int, gapsMs ...float64) Measurement {
	chunks := []Chunk{{At: msd(ttftMs), Kind: ChunkContent}}
	at := ttftMs
	for _, g := range gapsMs {
		at += g
		chunks = append(chunks, Chunk{At: msd(at), Kind: ChunkContent})
	}
	return Measurement{
		Status: StatusOK,
		Chunks: chunks,
		Result: Compute(Input{E2E: msd(e2eMs), Chunks: chunks, InputTokens: 100, OutputTokens: out}),
	}
}

func TestNewAggregate(t *testing.T) {
	ms := []Measurement{
		okMeasurement(100, 300, 11, 10, 30),
		okMeasurement(200, 500, 31, 20),
		{Status: StatusError, Err: &providers.Error{Type: providers.ErrRateLimit}},
		{Status: StatusError, Err: &providers.Error{Type: providers.ErrRateLimit}},
		{Status: StatusCanceled, Err: &providers.Error{Type: providers.ErrCanceled}},
	}
	ms[0].Result.ReasoningTokens = 5
	a := NewAggregate(ms, 2*time.Second)
	if a.ReasoningTokens != 5 {
		t.Errorf("reasoning tokens = %d", a.ReasoningTokens)
	}

	if a.Requests != 4 || a.Succeeded != 2 || a.Failed != 2 || a.Canceled != 1 {
		t.Errorf("counts = %+v", a)
	}
	if !near(a.ErrorRate, 0.5) || a.ErrorsByType[providers.ErrRateLimit] != 2 {
		t.Errorf("errors = %v %v", a.ErrorRate, a.ErrorsByType)
	}
	// 2 successful requests over 2s; (11 + 31) output tokens over 2s.
	if !near(a.RequestThroughput, 1) || !near(a.OutputThroughput, 21) {
		t.Errorf("throughput = %v req/s, %v tok/s", a.RequestThroughput, a.OutputThroughput)
	}
	if a.InputTokens != 200 || a.OutputTokens != 42 {
		t.Errorf("tokens = %d / %d", a.InputTokens, a.OutputTokens)
	}
	// Latencies only from successful requests.
	if a.TTFT.Count != 2 || !near(a.TTFT.Min, 100) || !near(a.TTFT.Max, 200) || !near(a.TTFT.Mean, 150) {
		t.Errorf("TTFT = %+v", a.TTFT)
	}
	if a.E2E.Count != 2 || !near(a.E2E.P50, 400) {
		t.Errorf("E2E = %+v", a.E2E)
	}
	// TPOT: (300-100)/10 = 20, (500-200)/30 = 10.
	if !near(a.TPOT.Min, 10) || !near(a.TPOT.Max, 20) {
		t.Errorf("TPOT = %+v", a.TPOT)
	}
	// ITL pools all gaps: 10, 30, 20.
	if a.ITL.Count != 3 || !near(a.ITL.P50, 20) || !near(a.ITL.Max, 30) {
		t.Errorf("ITL = %+v", a.ITL)
	}
}

func TestNewAggregateEmpty(t *testing.T) {
	a := NewAggregate(nil, 0)
	if a.Requests != 0 || a.ErrorRate != 0 || a.RequestThroughput != 0 || a.TTFT.Count != 0 {
		t.Errorf("empty aggregate = %+v", a)
	}
	if a.ErrorsByType == nil {
		t.Error("ErrorsByType should be an empty map, not nil")
	}
}
