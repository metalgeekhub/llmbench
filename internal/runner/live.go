package runner

import (
	"encoding/json"
	"time"

	"github.com/metalgeekhub/llmbench/internal/metrics"
)

// maxLivePoints caps the in-memory series (one hour at 1 s); older points
// are dropped.
const maxLivePoints = 3600

// LivePoint is one interval of live metrics for the dashboard.
type LivePoint struct {
	// T is seconds since the run started, at the end of the interval.
	T float64 `json:"t"`
	// Cell is the index of the cell running during the interval.
	Cell     int `json:"cell"`
	Users    int `json:"users"`     // active virtual users
	InFlight int `json:"in_flight"` // requests awaiting a response
	// RPS and TPS are successful requests and output tokens per second,
	// counting requests that completed during the interval.
	RPS    float64 `json:"rps"`
	TPS    float64 `json:"tps"`
	Errors int     `json:"errors"` // failed requests completed during the interval
	// TTFT percentiles of requests completed during the interval (ms).
	TTFTP50 *float64 `json:"ttft_p50"`
	TTFTP95 *float64 `json:"ttft_p95"`
}

// liveBucket accumulates measured (non-warm-up) completions between ticks.
type liveBucket struct {
	ok, errors, tokens int
	ttfts              []float64
}

// addLive records a measured completion. Caller holds a.mu.
func (a *activeRun) addLive(m metrics.Measurement) {
	switch m.Status {
	case metrics.StatusOK:
		a.bucket.ok++
		a.bucket.tokens += m.Result.OutputTokens
		if m.Result.TTFTMs != nil {
			a.bucket.ttfts = append(a.bucket.ttfts, *m.Result.TTFTMs)
		}
	case metrics.StatusError:
		a.bucket.errors++
	}
}

// sample closes the current interval and appends a point.
func (a *activeRun) sample(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	secs := now.Sub(a.lastSample).Seconds()
	if secs <= 0 {
		return
	}
	b := a.bucket
	p := LivePoint{
		T:        now.Sub(a.runStart).Seconds(),
		Cell:     a.progress.CellIndex,
		Users:    a.progress.ActiveUsers,
		InFlight: a.progress.InFlight,
		RPS:      float64(b.ok) / secs,
		TPS:      float64(b.tokens) / secs,
		Errors:   b.errors,
	}
	if len(b.ttfts) > 0 {
		s := metrics.Summarize(b.ttfts)
		p.TTFTP50, p.TTFTP95 = &s.P50, &s.P95
	}
	a.series = append(a.series, p)
	if len(a.series) > maxLivePoints {
		drop := len(a.series) - maxLivePoints
		a.series = append(a.series[:0:0], a.series[drop:]...)
		a.seriesDropped += drop
	}
	a.bucket = liveBucket{}
	a.lastSample = now
}

// seriesSince returns points with absolute index >= since, and the next
// index to ask for.
func (a *activeRun) seriesSince(since int) ([]LivePoint, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.seriesDropped + len(a.series)
	start := max(since-a.seriesDropped, 0)
	if start >= len(a.series) {
		return []LivePoint{}, next
	}
	return append([]LivePoint{}, a.series[start:]...), next
}

func (a *activeRun) seriesJSON() json.RawMessage {
	points, _ := a.seriesSince(0)
	b, err := json.Marshal(points)
	if err != nil {
		return nil
	}
	return b
}
