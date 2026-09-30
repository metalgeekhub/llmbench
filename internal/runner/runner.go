package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
)

var (
	// ErrBusy is returned when a run is already in progress. Runs are
	// serialized so concurrent benchmarks don't distort each other.
	ErrBusy = errors.New("another benchmark is running; stop it or wait for it to finish")
	// ErrActive is returned when deleting a run that is still running.
	ErrActive = errors.New("the run is still in progress; stop it first")
	// ErrNotActive is returned when stopping a run that is not running.
	ErrNotActive = errors.New("the run is not in progress")
)

// DefaultLiveInterval is how often live dashboard points are sampled.
const DefaultLiveInterval = time.Second

// Runner starts and tracks benchmark runs.
type Runner struct {
	store   store.Store
	sources *sources.Manager
	// LiveInterval is the live-metrics sampling period.
	LiveInterval time.Duration

	mu     sync.Mutex
	active map[string]*activeRun
	wg     sync.WaitGroup
}

func New(st store.Store, src *sources.Manager) *Runner {
	return &Runner{store: st, sources: src, LiveInterval: DefaultLiveInterval, active: map[string]*activeRun{}}
}

// RunView is a run with its cells and, while running, live progress. While
// running, Timeline holds the live series collected so far.
type RunView struct {
	store.TestRun
	Cells    []store.RunCell `json:"cells"`
	Progress *Progress       `json:"progress"`
}

// StartOptions carry optional metadata for a run.
type StartOptions struct {
	// DefinitionID/DefinitionVersion record the saved definition the run
	// was started from.
	DefinitionID      string
	DefinitionVersion int
}

// Start validates cfg, persists the run, and executes it in the background.
func (r *Runner) Start(ctx context.Context, cfg Config) (RunView, error) {
	return r.StartWith(ctx, cfg, StartOptions{})
}

// StartWith is Start with metadata such as the originating definition.
func (r *Runner) StartWith(ctx context.Context, cfg Config, opts StartOptions) (RunView, error) {
	if err := cfg.normalize(); err != nil {
		return RunView{}, err
	}
	targets, err := r.resolveTargets(ctx, cfg.Targets)
	if err != nil {
		return RunView{}, err
	}
	// One builder per target x thinking level; prompt text is filled in per
	// cell at execution time (large synthetic prompts are built lazily).
	plan := cfg.cellPlan()
	builders := map[string]*promptBuilder{}
	for _, pc := range plan {
		key := builderKey(pc)
		if _, ok := builders[key]; ok {
			continue
		}
		t := targets[pc.TargetIndex]
		b, err := newPromptBuilder(&cfg, "", t, pc.Thinking)
		if err != nil {
			return RunView{}, invalid("%s: %s", t.label(), err.Error())
		}
		builders[key] = b
	}
	if cfg.Name == "" {
		cfg.Name = defaultName(&cfg, targets)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.active) > 0 {
		return RunView{}, ErrBusy
	}

	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return RunView{}, err
	}
	now := time.Now().UTC()
	run := store.TestRun{
		ID:                uuid.NewString(),
		Name:              cfg.Name,
		Type:              cfg.Type,
		Status:            store.StatusRunning,
		Config:            configJSON,
		CreatedAt:         now,
		DefinitionID:      opts.DefinitionID,
		DefinitionVersion: opts.DefinitionVersion,
		StartedAt:         &now,
	}
	var cells []store.RunCell
	var cellBuilders []*promptBuilder
	for i, pc := range plan {
		t := targets[pc.TargetIndex]
		cell := store.RunCell{
			ID:            uuid.NewString(),
			Index:         i,
			SourceID:      t.SourceID,
			SourceName:    t.SourceName,
			Model:         t.Model,
			ProfileID:     t.ProfileID,
			ProfileName:   t.ProfileName,
			ContextTokens: pc.ContextTokens,
			Thinking:      pc.Thinking,
			Concurrency:   pc.Concurrency,
			Status:        store.StatusPending,
		}
		if cfg.LoadModel == LoadOpen {
			rate := pc.ArrivalRate
			cell.ArrivalRate = &rate
		}
		cells = append(cells, cell)
		cellBuilders = append(cellBuilders, builders[builderKey(pc)])
	}
	if err := r.store.CreateRun(ctx, &run, cells); err != nil {
		return RunView{}, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	ar := &activeRun{cancel: cancel, done: make(chan struct{}), runStart: start, lastSample: start}
	ar.progress.CellCount = len(cells)
	ar.progress.RecentErrors = []string{}
	r.active[run.ID] = ar
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer close(ar.done)
		r.execute(runCtx, ar, run, &cfg, cells, cellBuilders)
		r.mu.Lock()
		delete(r.active, run.ID)
		r.mu.Unlock()
		cancel()
	}()

	// The goroutine mutates cells; hand the caller a copy.
	return RunView{TestRun: run, Cells: slices.Clone(cells), Progress: ar.snapshot()}, nil
}

// resolveTargets fills in source names and, for profiles, the profile's
// source, model and parameters.
func (r *Runner) resolveTargets(ctx context.Context, targets []Target) ([]resolvedTarget, error) {
	out := make([]resolvedTarget, len(targets))
	names := map[string]string{}
	seen := map[string]bool{}
	for i, t := range targets {
		rt := resolvedTarget{SourceID: t.SourceID, Model: t.Model}
		if t.ProfileID != "" {
			p, err := r.store.GetProfile(ctx, t.ProfileID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, invalid("unknown profile %q", t.ProfileID)
			}
			if err != nil {
				return nil, err
			}
			rt = resolvedTarget{SourceID: p.SourceID, Model: p.Model, ProfileID: p.ID, ProfileName: p.Name, Params: p.Params}
		}
		// A profile and a plain target (or two profiles) may point at the
		// same model; that is fine, but the same profile twice is not.
		key := rt.SourceID + "|" + rt.Model + "|" + rt.ProfileID
		if seen[key] {
			return nil, invalid("%s is listed twice", rt.label())
		}
		seen[key] = true
		if _, ok := names[rt.SourceID]; !ok {
			src, err := r.sources.Get(ctx, rt.SourceID)
			if errors.Is(err, sources.ErrNotFound) {
				return nil, invalid("unknown source %q", rt.SourceID)
			}
			if err != nil {
				return nil, err
			}
			names[rt.SourceID] = src.Name
		}
		rt.SourceName = names[rt.SourceID]
		out[i] = rt
	}
	return out, nil
}

func (t resolvedTarget) label() string {
	if t.ProfileName != "" {
		return fmt.Sprintf("profile %q", t.ProfileName)
	}
	return fmt.Sprintf("model %q", t.Model)
}

func builderKey(pc plannedCell) string {
	return fmt.Sprintf("%d|%s", pc.TargetIndex, pc.Thinking)
}

var typeLabels = map[string]string{
	TypeSingle:             "Benchmark",
	TypeConcurrencySweep:   "Concurrency sweep",
	TypeContextSweep:       "Context sweep",
	TypeThinkingComparison: "Thinking comparison",
	TypeMatrix:             "Matrix",
}

func defaultName(c *Config, targets []resolvedTarget) string {
	label := typeLabels[c.Type]
	if c.Type == TypeConcurrencySweep && c.LoadModel == LoadOpen {
		label = "Rate sweep"
	}
	first := targets[0].Model
	if targets[0].ProfileName != "" {
		first = targets[0].ProfileName
	}
	name := fmt.Sprintf("%s · %s", label, first)
	if n := len(targets) - 1; n > 0 {
		name += fmt.Sprintf(" +%d", n)
	}
	return name
}

// Stop cancels a running run. In-flight requests are canceled and the
// results gathered so far are kept.
func (r *Runner) Stop(id string) error {
	r.mu.Lock()
	ar, ok := r.active[id]
	r.mu.Unlock()
	if !ok {
		return ErrNotActive
	}
	ar.stopped.Store(true)
	ar.cancel()
	return nil
}

// Shutdown stops all runs and waits for them to record their results.
func (r *Runner) Shutdown(ctx context.Context) {
	r.mu.Lock()
	for _, ar := range r.active {
		ar.stopped.Store(true)
		ar.cancel()
	}
	r.mu.Unlock()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (r *Runner) activeRun(id string) *activeRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active[id]
}

// Get returns a run with its cells and, while running, live progress and
// the live series so far.
func (r *Runner) Get(ctx context.Context, id string) (RunView, error) {
	run, err := r.store.GetRun(ctx, id)
	if err != nil {
		return RunView{}, err
	}
	cells, err := r.store.ListCells(ctx, id)
	if err != nil {
		return RunView{}, err
	}
	v := RunView{TestRun: run, Cells: cells}
	if ar := r.activeRun(id); ar != nil {
		v.Progress = ar.snapshot()
		v.Timeline = ar.seriesJSON()
	}
	return v, nil
}

// LiveUpdate is an incremental live-dashboard update.
type LiveUpdate struct {
	Run    RunView     `json:"run"`
	Points []LivePoint `json:"points"`
	// Next is the index to pass as since in the next call.
	Next int `json:"next"`
	// Done is true once the run has finished.
	Done bool `json:"done"`
}

// Live returns the run's current state and the live points from index
// since onwards. The run's Timeline is omitted; points carry the series.
func (r *Runner) Live(ctx context.Context, id string, since int) (LiveUpdate, error) {
	ar := r.activeRun(id)
	v, err := r.Get(ctx, id)
	if err != nil {
		return LiveUpdate{}, err
	}
	v.Timeline = nil
	u := LiveUpdate{Run: v, Points: []LivePoint{}, Next: since, Done: ar == nil}
	if ar != nil {
		u.Points, u.Next = ar.seriesSince(since)
	}
	return u, nil
}

// List returns recent runs, newest first.
func (r *Runner) List(ctx context.Context, limit int) ([]RunView, error) {
	runs, err := r.store.ListRuns(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]RunView, 0, len(runs))
	for _, run := range runs {
		cells, err := r.store.ListCells(ctx, run.ID)
		if err != nil {
			return nil, err
		}
		v := RunView{TestRun: run, Cells: cells}
		if ar := r.activeRun(run.ID); ar != nil {
			v.Progress = ar.snapshot()
		}
		out = append(out, v)
	}
	return out, nil
}

// Delete removes a finished run and all its requests.
func (r *Runner) Delete(ctx context.Context, id string) error {
	if r.activeRun(id) != nil {
		return ErrActive
	}
	return r.store.DeleteRun(ctx, id)
}

// wait blocks until the run finishes (tests).
func (r *Runner) wait(id string) {
	if ar := r.activeRun(id); ar != nil {
		<-ar.done
	}
}

// execute runs all cells in order and records the final status.
func (r *Runner) execute(ctx context.Context, ar *activeRun, run store.TestRun, cfg *Config,
	cells []store.RunCell, builders []*promptBuilder) {
	w := newWriter(r.store)

	// Live dashboard sampler.
	samplerDone := make(chan struct{})
	stopSampler := make(chan struct{})
	go func() {
		defer close(samplerDone)
		tick := time.NewTicker(r.LiveInterval)
		defer tick.Stop()
		for {
			select {
			case now := <-tick.C:
				ar.sample(now)
			case <-stopSampler:
				return
			}
		}
	}()

	// Synthetic prompts are built once per context length.
	prompts := map[int]string{}
	final := store.StatusCompleted
	failed := 0
	for i := range cells {
		c := &cells[i]
		if ctx.Err() != nil || final == store.StatusAborted {
			c.Status = store.StatusSkipped
			r.saveCell(c)
			continue
		}
		user, ok := prompts[c.ContextTokens]
		if !ok {
			user = userPrompt(cfg, c.ContextTokens)
			prompts[c.ContextTokens] = user
		}
		b := *builders[i] // shared maps are read-only; the copy gets this cell's prompt
		b.user = user
		switch r.runCell(ctx, ar, cfg, &b, run.ID, c, w) {
		case store.StatusAborted:
			final = store.StatusAborted
			run.Error = fmt.Sprintf("%s (%s, %d users): %s", c.Model, c.SourceName, c.Concurrency, c.Error)
		case store.StatusFailed:
			failed++
		}
	}
	// All requests must be on disk before the run is reported finished.
	w.close()
	close(stopSampler)
	<-samplerDone
	ar.sample(time.Now())

	switch {
	case ar.stopped.Load():
		final = store.StatusStopped
	case final == store.StatusCompleted && failed == len(cells):
		final = store.StatusFailed
		run.Error = cells[0].Error
	}
	now := time.Now().UTC()
	run.Status = final
	run.FinishedAt = &now
	run.Timeline = ar.seriesJSON()
	if err := r.store.UpdateRun(context.Background(), &run); err != nil {
		slog.Error("saving run", "run", run.ID, "err", err)
	}
}

func (r *Runner) saveCell(c *store.RunCell) {
	if err := r.store.UpdateCell(context.Background(), c); err != nil {
		slog.Error("saving run cell", "cell", c.ID, "err", err)
	}
}

// IsActive reports whether any of the runs is still executing.
func (r *Runner) IsActive(ids ...string) bool {
	for _, id := range ids {
		if r.activeRun(id) != nil {
			return true
		}
	}
	return false
}
