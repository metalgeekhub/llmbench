package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metalgeekhub/llmbench/internal/config"
	"github.com/metalgeekhub/llmbench/internal/secrets"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
	"github.com/metalgeekhub/llmbench/internal/tokenizer"
)

// fakeServer is an OpenAI-compatible endpoint that streams a few chunks
// with a fixed delay and records concurrency and prompts.
type fakeServer struct {
	*httptest.Server
	chunkDelay time.Duration
	failAll    atomic.Bool

	inFlight, peak atomic.Int64
	mu             sync.Mutex
	prompts        []string
	bodies         []map[string]any
}

func newFakeServer(t *testing.T, chunkDelay time.Duration) *fakeServer {
	t.Helper()
	f := &fakeServer{chunkDelay: chunkDelay}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.inFlight.Add(1)
		defer f.inFlight.Add(-1)
		for {
			p := f.peak.Load()
			if n <= p || f.peak.CompareAndSwap(p, n) {
				break
			}
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		msgs, _ := body["messages"].([]any)
		first, _ := msgs[0].(map[string]any)
		f.mu.Lock()
		f.prompts = append(f.prompts, fmt.Sprint(first["content"]))
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()

		if f.failAll.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":{"message":"overloaded"}}`)
			return
		}
		for i := range 4 {
			select {
			case <-time.After(f.chunkDelay):
			case <-r.Context().Done():
				return
			}
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"t%d \"}}]}\n\n", i)
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(f.Close)
	return f
}

func newTestRunner(t *testing.T, baseURL string) (*Runner, store.Store) {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.New("k")
	srcs := sources.New(st, box, []config.SourceConfig{{Name: "fake", Type: "openai", BaseURL: baseURL, Models: []string{"m"}}})
	return New(st, srcs), st
}

func baseConfig() Config {
	return Config{
		Type:            TypeConcurrencySweep,
		Targets:         []Target{{SourceID: "env-fake", Model: "m"}},
		Prompt:          Prompt{Mode: PromptFixed, Text: "hello"},
		Concurrency:     []int{1, 2, 4},
		RequestsPerCell: 8,
	}
}

func TestConcurrencySweep(t *testing.T) {
	f := newFakeServer(t, 10*time.Millisecond)
	r, st := newTestRunner(t, f.URL)
	ctx := context.Background()

	cfg := baseConfig()
	cfg.Concurrency = []int{4, 1, 2, 2} // normalized to 1, 2, 4
	cfg.WarmupRequests = 2
	cfg.CacheBust = true
	maxTok := 64
	cfg.MaxTokens = &maxTok
	cfg.IgnoreEOS = true

	v, err := r.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "Concurrency sweep · m" || len(v.Cells) != 3 || v.Progress == nil {
		t.Fatalf("started run = %+v", v)
	}
	r.wait(v.ID)

	got, err := r.Get(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusCompleted || got.FinishedAt == nil || got.Progress != nil {
		t.Fatalf("run = %+v", got.TestRun)
	}
	var prevThroughput float64
	for i, c := range got.Cells {
		want := []int{1, 2, 4}[i]
		s := c.Summary
		if c.Concurrency != want || c.Status != store.StatusCompleted || s == nil {
			t.Fatalf("cell %d = %+v", i, c)
		}
		if s.Requests != 8 || s.Succeeded != 8 || s.OutputTokens != 32 || s.TTFT.Count != 8 || s.ITL.Count != 24 {
			t.Errorf("cell %d summary = %+v", i, s)
		}
		// Latency per request is constant, so throughput grows with users.
		if s.RequestThroughput <= prevThroughput*1.4 {
			t.Errorf("cell %d throughput %.1f did not grow from %.1f", i, s.RequestThroughput, prevThroughput)
		}
		prevThroughput = s.RequestThroughput
	}
	if p := f.peak.Load(); p != 4 {
		t.Errorf("peak server concurrency = %d, want 4", p)
	}

	// 3 cells x (2 warm-up + 8 measured), all persisted with run/cell IDs.
	reqs, total, err := st.ListRequests(ctx, store.RequestFilter{RunID: v.ID, Limit: 100})
	if err != nil || total != 30 {
		t.Fatalf("persisted requests = %d, %v", total, err)
	}
	warm := 0
	for _, q := range reqs {
		if q.Warmup {
			warm++
		}
		if q.Kind != store.KindTest || q.CellID == "" || q.Params["concurrency"] == nil {
			t.Errorf("request = %+v", q)
			break
		}
	}
	if warm != 6 {
		t.Errorf("warm-up requests = %d, want 6", warm)
	}

	// Cache busting: every prompt is unique. ignore_eos and max_tokens are sent.
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[string]bool{}
	for _, p := range f.prompts {
		if seen[p] || !strings.HasSuffix(p, "hello") {
			t.Fatalf("prompt %q repeated or malformed", p)
		}
		seen[p] = true
	}
	if b := f.bodies[0]; b["ignore_eos"] != true || b["max_tokens"] != 64.0 {
		t.Errorf("request body = %v", b)
	}
}

func TestDurationBoundCell(t *testing.T) {
	f := newFakeServer(t, 5*time.Millisecond)
	r, _ := newTestRunner(t, f.URL)
	cfg := baseConfig()
	cfg.Type = TypeSingle
	cfg.Concurrency = []int{2}
	cfg.RequestsPerCell = 0
	cfg.DurationSeconds = 0.4

	start := time.Now()
	v, err := r.Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.wait(v.ID)
	elapsed := time.Since(start)
	got, _ := r.Get(context.Background(), v.ID)
	s := got.Cells[0].Summary
	// ~20ms per request, 2 users, 0.4s → about 40 requests.
	if got.Status != store.StatusCompleted || s == nil || s.Requests < 15 || s.Requests > 60 {
		t.Errorf("duration cell: status=%s summary=%+v", got.Status, s)
	}
	if elapsed > 2*time.Second {
		t.Errorf("duration-bound run took %v", elapsed)
	}
}

func TestStopKeepsPartialResults(t *testing.T) {
	f := newFakeServer(t, 20*time.Millisecond)
	r, _ := newTestRunner(t, f.URL)
	cfg := baseConfig()
	cfg.RequestsPerCell = 0
	cfg.DurationSeconds = 30

	v, err := r.Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if p := r.activeRun(v.ID).snapshot(); p.Phase != "measuring" || p.Completed == 0 {
		t.Errorf("progress = %+v", p)
	}
	if err := r.Stop(v.ID); err != nil {
		t.Fatal(err)
	}
	r.wait(v.ID)

	got, _ := r.Get(context.Background(), v.ID)
	if got.Status != store.StatusStopped {
		t.Errorf("run status = %s", got.Status)
	}
	c := got.Cells
	if c[0].Status != store.StatusStopped || c[0].Summary == nil || c[0].Summary.Succeeded == 0 {
		t.Errorf("stopped cell = %+v", c[0])
	}
	if c[1].Status != store.StatusSkipped || c[2].Status != store.StatusSkipped {
		t.Errorf("remaining cells = %s, %s", c[1].Status, c[2].Status)
	}
	if err := r.Stop(v.ID); !errors.Is(err, ErrNotActive) {
		t.Errorf("stop after finish err = %v", err)
	}
}

func TestErrorRateAbort(t *testing.T) {
	f := newFakeServer(t, time.Millisecond)
	f.failAll.Store(true)
	r, _ := newTestRunner(t, f.URL)
	cfg := baseConfig()
	cfg.RequestsPerCell = 1000
	cfg.MaxErrorRate = 0.5

	v, err := r.Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.wait(v.ID)
	got, _ := r.Get(context.Background(), v.ID)
	c := got.Cells
	if got.Status != store.StatusAborted || !strings.Contains(got.Error, "error rate") {
		t.Errorf("run = %s %q", got.Status, got.Error)
	}
	if c[0].Status != store.StatusAborted || c[0].Summary.Failed < minSampleForAbort || c[0].Summary.Requests > 100 ||
		c[0].Summary.ErrorsByType["http_5xx"] != c[0].Summary.Failed {
		t.Errorf("aborted cell = %+v summary=%+v", c[0], c[0].Summary)
	}
	if c[1].Status != store.StatusSkipped {
		t.Errorf("next cell = %s", c[1].Status)
	}
}

func TestOneRunAtATimeAndDelete(t *testing.T) {
	f := newFakeServer(t, 20*time.Millisecond)
	r, _ := newTestRunner(t, f.URL)
	ctx := context.Background()
	cfg := baseConfig()
	cfg.DurationSeconds = 30
	cfg.RequestsPerCell = 0

	v, err := r.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start(ctx, baseConfig()); !errors.Is(err, ErrBusy) {
		t.Errorf("second start err = %v", err)
	}
	if err := r.Delete(ctx, v.ID); !errors.Is(err, ErrActive) {
		t.Errorf("delete active err = %v", err)
	}
	list, err := r.List(ctx, 10)
	if err != nil || len(list) != 1 || list[0].Progress == nil {
		t.Errorf("list = %+v, %v", list, err)
	}

	r.Shutdown(ctx)
	got, _ := r.Get(ctx, v.ID)
	if got.Status != store.StatusStopped {
		t.Errorf("status after shutdown = %s", got.Status)
	}
	if err := r.Delete(ctx, v.ID); err != nil {
		t.Errorf("delete finished run: %v", err)
	}
	if _, err := r.Get(ctx, v.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get deleted run err = %v", err)
	}
}

func TestUnreachableSourceFailsRun(t *testing.T) {
	r, _ := newTestRunner(t, "http://127.0.0.1:1")
	cfg := baseConfig()
	cfg.Concurrency = []int{1}
	cfg.RequestsPerCell = 3
	v, err := r.Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.wait(v.ID)
	got, _ := r.Get(context.Background(), v.ID)
	// Connection errors are request failures, not a cell failure.
	s := got.Cells[0].Summary
	if got.Status != store.StatusCompleted || s == nil || s.Failed != 3 || s.ErrorsByType["connection"] != 3 {
		t.Errorf("run = %s, summary = %+v", got.Status, s)
	}
}

func TestValidation(t *testing.T) {
	r, _ := newTestRunner(t, "http://127.0.0.1:1")
	mutate := map[string]func(c *Config){
		"bad type":         func(c *Config) { c.Type = "chaos" },
		"no targets":       func(c *Config) { c.Targets = nil },
		"empty model":      func(c *Config) { c.Targets[0].Model = " " },
		"duplicate target": func(c *Config) { c.Targets = append(c.Targets, c.Targets[0]) },
		"unknown source":   func(c *Config) { c.Targets[0].SourceID = "nope" },
		"empty prompt":     func(c *Config) { c.Prompt.Text = "  " },
		"tiny synthetic":   func(c *Config) { c.Prompt = Prompt{Mode: PromptSynthetic, InputTokens: 3} },
		"bad prompt mode":  func(c *Config) { c.Prompt.Mode = "dataset" },
		"single with 2":    func(c *Config) { c.Type = TypeSingle; c.Concurrency = []int{1, 2} },
		"zero users":       func(c *Config) { c.Concurrency = []int{0} },
		"too many users":   func(c *Config) { c.Concurrency = []int{MaxConcurrency + 1} },
		"no stop limit":    func(c *Config) { c.RequestsPerCell = 0 },
		"negative warmup":  func(c *Config) { c.WarmupRequests = -1 },
		"error rate > 1":   func(c *Config) { c.MaxErrorRate = 1.5 },
		"zero max tokens":  func(c *Config) { z := 0; c.MaxTokens = &z },
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

func TestSyntheticPrompt(t *testing.T) {
	cfg := baseConfig()
	cfg.Prompt = Prompt{Mode: PromptSynthetic, InputTokens: 500, SystemPrompt: "sys"}
	cfg.CacheBust = true
	b, err := newPromptBuilder(&cfg, userPrompt(&cfg, 0), resolvedTarget{Model: "m"}, "")
	if err != nil {
		t.Fatal(err)
	}
	req := b.request("m")
	if len(req.Messages) != 2 || !strings.HasPrefix(req.Messages[0].Content, "[") ||
		!strings.HasSuffix(req.Messages[1].Content, tokenizer.DefaultInstruction) {
		t.Fatalf("messages = %+v", req.Messages)
	}
	if b.request("m").Messages[0].Content == req.Messages[0].Content {
		t.Error("cache-bust marker should differ per request")
	}
}

func TestCPUSampler(t *testing.T) {
	s := startCPU()
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = make([]byte, 1024) // keep the GC busy
	}
	pct := s.stop()
	if pct == nil || *pct <= 0 || *pct > 100 {
		t.Errorf("cpu = %v", pct)
	}
}

func TestProfileTargetsAndPrecedence(t *testing.T) {
	f := newFakeServer(t, time.Millisecond)
	r, st := newTestRunner(t, f.URL)
	ctx := context.Background()

	temp := 0.9
	profMax := 999
	if err := st.CreateProfile(ctx, &store.Profile{ID: "p-think", Name: "m thinking-off", SourceID: "env-fake", Model: "m",
		Params: store.ChatParams{Thinking: "off", ThinkingStyle: "chat_template_kwargs", Temperature: &temp, MaxTokens: &profMax,
			SystemPrompt: "profile system", ExtraBody: map[string]any{"top_k": 5, "shared": "profile"}}}); err != nil {
		t.Fatal(err)
	}

	cfg := baseConfig()
	cfg.Type = TypeSingle
	cfg.Concurrency = []int{1}
	cfg.RequestsPerCell = 2
	runMax := 64
	cfg.MaxTokens = &runMax // run setting wins over the profile's
	cfg.ExtraBody = map[string]any{"shared": "run"}
	// A plain target and a profile of the same model are different targets.
	cfg.Targets = []Target{{SourceID: "env-fake", Model: "m"}, {ProfileID: "p-think", SourceID: "ignored", Model: "ignored"}}

	v, err := r.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "Benchmark · m +1" || v.Cells[1].ProfileName != "m thinking-off" || v.Cells[1].Model != "m" || v.Cells[1].SourceName != "fake" {
		t.Fatalf("run = %+v cells = %+v", v.TestRun, v.Cells)
	}
	r.wait(v.ID)

	f.mu.Lock()
	var profileBody map[string]any
	for _, b := range f.bodies {
		if b["temperature"] == 0.9 {
			profileBody = b
		}
	}
	f.mu.Unlock()
	if profileBody == nil {
		t.Fatal("no request used the profile's parameters")
	}
	kw, _ := profileBody["chat_template_kwargs"].(map[string]any)
	if kw["enable_thinking"] != false || profileBody["max_tokens"] != 64.0 || profileBody["top_k"] != 5.0 || profileBody["shared"] != "run" {
		t.Errorf("profile request body = %v", profileBody)
	}
	msgs := profileBody["messages"].([]any)
	if first := msgs[0].(map[string]any); first["role"] != "system" || first["content"] != "profile system" {
		t.Errorf("profile system prompt not used: %v", msgs)
	}

	reqs, _, _ := st.ListRequests(ctx, store.RequestFilter{CellID: v.Cells[1].ID})
	if len(reqs) == 0 || reqs[0].Params["thinking"] != "off" || reqs[0].Params["profile"] != "m thinking-off" {
		t.Errorf("recorded params = %+v", reqs)
	}

	// Unknown or duplicate profiles are rejected.
	cfg.Targets = []Target{{ProfileID: "nope"}}
	var ve *ValidationError
	if _, err := r.Start(ctx, cfg); !errors.As(err, &ve) {
		t.Errorf("unknown profile err = %v", err)
	}
	cfg.Targets = []Target{{ProfileID: "p-think"}, {ProfileID: "p-think"}}
	if _, err := r.Start(ctx, cfg); !errors.As(err, &ve) {
		t.Errorf("duplicate profile err = %v", err)
	}
}

func TestLiveSeries(t *testing.T) {
	f := newFakeServer(t, 5*time.Millisecond)
	r, st := newTestRunner(t, f.URL)
	r.LiveInterval = 50 * time.Millisecond
	ctx := context.Background()

	cfg := baseConfig()
	cfg.Concurrency = []int{1, 4}
	cfg.RequestsPerCell = 0
	cfg.DurationSeconds = 0.4

	v, err := r.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	u, err := r.Live(ctx, v.ID, 0)
	if err != nil || u.Done || len(u.Points) == 0 || u.Next != len(u.Points) || u.Run.Progress == nil || u.Run.Timeline != nil {
		t.Fatalf("live update = %+v, %v", u, err)
	}
	// Incremental: asking from Next returns only newer points.
	time.Sleep(120 * time.Millisecond)
	u2, _ := r.Live(ctx, v.ID, u.Next)
	if len(u2.Points) == 0 || u2.Next <= u.Next || u2.Points[0].T <= u.Points[len(u.Points)-1].T {
		t.Errorf("incremental update = %d points, next %d (was %d)", len(u2.Points), u2.Next, u.Next)
	}
	r.wait(v.ID)

	done, _ := r.Live(ctx, v.ID, u2.Next)
	if !done.Done || len(done.Points) != 0 {
		t.Errorf("finished live update = %+v", done)
	}

	// The series is persisted with the run.
	run, _ := st.GetRun(ctx, v.ID)
	var points []LivePoint
	if err := json.Unmarshal(run.Timeline, &points); err != nil || len(points) < 10 {
		t.Fatalf("persisted timeline: %d points, %v", len(points), err)
	}
	var sawCell1, sawTraffic bool
	maxUsers := 0
	for _, p := range points {
		if p.Cell == 1 {
			sawCell1 = true
		}
		if p.RPS > 0 && p.TPS > 0 && p.TTFTP50 != nil {
			sawTraffic = true
		}
		maxUsers = max(maxUsers, p.Users)
	}
	if !sawCell1 || !sawTraffic || maxUsers != 4 {
		t.Errorf("series: cell1=%v traffic=%v maxUsers=%d", sawCell1, sawTraffic, maxUsers)
	}
}
