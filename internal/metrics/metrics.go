// Package metrics computes per-request performance metrics from streamed
// chunk timings and summarises distributions with percentiles.
//
// Definitions follow docs/PLAN.md §5.
package metrics

import (
	"math"
	"sort"
	"time"
)

// ChunkKind distinguishes answer tokens from reasoning tokens.
type ChunkKind byte

const (
	ChunkContent   ChunkKind = 'c'
	ChunkReasoning ChunkKind = 'r'
)

// Chunk is one token-bearing streamed chunk, timed relative to request start.
type Chunk struct {
	At   time.Duration
	Kind ChunkKind
}

// Input is everything needed to compute a request's metrics.
type Input struct {
	// E2E is request start → final chunk received (or failure).
	E2E time.Duration
	// Chunks are the token-bearing chunks in receive order.
	Chunks []Chunk

	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
	CachedTokens    int
	TokensEstimated bool
}

// Result holds per-request metrics. Pointer fields are nil when the metric
// is undefined for the request (for example TPOT with a single output token).
type Result struct {
	TTFTMs     *float64 `json:"ttft_ms"`
	TTFATMs    *float64 `json:"ttfat_ms"` // time to first answer (non-reasoning) token
	E2EMs      float64  `json:"e2e_ms"`
	TPOTMs     *float64 `json:"tpot_ms"`
	OutputTPS  *float64 `json:"output_tps"`
	PrefillTPS *float64 `json:"prefill_tps"`

	InputTokens     int  `json:"input_tokens"`
	OutputTokens    int  `json:"output_tokens"`
	ReasoningTokens int  `json:"reasoning_tokens"`
	CachedTokens    int  `json:"cached_tokens"`
	TokensEstimated bool `json:"tokens_estimated"`

	ChunkCount int `json:"chunk_count"`
	// ITL is the distribution of gaps between token-bearing chunks, in ms.
	// It is measured per chunk, not per token.
	ITL Summary `json:"itl"`
}

// Compute derives all per-request metrics from in.
func Compute(in Input) Result {
	r := Result{
		E2EMs:           ms(in.E2E),
		InputTokens:     in.InputTokens,
		OutputTokens:    in.OutputTokens,
		ReasoningTokens: in.ReasoningTokens,
		CachedTokens:    in.CachedTokens,
		TokensEstimated: in.TokensEstimated,
		ChunkCount:      len(in.Chunks),
	}
	if len(in.Chunks) == 0 {
		return r
	}

	ttft := in.Chunks[0].At
	r.TTFTMs = ptr(ms(ttft))
	for _, c := range in.Chunks {
		if c.Kind == ChunkContent {
			r.TTFATMs = ptr(ms(c.At))
			break
		}
	}

	decode := in.E2E - ttft
	if tpot, ok := TPOT(in.E2E, ttft, in.OutputTokens); ok {
		r.TPOTMs = ptr(tpot)
	}
	if decode > 0 && in.OutputTokens > 0 {
		r.OutputTPS = ptr(float64(in.OutputTokens) / decode.Seconds())
	}
	if ttft > 0 && in.InputTokens > 0 {
		r.PrefillTPS = ptr(float64(in.InputTokens) / ttft.Seconds())
	}

	if len(in.Chunks) > 1 {
		gaps := make([]float64, 0, len(in.Chunks)-1)
		for i := 1; i < len(in.Chunks); i++ {
			gaps = append(gaps, ms(in.Chunks[i].At-in.Chunks[i-1].At))
		}
		r.ITL = Summarize(gaps)
	}
	return r
}

// TPOT returns (E2E − TTFT) / (outputTokens − 1) in milliseconds. It is
// undefined (ok=false) when there are fewer than two output tokens.
func TPOT(e2e, ttft time.Duration, outputTokens int) (float64, bool) {
	if outputTokens < 2 || e2e < ttft {
		return 0, false
	}
	return ms(e2e-ttft) / float64(outputTokens-1), true
}

// Summary describes a distribution. All fields are zero when Count is 0.
type Summary struct {
	Count int     `json:"count"`
	Min   float64 `json:"min"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
}

// Summarize computes min, mean, p50, p90, p95, p99 and max of values.
// values is not modified.
func Summarize(values []float64) Summary {
	if len(values) == 0 {
		return Summary{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)

	var sum float64
	for _, v := range sorted {
		sum += v
	}
	return Summary{
		Count: len(sorted),
		Min:   sorted[0],
		Mean:  sum / float64(len(sorted)),
		P50:   percentileSorted(sorted, 50),
		P90:   percentileSorted(sorted, 90),
		P95:   percentileSorted(sorted, 95),
		P99:   percentileSorted(sorted, 99),
		Max:   sorted[len(sorted)-1],
	}
}

// Percentile returns the p-th percentile (0–100) of values using linear
// interpolation between closest ranks (the numpy default). It returns NaN for
// an empty slice.
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return percentileSorted(sorted, p)
}

func percentileSorted(sorted []float64, p float64) float64 {
	p = math.Max(0, math.Min(100, p))
	pos := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo] + (sorted[hi]-sorted[lo])*frac
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func ptr[T any](v T) *T { return &v }
