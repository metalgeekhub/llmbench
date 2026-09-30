// Package runner executes benchmarks. A run is a matrix of cells, one per
// combination of target x context length x thinking level x load, each
// measured through the shared pipeline with closed-loop virtual users or an
// open-loop arrival process.
package runner

import (
	"fmt"
	"slices"
	"strings"

	"github.com/metalgeekhub/llmbench/internal/metrics"
	"github.com/metalgeekhub/llmbench/internal/providers"
)

// Test types. They are presets of the matrix: the type decides which
// dimensions must or may vary.
const (
	TypeSingle             = "single"
	TypeConcurrencySweep   = "concurrency_sweep" // load varies (users or rates)
	TypeContextSweep       = "context_sweep"     // input length varies
	TypeThinkingComparison = "thinking_comparison"
	TypeMatrix             = "matrix" // any combination
)

// Load models.
const (
	// LoadClosed runs N virtual users, each sending its next request when
	// the previous one finishes.
	LoadClosed = "closed"
	// LoadOpen sends requests at a target rate with Poisson arrivals,
	// regardless of how fast the server answers.
	LoadOpen = "open"
)

// Prompt sources.
const (
	PromptFixed     = "fixed"
	PromptSynthetic = "synthetic"
)

// Limits keep a mistyped config from launching something absurd.
const (
	MaxTargets         = 8 // also the number of distinguishable chart colors
	MaxConcurrency     = 1024
	MaxLevels          = 16 // values per dimension
	MaxCells           = 256
	MaxRequestsPerCell = 100_000
	MaxDurationSeconds = 24 * 60 * 60
	MaxWarmupRequests  = 1000
	MaxInputTokens     = 1_000_000
	MaxTimeoutSeconds  = 3600
	MaxArrivalRate     = 1000
	MaxInFlightLimit   = 4096
	// DefaultMaxInFlight caps concurrent requests in open-loop cells.
	DefaultMaxInFlight = 256
)

var thinkingOrder = []string{providers.ThinkingOff, providers.ThinkingLow, providers.ThinkingMedium, providers.ThinkingHigh}

// Config defines a benchmark. It is stored with the run so it can be re-run.
type Config struct {
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Targets     []Target       `json:"targets"`
	Prompt      Prompt         `json:"prompt"`
	MaxTokens   *int           `json:"max_tokens"`
	Temperature *float64       `json:"temperature"`
	IgnoreEOS   bool           `json:"ignore_eos"`
	ExtraBody   map[string]any `json:"extra_body"`

	// LoadModel is "closed" (default) or "open".
	LoadModel string `json:"load_model"`
	// Concurrency lists virtual-user counts (closed loop).
	Concurrency []int `json:"concurrency"`
	// ArrivalRates lists target request rates per second (open loop).
	ArrivalRates []float64 `json:"arrival_rates"`
	// MaxInFlight caps concurrent requests in open-loop cells; arrivals
	// beyond it wait, which shows up as schedule lag.
	MaxInFlight int `json:"max_in_flight"`

	// ContextLengths sweeps the synthetic prompt length in tokens. When
	// set, the prompt is synthetic and Prompt.InputTokens is ignored.
	ContextLengths []int `json:"context_lengths"`
	// ThinkingLevels sweeps the thinking control. ThinkingStyle says how to
	// send it for targets whose profile doesn't define a style.
	ThinkingLevels []string `json:"thinking_levels"`
	ThinkingStyle  string   `json:"thinking_style"`

	// A cell ends after RequestsPerCell measured requests or DurationSeconds,
	// whichever comes first; 0 disables that limit (at least one is required).
	RequestsPerCell int     `json:"requests_per_cell"`
	DurationSeconds float64 `json:"duration_seconds"`
	// WarmupRequests run before each cell's measurement and are excluded.
	WarmupRequests int `json:"warmup_requests"`
	// CacheBust prefixes every prompt with a unique marker so prefix/KV
	// caching doesn't inflate results.
	CacheBust      bool    `json:"cache_bust"`
	TimeoutSeconds float64 `json:"timeout_seconds"`
	// MaxErrorRate (0–1) aborts the run when exceeded; 0 disables.
	MaxErrorRate float64 `json:"max_error_rate"`
	// SLO sets per-request targets for goodput (optional). It can also be
	// changed after the run; goodput is computed from stored requests.
	SLO *metrics.SLO `json:"slo,omitempty"`
}

// Target is a model on a source, or a model profile. With a profile, its
// source, model and parameters are used; the run's own settings (max tokens,
// temperature, system prompt) take precedence where set, and the run's extra
// body is merged over the profile's.
type Target struct {
	SourceID  string `json:"source_id"`
	Model     string `json:"model"`
	ProfileID string `json:"profile_id,omitempty"`
}

// Prompt describes what each request sends.
type Prompt struct {
	Mode         string `json:"mode"`
	Text         string `json:"text"`         // fixed
	InputTokens  int    `json:"input_tokens"` // synthetic
	SystemPrompt string `json:"system_prompt"`
}

// ValidationError reports an invalid config.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// normalize validates c and canonicalises it in place (trimmed strings,
// sorted unique dimension values).
func (c *Config) normalize() error {
	c.Name = strings.TrimSpace(c.Name)
	switch c.Type {
	case TypeSingle, TypeConcurrencySweep, TypeContextSweep, TypeThinkingComparison, TypeMatrix:
	default:
		return invalid("unsupported test type %q", c.Type)
	}
	if err := c.normalizeTargets(); err != nil {
		return err
	}
	if err := c.normalizeLoad(); err != nil {
		return err
	}
	if err := c.normalizeContext(); err != nil {
		return err
	}
	if err := c.normalizeThinking(); err != nil {
		return err
	}

	// Type presets.
	nLoad, nCtx, nThink := c.loadCount(), len(c.ContextLengths), len(c.ThinkingLevels)
	switch c.Type {
	case TypeSingle:
		if nLoad != 1 || nCtx > 1 || nThink > 1 {
			return invalid("a single benchmark uses one load level, context length and thinking level")
		}
	case TypeConcurrencySweep:
		if nCtx > 1 || nThink > 1 {
			return invalid("a concurrency sweep varies only the load; use a matrix test to vary more")
		}
	case TypeContextSweep:
		if nCtx == 0 {
			return invalid("add at least one context length")
		}
		if nLoad != 1 || nThink > 1 {
			return invalid("a context sweep varies only the context length; use a matrix test to vary more")
		}
	case TypeThinkingComparison:
		if nThink == 0 {
			return invalid("choose the thinking levels to compare")
		}
		if nLoad != 1 || nCtx > 1 {
			return invalid("a thinking comparison varies only the thinking level; use a matrix test to vary more")
		}
	}
	if n := len(c.Targets) * nLoad * max(nCtx, 1) * max(nThink, 1); n > MaxCells {
		return invalid("this plan has %d steps; the limit is %d", n, MaxCells)
	}

	switch {
	case c.RequestsPerCell < 0 || c.RequestsPerCell > MaxRequestsPerCell:
		return invalid("requests per cell must be between 1 and %d", MaxRequestsPerCell)
	case c.DurationSeconds < 0 || c.DurationSeconds > MaxDurationSeconds:
		return invalid("duration per cell must be at most %d seconds", MaxDurationSeconds)
	case c.RequestsPerCell == 0 && c.DurationSeconds == 0:
		return invalid("set requests per cell, a duration per cell, or both")
	case c.WarmupRequests < 0 || c.WarmupRequests > MaxWarmupRequests:
		return invalid("warm-up requests must be between 0 and %d", MaxWarmupRequests)
	case c.TimeoutSeconds < 0 || c.TimeoutSeconds > MaxTimeoutSeconds:
		return invalid("timeout must be between 0 and %d seconds", MaxTimeoutSeconds)
	case c.MaxErrorRate < 0 || c.MaxErrorRate > 1:
		return invalid("max error rate must be between 0 and 1")
	case c.MaxTokens != nil && *c.MaxTokens < 1:
		return invalid("max tokens must be at least 1")
	}
	if c.SLO != nil {
		if err := c.SLO.Validate(); err != nil {
			return invalid("%s", err.Error())
		}
		if c.SLO.IsZero() {
			c.SLO = nil
		}
	}
	return nil
}

func (c *Config) normalizeTargets() error {
	if len(c.Targets) == 0 {
		return invalid("add at least one model")
	}
	if len(c.Targets) > MaxTargets {
		return invalid("at most %d models per run", MaxTargets)
	}
	seen := map[Target]bool{}
	for i := range c.Targets {
		t := &c.Targets[i]
		t.SourceID = strings.TrimSpace(t.SourceID)
		t.Model = strings.TrimSpace(t.Model)
		t.ProfileID = strings.TrimSpace(t.ProfileID)
		if t.ProfileID != "" {
			// Source and model come from the profile.
			t.SourceID, t.Model = "", ""
		} else if t.SourceID == "" || t.Model == "" {
			return invalid("every model needs a source and a model name")
		}
		if seen[*t] {
			return invalid("the same model or profile is listed twice")
		}
		seen[*t] = true
	}
	return nil
}

func (c *Config) normalizeLoad() error {
	switch c.LoadModel {
	case "", LoadClosed:
		c.LoadModel = LoadClosed
		c.ArrivalRates = nil
		c.MaxInFlight = 0
		levels := slices.Clone(c.Concurrency)
		slices.Sort(levels)
		levels = slices.Compact(levels)
		switch {
		case len(levels) == 0:
			return invalid("set the number of concurrent users")
		case len(levels) > MaxLevels:
			return invalid("at most %d concurrency levels", MaxLevels)
		case levels[0] < 1 || levels[len(levels)-1] > MaxConcurrency:
			return invalid("concurrency must be between 1 and %d", MaxConcurrency)
		}
		c.Concurrency = levels
	case LoadOpen:
		c.Concurrency = nil
		rates := slices.Clone(c.ArrivalRates)
		slices.Sort(rates)
		rates = slices.Compact(rates)
		switch {
		case len(rates) == 0:
			return invalid("set at least one arrival rate (requests per second)")
		case len(rates) > MaxLevels:
			return invalid("at most %d arrival rates", MaxLevels)
		case rates[0] <= 0 || rates[len(rates)-1] > MaxArrivalRate:
			return invalid("arrival rates must be above 0 and at most %d requests per second", MaxArrivalRate)
		}
		c.ArrivalRates = rates
		if c.MaxInFlight == 0 {
			c.MaxInFlight = DefaultMaxInFlight
		}
		if c.MaxInFlight < 1 || c.MaxInFlight > MaxInFlightLimit {
			return invalid("max in-flight requests must be between 1 and %d", MaxInFlightLimit)
		}
	default:
		return invalid("unsupported load model %q (use closed or open)", c.LoadModel)
	}
	return nil
}

func (c *Config) normalizeContext() error {
	if len(c.ContextLengths) == 0 {
		switch c.Prompt.Mode {
		case PromptFixed:
			if strings.TrimSpace(c.Prompt.Text) == "" {
				return invalid("prompt text is required")
			}
		case PromptSynthetic:
			if c.Prompt.InputTokens < 16 || c.Prompt.InputTokens > MaxInputTokens {
				return invalid("synthetic prompt length must be between 16 and %d tokens", MaxInputTokens)
			}
		default:
			return invalid("unsupported prompt mode %q", c.Prompt.Mode)
		}
		return nil
	}
	lengths := slices.Clone(c.ContextLengths)
	slices.Sort(lengths)
	lengths = slices.Compact(lengths)
	switch {
	case len(lengths) > MaxLevels:
		return invalid("at most %d context lengths", MaxLevels)
	case lengths[0] < 16 || lengths[len(lengths)-1] > MaxInputTokens:
		return invalid("context lengths must be between 16 and %d tokens", MaxInputTokens)
	}
	c.ContextLengths = lengths
	// Context sweeps always use synthetic prompts of exact lengths.
	c.Prompt.Mode = PromptSynthetic
	c.Prompt.Text = ""
	c.Prompt.InputTokens = 0
	return nil
}

func (c *Config) normalizeThinking() error {
	if len(c.ThinkingLevels) == 0 {
		c.ThinkingStyle = ""
		return nil
	}
	want := map[string]bool{}
	for _, l := range c.ThinkingLevels {
		if !slices.Contains(thinkingOrder, l) {
			return invalid("unknown thinking level %q (use off, low, medium or high)", l)
		}
		want[l] = true
	}
	var levels []string
	for _, l := range thinkingOrder {
		if want[l] {
			levels = append(levels, l)
		}
	}
	c.ThinkingLevels = levels
	if _, err := providers.ParseThinking(providers.ThinkingHigh, c.ThinkingStyle); err != nil {
		return invalid("%s", err.Error())
	}
	return nil
}

func (c *Config) loadCount() int {
	if c.LoadModel == LoadOpen {
		return len(c.ArrivalRates)
	}
	return len(c.Concurrency)
}

type plannedCell struct {
	TargetIndex   int
	ContextTokens int     // 0 = not swept
	Thinking      string  // "" = not swept
	Concurrency   int     // users (closed) or in-flight cap (open)
	ArrivalRate   float64 // open loop only
}

// cellPlan lists cells in execution order: target, then context length,
// then thinking level, with the load innermost so each load sweep runs
// back to back and a server hosting several models switches rarely.
func (c *Config) cellPlan() []plannedCell {
	contexts := c.ContextLengths
	if len(contexts) == 0 {
		contexts = []int{0}
	}
	thinking := c.ThinkingLevels
	if len(thinking) == 0 {
		thinking = []string{""}
	}
	var plan []plannedCell
	for i := range c.Targets {
		for _, ctx := range contexts {
			for _, th := range thinking {
				if c.LoadModel == LoadOpen {
					for _, r := range c.ArrivalRates {
						plan = append(plan, plannedCell{i, ctx, th, c.MaxInFlight, r})
					}
				} else {
					for _, n := range c.Concurrency {
						plan = append(plan, plannedCell{i, ctx, th, n, 0})
					}
				}
			}
		}
	}
	return plan
}

// Validate checks and canonicalises the config without starting a run
// (used for saved definitions). Sources and profiles are resolved at start.
func (c *Config) Validate() error { return c.normalize() }
