package runner

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/metalgeekhub/llmbench/internal/metrics"
	"github.com/metalgeekhub/llmbench/internal/providers"
	"github.com/metalgeekhub/llmbench/internal/store"
)

// minSampleForAbort is how many requests must complete before the
// error-rate stop condition is evaluated.
const minSampleForAbort = 10

const recentErrorCount = 5

// sampleOutputLimit caps the stored sample response per cell (characters).
const sampleOutputLimit = 4000

// Progress is a live snapshot of a running benchmark.
type Progress struct {
	CellIndex int    `json:"cell_index"`
	CellCount int    `json:"cell_count"`
	CellID    string `json:"cell_id"`
	Phase     string `json:"phase"` // "warmup" or "measuring"
	// Concurrency is the user count (closed loop) or in-flight cap (open).
	Concurrency int `json:"concurrency"`
	// ArrivalRate is the open-loop target rate (0 for closed loop).
	ArrivalRate float64 `json:"arrival_rate"`
	// TargetRequests is 0 when the cell is bounded by duration only.
	TargetRequests  int     `json:"target_requests"`
	DurationSeconds float64 `json:"duration_seconds"`
	WarmupDone      int     `json:"warmup_done"`
	WarmupTotal     int     `json:"warmup_total"`

	Completed    int `json:"completed"` // measured requests finished (ok + failed)
	Failed       int `json:"failed"`
	InFlight     int `json:"in_flight"`
	ActiveUsers  int `json:"active_users"` // virtual users (closed) or in-flight requests (open)
	OutputTokens int `json:"output_tokens"`

	CellElapsedSeconds    float64  `json:"cell_elapsed_seconds"`
	RunElapsedSeconds     float64  `json:"run_elapsed_seconds"`
	RequestsPerSecond     float64  `json:"requests_per_second"`
	OutputTokensPerSecond float64  `json:"output_tokens_per_second"`
	RecentErrors          []string `json:"recent_errors"`
}

type activeRun struct {
	cancel   context.CancelFunc
	stopped  atomic.Bool
	done     chan struct{}
	runStart time.Time

	mu        sync.Mutex
	progress  Progress
	cellStart time.Time // start of the measured phase

	// Live dashboard series (guarded by mu).
	bucket        liveBucket
	lastSample    time.Time
	series        []LivePoint
	seriesDropped int
}

func (a *activeRun) update(fn func(p *Progress)) {
	a.mu.Lock()
	fn(&a.progress)
	a.mu.Unlock()
}

func (a *activeRun) snapshot() *Progress {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.progress
	p.RecentErrors = append([]string{}, a.progress.RecentErrors...)
	p.RunElapsedSeconds = time.Since(a.runStart).Seconds()
	if !a.cellStart.IsZero() {
		p.CellElapsedSeconds = time.Since(a.cellStart).Seconds()
		if s := p.CellElapsedSeconds; s > 0 {
			p.RequestsPerSecond = float64(p.Completed-p.Failed) / s
			p.OutputTokensPerSecond = float64(p.OutputTokens) / s
		}
	}
	return &p
}

func (a *activeRun) recordError(msg string) {
	errs := append(a.progress.RecentErrors, msg)
	if len(errs) > recentErrorCount {
		errs = errs[len(errs)-recentErrorCount:]
	}
	a.progress.RecentErrors = errs
}

// loadGen runs the measurement phase's load and sends every measurement to
// results, closing it when done. issueCtx stops new requests; ctx cancels
// in-flight ones. It returns the open-loop schedule lags (ms) after
// results is closed (nil for closed loop).
type loadGen func(ctx, issueCtx context.Context, send func() metrics.Measurement,
	results chan<- metrics.Measurement) (lags func() []float64)

// runCell executes one cell (warm-up, then measurement) and returns its
// final status.
func (r *Runner) runCell(ctx context.Context, ar *activeRun, cfg *Config, prompts *promptBuilder,
	runID string, c *store.RunCell, w *writer) string {
	start := time.Now().UTC()
	c.Status = store.StatusRunning
	c.StartedAt = &start
	r.saveCell(c)

	rate := 0.0
	if c.ArrivalRate != nil {
		rate = *c.ArrivalRate
	}
	ar.mu.Lock()
	ar.cellStart = time.Time{}
	ar.progress = Progress{
		CellIndex:       c.Index,
		CellCount:       ar.progress.CellCount,
		CellID:          c.ID,
		Phase:           "warmup",
		Concurrency:     c.Concurrency,
		ArrivalRate:     rate,
		TargetRequests:  cfg.RequestsPerCell,
		DurationSeconds: cfg.DurationSeconds,
		WarmupTotal:     cfg.WarmupRequests,
		RecentErrors:    []string{},
	}
	ar.mu.Unlock()

	finish := func(status, errMsg string) string {
		now := time.Now().UTC()
		c.Status = status
		c.Error = errMsg
		c.FinishedAt = &now
		r.saveCell(c)
		return status
	}

	timeout := time.Duration(cfg.TimeoutSeconds * float64(time.Second))
	provider, err := r.sources.LoadProvider(ctx, c.SourceID, c.Concurrency, timeout)
	if err != nil {
		return finish(store.StatusFailed, err.Error())
	}

	params := prompts.requestParams(c.Concurrency)
	if c.ContextTokens > 0 {
		params["context_tokens"] = c.ContextTokens
	}
	if rate > 0 {
		params["arrival_rate"] = rate
	}
	record := func(m metrics.Measurement, warmup bool) {
		req := store.Request{
			ID:         uuid.NewString(),
			Kind:       store.KindTest,
			SourceID:   c.SourceID,
			SourceName: c.SourceName,
			Model:      c.Model,
			RunID:      runID,
			CellID:     c.ID,
			Warmup:     warmup,
			Status:     m.Status,
			StartedAt:  m.StartedAt,
			Metrics:    m.Result,
			Params:     params,
		}
		if m.Err != nil {
			req.ErrorType = m.Err.Type
			req.ErrorMessage = m.Err.Message
			req.HTTPStatus = m.Err.HTTPStatus
		}
		w.add(store.RequestRecord{Request: req, Timeline: store.TimelineFromChunks(m.Chunks)})
	}
	send := func() metrics.Measurement {
		return metrics.Collect(ctx, provider, prompts.request(c.Model), nil)
	}

	// Warm-up: sequential, recorded but excluded from the summary.
	if cfg.WarmupRequests > 0 {
		ar.update(func(p *Progress) { p.ActiveUsers = 1 })
	}
	for i := 0; i < cfg.WarmupRequests && ctx.Err() == nil; i++ {
		ar.update(func(p *Progress) { p.InFlight = 1 })
		record(send(), true)
		ar.update(func(p *Progress) { p.WarmupDone++; p.InFlight = 0 })
	}
	ar.update(func(p *Progress) { p.ActiveUsers = 0 })
	if ctx.Err() != nil {
		return finish(store.StatusStopped, "")
	}

	// Measurement. issueCtx only stops new requests; in-flight ones finish
	// (or are canceled by ctx on user stop).
	issueCtx, stopIssuing := context.WithCancel(ctx)
	defer stopIssuing()
	if cfg.DurationSeconds > 0 {
		var cancelDeadline context.CancelFunc
		issueCtx, cancelDeadline = context.WithTimeout(issueCtx, time.Duration(cfg.DurationSeconds*float64(time.Second)))
		defer cancelDeadline()
	}

	cpu := startCPU()
	measureStart := time.Now()
	ar.update(func(p *Progress) { p.Phase = "measuring" })
	ar.mu.Lock()
	ar.cellStart = measureStart
	ar.mu.Unlock()

	var gen loadGen
	if rate > 0 {
		gen = openLoop(ar, rate, cfg.RequestsPerCell, c.Concurrency)
	} else {
		gen = closedLoop(ar, c.Concurrency, cfg.RequestsPerCell)
	}
	results := make(chan metrics.Measurement, c.Concurrency)
	lags := gen(ctx, issueCtx, send, results)

	var (
		all     []metrics.Measurement
		last    = measureStart
		aborted bool
		ok, bad int
	)
	for m := range results {
		all = append(all, m)
		last = time.Now()
		record(m, false)
		switch m.Status {
		case metrics.StatusOK:
			ok++
			if c.SampleOutput == "" && c.SampleReasoning == "" && (m.Content != "" || m.Reasoning != "") {
				c.SampleOutput = truncateRunes(m.Content, sampleOutputLimit)
				c.SampleReasoning = truncateRunes(m.Reasoning, sampleOutputLimit)
			}
		case metrics.StatusError:
			bad++
		}
		ar.update(func(p *Progress) {
			if m.Status == metrics.StatusCanceled {
				return
			}
			ar.addLive(m)
			p.Completed++
			if m.Status == metrics.StatusError {
				p.Failed++
				ar.recordError(m.Err.Error())
			} else {
				p.OutputTokens += m.Result.OutputTokens
			}
		})
		if cfg.MaxErrorRate > 0 && !aborted && ok+bad >= minSampleForAbort &&
			float64(bad)/float64(ok+bad) > cfg.MaxErrorRate {
			aborted = true
			stopIssuing()
		}
	}

	agg := metrics.NewAggregate(all, last.Sub(measureStart))
	c.Summary = &agg
	c.ClientCPUPct = cpu.stop()
	if l := lags(); len(l) > 0 {
		p95 := metrics.Percentile(l, 95)
		c.ScheduleLagP95Ms = &p95
	}

	switch {
	case ctx.Err() != nil:
		return finish(store.StatusStopped, "")
	case aborted:
		return finish(store.StatusAborted, fmt.Sprintf("error rate %.0f%% exceeded the %.0f%% limit",
			100*agg.ErrorRate, 100*cfg.MaxErrorRate))
	default:
		return finish(store.StatusCompleted, "")
	}
}

// closedLoop runs `users` virtual users; each sends its next request as
// soon as the previous one finishes, until the budget (0 = unlimited) or
// issueCtx ends.
func closedLoop(ar *activeRun, users, budget int) loadGen {
	return func(ctx, issueCtx context.Context, send func() metrics.Measurement, results chan<- metrics.Measurement) func() []float64 {
		var claimed atomic.Int64
		var wg sync.WaitGroup
		for range users {
			wg.Add(1)
			ar.update(func(p *Progress) { p.ActiveUsers++ })
			go func() {
				defer wg.Done()
				defer ar.update(func(p *Progress) { p.ActiveUsers-- })
				for issueCtx.Err() == nil {
					if budget > 0 && claimed.Add(1) > int64(budget) {
						return
					}
					ar.update(func(p *Progress) { p.InFlight++ })
					m := send()
					ar.update(func(p *Progress) { p.InFlight-- })
					results <- m
				}
			}()
		}
		go func() {
			wg.Wait()
			close(results)
		}()
		return func() []float64 { return nil }
	}
}

// openLoop sends requests with Poisson arrivals at `rate` per second,
// independent of response times, until the budget (0 = unlimited) or
// issueCtx ends. At most maxInFlight requests run at once; arrivals beyond
// that wait for a slot, which is reported as schedule lag.
func openLoop(ar *activeRun, rate float64, budget, maxInFlight int) loadGen {
	return func(ctx, issueCtx context.Context, send func() metrics.Measurement, results chan<- metrics.Measurement) func() []float64 {
		slots := make(chan struct{}, maxInFlight)
		var wg sync.WaitGroup
		var lags []float64
		schedDone := make(chan struct{})
		go func() {
			defer close(schedDone)
			timer := time.NewTimer(0)
			defer timer.Stop()
			<-timer.C
			next := time.Now()
			for issued := 0; budget == 0 || issued < budget; issued++ {
				// Exponential inter-arrival times give a Poisson process.
				next = next.Add(time.Duration(rand.ExpFloat64() / rate * float64(time.Second)))
				if d := time.Until(next); d > 0 {
					timer.Reset(d)
					select {
					case <-timer.C:
					case <-issueCtx.Done():
						return
					}
				} else if issueCtx.Err() != nil {
					return
				}
				select {
				case slots <- struct{}{}:
				case <-issueCtx.Done():
					return
				}
				lags = append(lags, float64(time.Since(next))/float64(time.Millisecond))
				wg.Add(1)
				ar.update(func(p *Progress) { p.InFlight++; p.ActiveUsers = p.InFlight })
				go func() {
					defer wg.Done()
					m := send()
					<-slots
					ar.update(func(p *Progress) { p.InFlight--; p.ActiveUsers = p.InFlight })
					results <- m
				}()
			}
		}()
		go func() {
			<-schedDone
			wg.Wait()
			close(results)
		}()
		// Only called after results is closed, i.e. after the scheduler exited.
		return func() []float64 { return lags }
	}
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// Compile-time check that thinking levels used by the runner match the
// adapter's.
var _ = providers.ThinkingOff
