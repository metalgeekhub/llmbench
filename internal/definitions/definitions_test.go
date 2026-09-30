package definitions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metalgeekhub/llmbench/internal/config"
	"github.com/metalgeekhub/llmbench/internal/metrics"
	"github.com/metalgeekhub/llmbench/internal/runner"
	"github.com/metalgeekhub/llmbench/internal/secrets"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
)

func newService(t *testing.T) (*Service, *runner.Runner, store.Store) {
	t.Helper()
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(llm.Close)
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.New("k")
	srcs := sources.New(st, box, []config.SourceConfig{{Name: "fake", Type: "openai", BaseURL: llm.URL, Models: []string{"m"}}})
	r := runner.New(st, srcs)
	return NewService(st, r), r, st
}

func cfg() runner.Config {
	ttft := 500.0
	return runner.Config{
		Type:            runner.TypeConcurrencySweep,
		Targets:         []runner.Target{{SourceID: "env-fake", Model: "m"}},
		Prompt:          runner.Prompt{Mode: runner.PromptFixed, Text: "hi"},
		Concurrency:     []int{2, 1},
		RequestsPerCell: 2,
		SLO:             &metrics.SLO{TTFTMs: &ttft},
	}
}

func TestDefinitionLifecycle(t *testing.T) {
	s, r, st := newService(t)
	ctx := context.Background()

	d, err := s.Create(ctx, Input{Name: "Nightly sweep", Config: cfg()})
	if err != nil {
		t.Fatal(err)
	}
	var saved runner.Config
	_ = json.Unmarshal(d.Config, &saved)
	if d.Version != 1 || saved.Concurrency[0] != 1 || saved.SLO == nil {
		t.Errorf("definition = %+v config = %+v", d, saved)
	}

	c := cfg()
	c.Concurrency = []int{1, 2, 4}
	d2, err := s.Update(ctx, d.ID, Input{Name: "Nightly sweep", Description: "now with 4 users", Config: c})
	if err != nil || d2.Version != 2 {
		t.Fatalf("update = %+v, %v", d2, err)
	}
	if v, _ := s.Versions(ctx, d.ID); len(v) != 2 {
		t.Errorf("versions = %d", len(v))
	}

	v, err := s.Run(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.DefinitionID != d.ID || v.DefinitionVersion != 2 || v.Name != "Nightly sweep (v2)" || len(v.Cells) != 3 {
		t.Errorf("run = %+v", v.TestRun)
	}
	r.Shutdown(ctx)
	if got, _ := st.GetRun(ctx, v.ID); got.DefinitionVersion != 2 {
		t.Errorf("stored run = %+v", got)
	}

	var ve *ValidationError
	if _, err := s.Create(ctx, Input{Name: "Nightly sweep", Config: cfg()}); !errors.As(err, &ve) {
		t.Errorf("duplicate name err = %v", err)
	}
	bad := cfg()
	bad.Concurrency = nil
	var rve *runner.ValidationError
	if _, err := s.Create(ctx, Input{Name: "broken", Config: bad}); !errors.As(err, &rve) {
		t.Errorf("invalid config err = %v", err)
	}
	if _, err := s.Create(ctx, Input{Name: " ", Config: cfg()}); !errors.As(err, &ve) {
		t.Errorf("empty name err = %v", err)
	}
}

func TestExportImport(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()
	d, err := s.Create(ctx, Input{Name: "Context check", Description: "1k vs 8k", Config: cfg()})
	if err != nil {
		t.Fatal(err)
	}

	y, err := s.Export(ctx, d.ID, "yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(y)
	if !strings.Contains(text, "kind: llmbench-definition") || !strings.Contains(text, "requests_per_cell: 2") || !strings.Contains(text, "ttft_ms: 500") {
		t.Fatalf("yaml = %s", text)
	}
	j, err := s.Export(ctx, d.ID, "json")
	if err != nil || !json.Valid(j) {
		t.Fatalf("json export = %s, %v", j, err)
	}

	// Importing into another instance creates it; importing again with the
	// same name adds a version.
	other, _, _ := newService(t)
	imp, err := other.Import(ctx, y)
	if err != nil || imp.Name != "Context check" || imp.Description != "1k vs 8k" || imp.Version != 1 {
		t.Fatalf("import yaml = %+v, %v", imp, err)
	}
	again, err := other.Import(ctx, j)
	if err != nil || again.ID != imp.ID || again.Version != 2 {
		t.Errorf("import json = %+v, %v", again, err)
	}

	var ve *ValidationError
	for name, body := range map[string]string{
		"not yaml":   "::: :::",
		"wrong kind": "kind: something\nname: x\nconfig: {}",
		"bad config": "kind: llmbench-definition\nname: x\nconfig:\n  type: nope",
	} {
		if _, err := other.Import(ctx, []byte(body)); err == nil {
			t.Errorf("%s: expected error", name)
		} else if name != "bad config" && !errors.As(err, &ve) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := s.Export(ctx, d.ID, "xml"); !errors.As(err, &ve) {
		t.Errorf("bad format err = %v", err)
	}
}
