package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/metalgeekhub/llmbench/internal/providers"
	"github.com/metalgeekhub/llmbench/internal/store"
	"github.com/metalgeekhub/llmbench/internal/tokenizer"
)

func TestMatrixPlanAndDimensions(t *testing.T) {
	f := newFakeServer(t, time.Millisecond)
	r, st := newTestRunner(t, f.URL)
	ctx := context.Background()

	cfg := baseConfig()
	cfg.Type = TypeMatrix
	cfg.ContextLengths = []int{512, 64, 64}      // normalized to 64, 512
	cfg.ThinkingLevels = []string{"high", "off"} // normalized to off, high
	cfg.ThinkingStyle = providers.StyleChatTemplate
	cfg.Concurrency = []int{2, 1}
	cfg.RequestsPerCell = 2
	cfg.Prompt = Prompt{Mode: PromptFixed, Text: "ignored when sweeping context"}

	v, err := r.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// 1 target x 2 contexts x 2 thinking x 2 loads, load innermost.
	if len(v.Cells) != 8 {
		t.Fatalf("cells = %d", len(v.Cells))
	}
	want := []struct {
		ctx   int
		think string
		users int
	}{{64, "off", 1}, {64, "off", 2}, {64, "high", 1}, {64, "high", 2}, {512, "off", 1}, {512, "off", 2}, {512, "high", 1}, {512, "high", 2}}
	for i, w := range want {
		c := v.Cells[i]
		if c.ContextTokens != w.ctx || c.Thinking != w.think || c.Concurrency != w.users || c.ArrivalRate != nil {
			t.Errorf("cell %d = ctx %d think %q users %d", i, c.ContextTokens, c.Thinking, c.Concurrency)
		}
	}
	r.wait(v.ID)
	got, _ := r.Get(ctx, v.ID)
	if got.Status != store.StatusCompleted {
		t.Fatalf("status = %s", got.Status)
	}
	if got.Cells[0].SampleOutput == "" {
		t.Error("sample output not captured")
	}

	// Each request carried its cell's prompt length and thinking setting.
	reqs, _, _ := st.ListRequests(ctx, store.RequestFilter{RunID: v.ID, Limit: 100})
	for _, q := range reqs {
		if q.Params["context_tokens"] == nil || q.Params["thinking"] == nil {
			t.Fatalf("request params = %v", q.Params)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	lengths := map[int]bool{}
	thinkingSeen := map[any]bool{}
	for _, b := range f.bodies {
		msgs := b["messages"].([]any)
		user := msgs[len(msgs)-1].(map[string]any)["content"].(string)
		n := tokenizer.Count(user)
		switch {
		case n >= 60 && n <= 70:
			lengths[64] = true
		case n >= 500 && n <= 520:
			lengths[512] = true
		default:
			t.Errorf("unexpected prompt length %d", n)
		}
		kw, _ := b["chat_template_kwargs"].(map[string]any)
		thinkingSeen[kw["enable_thinking"]] = true
	}
	if !lengths[64] || !lengths[512] || !thinkingSeen[true] || !thinkingSeen[false] {
		t.Errorf("lengths=%v thinking=%v", lengths, thinkingSeen)
	}
}

func TestOpenLoopRate(t *testing.T) {
	f := newFakeServer(t, 20*time.Millisecond) // ~80ms per request
	r, _ := newTestRunner(t, f.URL)
	ctx := context.Background()

	cfg := baseConfig()
	cfg.Type = TypeConcurrencySweep
	cfg.LoadModel = LoadOpen
	cfg.ArrivalRates = []float64{50}
	cfg.RequestsPerCell = 0
	cfg.DurationSeconds = 1.5

	v, err := r.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "Rate sweep · m" || v.Cells[0].ArrivalRate == nil || *v.Cells[0].ArrivalRate != 50 || v.Cells[0].Concurrency != DefaultMaxInFlight {
		t.Fatalf("run = %+v cell = %+v", v.TestRun, v.Cells[0])
	}
	r.wait(v.ID)
	got, _ := r.Get(ctx, v.ID)
	c := got.Cells[0]
	s := c.Summary
	// Open loop keeps sending at ~50/s even though each request takes
	// ~80ms, so several run concurrently: ~75 requests over 1.5s.
	if s == nil || s.Requests < 45 || s.Requests > 110 {
		t.Fatalf("open-loop requests = %+v", s)
	}
	if p := f.peak.Load(); p < 2 {
		t.Errorf("open loop should overlap requests, peak = %d", p)
	}
	if c.ScheduleLagP95Ms == nil || *c.ScheduleLagP95Ms > 50 {
		t.Errorf("schedule lag p95 = %v", c.ScheduleLagP95Ms)
	}
}

func TestOpenLoopInFlightCap(t *testing.T) {
	f := newFakeServer(t, 25*time.Millisecond) // ~100ms per request
	r, _ := newTestRunner(t, f.URL)
	cfg := baseConfig()
	cfg.Type = TypeSingle
	cfg.LoadModel = LoadOpen
	cfg.ArrivalRates = []float64{200}
	cfg.MaxInFlight = 3
	cfg.RequestsPerCell = 30

	v, err := r.Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.wait(v.ID)
	got, _ := r.Get(context.Background(), v.ID)
	c := got.Cells[0]
	if p := f.peak.Load(); p > 3 {
		t.Errorf("in-flight cap exceeded: peak %d", p)
	}
	// 200/s offered but only ~30/s possible: arrivals wait, so lag is large.
	if c.Summary.Requests != 30 || c.ScheduleLagP95Ms == nil || *c.ScheduleLagP95Ms < 100 {
		t.Errorf("requests = %d, lag p95 = %v", c.Summary.Requests, c.ScheduleLagP95Ms)
	}
}

func TestThinkingComparisonUsesProfileStyle(t *testing.T) {
	f := newFakeServer(t, time.Millisecond)
	r, st := newTestRunner(t, f.URL)
	ctx := context.Background()
	if err := st.CreateProfile(ctx, &store.Profile{ID: "oai", Name: "oai", SourceID: "env-fake", Model: "m",
		Params: store.ChatParams{Thinking: "low", ThinkingStyle: providers.StyleReasoningEffort}}); err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig()
	cfg.Type = TypeThinkingComparison
	cfg.Targets = []Target{{ProfileID: "oai"}}
	cfg.ThinkingLevels = []string{"medium", "high"}
	// Fallback only: the profile defines its own style, which wins.
	cfg.ThinkingStyle = providers.StyleChatTemplate
	cfg.Concurrency = []int{1}
	cfg.RequestsPerCell = 1

	v, err := r.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.wait(v.ID)
	f.mu.Lock()
	defer f.mu.Unlock()
	efforts := map[any]bool{}
	for _, b := range f.bodies {
		if _, ok := b["chat_template_kwargs"]; ok {
			t.Errorf("profile style should be used: %v", b)
		}
		efforts[b["reasoning_effort"]] = true
	}
	if !efforts["medium"] || !efforts["high"] || efforts["low"] {
		t.Errorf("reasoning efforts sent = %v", efforts)
	}
}

func TestMatrixValidation(t *testing.T) {
	r, _ := newTestRunner(t, "http://127.0.0.1:1")
	mutate := map[string]func(c *Config){
		"context sweep without lengths": func(c *Config) { c.Type = TypeContextSweep; c.Concurrency = []int{1} },
		"context sweep with two loads":  func(c *Config) { c.Type = TypeContextSweep; c.ContextLengths = []int{64} },
		"thinking without style": func(c *Config) {
			c.Type = TypeThinkingComparison
			c.Concurrency = []int{1}
			c.ThinkingLevels = []string{"off"}
		},
		"unknown thinking level": func(c *Config) {
			c.Type = TypeMatrix
			c.ThinkingLevels = []string{"max"}
			c.ThinkingStyle = providers.StyleReasoningEffort
		},
		"tiny context":            func(c *Config) { c.Type = TypeMatrix; c.ContextLengths = []int{8} },
		"open loop without rates": func(c *Config) { c.LoadModel = LoadOpen },
		"open loop zero rate":     func(c *Config) { c.LoadModel = LoadOpen; c.ArrivalRates = []float64{0} },
		"open loop in-flight too big": func(c *Config) {
			c.LoadModel = LoadOpen
			c.ArrivalRates = []float64{1}
			c.MaxInFlight = MaxInFlightLimit + 1
		},
		"unknown load model":    func(c *Config) { c.LoadModel = "burst" },
		"sweep varying context": func(c *Config) { c.ContextLengths = []int{64, 128} },
		"too many cells": func(c *Config) {
			// 2 x 16 x 16 = 512 > MaxCells.
			c.Type = TypeMatrix
			c.Targets = append(c.Targets, Target{SourceID: "env-fake", Model: "other"})
			c.Concurrency = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
			c.ContextLengths = []int{16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536, 131072, 262144, 524288}
		},
	}
	for name, fn := range mutate {
		t.Run(name, func(t *testing.T) {
			cfg := baseConfig()
			fn(&cfg)
			var ve *ValidationError
			if _, err := r.Start(context.Background(), cfg); !errors.As(err, &ve) {
				t.Errorf("err = %v, want ValidationError", err)
			}
		})
	}
}
