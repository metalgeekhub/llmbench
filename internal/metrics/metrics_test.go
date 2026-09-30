package metrics

import (
	"math"
	"testing"
	"time"
)

const eps = 1e-9

func near(a, b float64) bool { return math.Abs(a-b) < eps }

func msd(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }

func TestComputeBasic(t *testing.T) {
	// First token at 200ms, then chunks every 20ms, last at 380ms.
	var chunks []Chunk
	for i := range 10 {
		chunks = append(chunks, Chunk{At: msd(200 + float64(i)*20), Kind: ChunkContent})
	}
	r := Compute(Input{
		E2E:          msd(400),
		Chunks:       chunks,
		InputTokens:  1000,
		OutputTokens: 11,
	})

	if r.TTFTMs == nil || !near(*r.TTFTMs, 200) {
		t.Errorf("TTFT = %v, want 200", r.TTFTMs)
	}
	if r.TTFATMs == nil || !near(*r.TTFATMs, 200) {
		t.Errorf("TTFAT = %v, want 200", r.TTFATMs)
	}
	if !near(r.E2EMs, 400) {
		t.Errorf("E2E = %v, want 400", r.E2EMs)
	}
	// (400 - 200) / (11 - 1) = 20ms
	if r.TPOTMs == nil || !near(*r.TPOTMs, 20) {
		t.Errorf("TPOT = %v, want 20", r.TPOTMs)
	}
	// 11 tokens / 0.2s = 55 tok/s
	if r.OutputTPS == nil || !near(*r.OutputTPS, 55) {
		t.Errorf("OutputTPS = %v, want 55", r.OutputTPS)
	}
	// 1000 tokens / 0.2s = 5000 tok/s
	if r.PrefillTPS == nil || !near(*r.PrefillTPS, 5000) {
		t.Errorf("PrefillTPS = %v, want 5000", r.PrefillTPS)
	}
	if r.ChunkCount != 10 || r.ITL.Count != 9 || !near(r.ITL.Mean, 20) || !near(r.ITL.Max, 20) {
		t.Errorf("ITL = %+v chunks=%d", r.ITL, r.ChunkCount)
	}
}

func TestComputeReasoningThenAnswer(t *testing.T) {
	r := Compute(Input{
		E2E: msd(1000),
		Chunks: []Chunk{
			{At: msd(100), Kind: ChunkReasoning},
			{At: msd(300), Kind: ChunkReasoning},
			{At: msd(700), Kind: ChunkContent},
			{At: msd(900), Kind: ChunkContent},
		},
		OutputTokens:    4,
		ReasoningTokens: 2,
	})
	if r.TTFTMs == nil || !near(*r.TTFTMs, 100) {
		t.Errorf("TTFT should be first token of any kind, got %v", r.TTFTMs)
	}
	if r.TTFATMs == nil || !near(*r.TTFATMs, 700) {
		t.Errorf("TTFAT should be first answer token, got %v", r.TTFATMs)
	}
	// (1000 - 100) / 3 = 300
	if r.TPOTMs == nil || !near(*r.TPOTMs, 300) {
		t.Errorf("TPOT = %v, want 300", r.TPOTMs)
	}
	// Gaps: 200, 400, 200
	if r.ITL.Count != 3 || !near(r.ITL.Min, 200) || !near(r.ITL.Max, 400) || !near(r.ITL.P50, 200) {
		t.Errorf("ITL = %+v", r.ITL)
	}
}

func TestComputeUndefinedMetrics(t *testing.T) {
	// No chunks at all (e.g. failed request).
	r := Compute(Input{E2E: msd(50)})
	if r.TTFTMs != nil || r.TTFATMs != nil || r.TPOTMs != nil || r.OutputTPS != nil || r.PrefillTPS != nil {
		t.Errorf("expected all timing metrics nil, got %+v", r)
	}
	if r.ITL.Count != 0 {
		t.Errorf("ITL should be empty, got %+v", r.ITL)
	}

	// Single output token: TPOT undefined, ITL empty.
	r = Compute(Input{
		E2E:          msd(120),
		Chunks:       []Chunk{{At: msd(100), Kind: ChunkContent}},
		OutputTokens: 1,
	})
	if r.TPOTMs != nil {
		t.Errorf("TPOT should be nil for one output token, got %v", *r.TPOTMs)
	}
	if r.TTFTMs == nil || !near(*r.TTFTMs, 100) {
		t.Errorf("TTFT = %v", r.TTFTMs)
	}
	if r.ITL.Count != 0 {
		t.Errorf("ITL should be empty with one chunk")
	}
	// Reasoning only: no answer token.
	r = Compute(Input{
		E2E:          msd(120),
		Chunks:       []Chunk{{At: msd(10), Kind: ChunkReasoning}},
		OutputTokens: 1,
	})
	if r.TTFATMs != nil {
		t.Errorf("TTFAT should be nil without answer tokens")
	}
}

func TestTPOT(t *testing.T) {
	tests := []struct {
		e2e, ttft float64
		out       int
		want      float64
		ok        bool
	}{
		{1000, 200, 81, 10, true},
		{500, 100, 2, 400, true},
		{500, 100, 1, 0, false},
		{500, 100, 0, 0, false},
		{100, 200, 10, 0, false}, // inconsistent timings
	}
	for _, tt := range tests {
		got, ok := TPOT(msd(tt.e2e), msd(tt.ttft), tt.out)
		if ok != tt.ok || (ok && !near(got, tt.want)) {
			t.Errorf("TPOT(%v, %v, %d) = %v, %v; want %v, %v", tt.e2e, tt.ttft, tt.out, got, ok, tt.want, tt.ok)
		}
	}
}

func TestPercentile(t *testing.T) {
	vals := []float64{15, 20, 35, 40, 50}
	tests := map[float64]float64{
		0:   15,
		25:  20,
		50:  35,
		75:  40,
		100: 50,
		40:  29, // pos 1.6 → 20 + 0.6*15
		90:  46, // pos 3.6 → 40 + 0.6*10
	}
	for p, want := range tests {
		if got := Percentile(vals, p); !near(got, want) {
			t.Errorf("Percentile(%v) = %v, want %v", p, got, want)
		}
	}
	if !math.IsNaN(Percentile(nil, 50)) {
		t.Error("Percentile of empty slice should be NaN")
	}
	if got := Percentile([]float64{7}, 99); got != 7 {
		t.Errorf("single value percentile = %v", got)
	}
	// Out-of-range p is clamped.
	if got := Percentile(vals, 150); got != 50 {
		t.Errorf("clamped p = %v", got)
	}
}

func TestSummarize(t *testing.T) {
	// 1..100: numpy gives p50=50.5, p90=90.1, p95=95.05, p99=99.01.
	vals := make([]float64, 100)
	for i := range vals {
		vals[i] = float64(100 - i) // unsorted input
	}
	s := Summarize(vals)
	want := Summary{Count: 100, Min: 1, Mean: 50.5, P50: 50.5, P90: 90.1, P95: 95.05, P99: 99.01, Max: 100}
	for name, pair := range map[string][2]float64{
		"min": {s.Min, want.Min}, "mean": {s.Mean, want.Mean}, "p50": {s.P50, want.P50},
		"p90": {s.P90, want.P90}, "p95": {s.P95, want.P95}, "p99": {s.P99, want.P99}, "max": {s.Max, want.Max},
	} {
		if math.Abs(pair[0]-pair[1]) > 1e-6 {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
	if s.Count != 100 {
		t.Errorf("count = %d", s.Count)
	}
	if vals[0] != 100 {
		t.Error("Summarize must not modify its input")
	}
	if (Summarize(nil) != Summary{}) {
		t.Error("empty summary should be zero")
	}
}
