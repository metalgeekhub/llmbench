package metrics

import "time"

// Aggregate summarises a group of measured requests (one benchmark cell).
// Latency and speed distributions only include successful requests;
// canceled requests (user stop) are excluded from everything but Canceled.
type Aggregate struct {
	// Requests counts completed requests: Succeeded + Failed.
	Requests     int            `json:"requests"`
	Succeeded    int            `json:"succeeded"`
	Failed       int            `json:"failed"`
	Canceled     int            `json:"canceled"`
	ErrorRate    float64        `json:"error_rate"` // Failed / Requests
	ErrorsByType map[string]int `json:"errors_by_type"`

	WallSeconds float64 `json:"wall_seconds"`
	// RequestThroughput is successful requests per second of wall time.
	RequestThroughput float64 `json:"request_throughput"`
	// OutputThroughput is output tokens per second across all users.
	OutputThroughput float64 `json:"output_throughput"`

	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// ReasoningTokens is the part of OutputTokens spent on reasoning.
	ReasoningTokens int  `json:"reasoning_tokens"`
	TokensEstimated bool `json:"tokens_estimated"`

	TTFT       Summary `json:"ttft_ms"`
	TTFAT      Summary `json:"ttfat_ms"`
	E2E        Summary `json:"e2e_ms"`
	TPOT       Summary `json:"tpot_ms"`
	OutputTPS  Summary `json:"output_tps"`
	PrefillTPS Summary `json:"prefill_tps"`
	// ITL pools the gaps between chunks of all successful requests (per chunk).
	ITL Summary `json:"itl_ms"`
}

// NewAggregate summarises measurements taken over wall time.
func NewAggregate(measurements []Measurement, wall time.Duration) Aggregate {
	a := Aggregate{ErrorsByType: map[string]int{}, WallSeconds: wall.Seconds()}
	var ttft, ttfat, e2e, tpot, outTPS, prefill, itl []float64
	for _, m := range measurements {
		switch m.Status {
		case StatusCanceled:
			a.Canceled++
			continue
		case StatusError:
			a.Failed++
			if m.Err != nil {
				a.ErrorsByType[m.Err.Type]++
			}
			continue
		}
		a.Succeeded++
		r := m.Result
		a.InputTokens += r.InputTokens
		a.OutputTokens += r.OutputTokens
		a.ReasoningTokens += r.ReasoningTokens
		a.TokensEstimated = a.TokensEstimated || r.TokensEstimated
		e2e = append(e2e, r.E2EMs)
		appendPtr(&ttft, r.TTFTMs)
		appendPtr(&ttfat, r.TTFATMs)
		appendPtr(&tpot, r.TPOTMs)
		appendPtr(&outTPS, r.OutputTPS)
		appendPtr(&prefill, r.PrefillTPS)
		for i := 1; i < len(m.Chunks); i++ {
			itl = append(itl, ms(m.Chunks[i].At-m.Chunks[i-1].At))
		}
	}
	a.Requests = a.Succeeded + a.Failed
	if a.Requests > 0 {
		a.ErrorRate = float64(a.Failed) / float64(a.Requests)
	}
	if s := wall.Seconds(); s > 0 {
		a.RequestThroughput = float64(a.Succeeded) / s
		a.OutputThroughput = float64(a.OutputTokens) / s
	}
	a.TTFT = Summarize(ttft)
	a.TTFAT = Summarize(ttfat)
	a.E2E = Summarize(e2e)
	a.TPOT = Summarize(tpot)
	a.OutputTPS = Summarize(outTPS)
	a.PrefillTPS = Summarize(prefill)
	a.ITL = Summarize(itl)
	return a
}

func appendPtr(dst *[]float64, v *float64) {
	if v != nil {
		*dst = append(*dst, *v)
	}
}
