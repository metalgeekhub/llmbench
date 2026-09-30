package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/metalgeekhub/llmbench/internal/metrics"
)

func TestDefinitionsVersioned(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()

	d := &Definition{ID: "d1", Name: "nightly sweep", Config: json.RawMessage(`{"concurrency":[1,2]}`)}
	if err := s.SaveDefinition(ctx, d); err != nil {
		t.Fatal(err)
	}
	if d.Version != 1 {
		t.Errorf("first version = %d", d.Version)
	}
	d.Config = json.RawMessage(`{"concurrency":[1,2,4]}`)
	d.Description = "added 4 users"
	if err := s.SaveDefinition(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDefinition(ctx, "d1")
	if err != nil || got.Version != 2 || string(got.Config) != `{"concurrency":[1,2,4]}` || got.Description != "added 4 users" {
		t.Fatalf("definition = %+v, %v", got, err)
	}
	versions, err := s.ListDefinitionVersions(ctx, "d1")
	if err != nil || len(versions) != 2 || versions[0].Version != 2 || string(versions[1].Config) != `{"concurrency":[1,2]}` {
		t.Errorf("versions = %+v, %v", versions, err)
	}
	if err := s.SaveDefinition(ctx, &Definition{ID: "d2", Name: "nightly sweep", Config: json.RawMessage(`{}`)}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate name err = %v", err)
	}
	if list, _ := s.ListDefinitions(ctx); len(list) != 1 {
		t.Errorf("list = %+v", list)
	}
	if err := s.DeleteDefinition(ctx, "d1"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.ListDefinitionVersions(ctx, "d1"); len(v) != 0 {
		t.Error("versions should be deleted with the definition")
	}
}

func TestImportRunAndExportHelpers(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ttft, tpot := 100.0, 20.0

	run := &TestRun{ID: "imp", Name: "from elsewhere", Type: "single", Status: StatusCompleted, Config: json.RawMessage(`{}`),
		DefinitionID: "def", DefinitionVersion: 3, ImportedAt: &now}
	cells := []RunCell{{ID: "c", Index: 0, SourceID: "remote", SourceName: "remote", Model: "m", Concurrency: 1, Status: StatusCompleted}}
	reqs := []RequestRecord{
		{Request: Request{ID: "r1", Kind: KindTest, SourceID: "remote", SourceName: "remote", Model: "m", CellID: "c", Status: "ok",
			StartedAt: now, Metrics: metrics.Result{TTFTMs: &ttft, TPOTMs: &tpot, E2EMs: 500}}, Timeline: &Timeline{OffsetsMs: []float64{100}, Kinds: "c"}},
		{Request: Request{ID: "r2", Kind: KindTest, SourceID: "remote", SourceName: "remote", Model: "m", CellID: "c", Status: "error",
			StartedAt: now.Add(time.Millisecond), Metrics: metrics.Result{E2EMs: 5}}},
		{Request: Request{ID: "r3", Kind: KindTest, SourceID: "remote", SourceName: "remote", Model: "m", CellID: "c", Status: "ok", Warmup: true,
			StartedAt: now.Add(-time.Second), Metrics: metrics.Result{E2EMs: 5}}},
	}
	if err := s.ImportRun(ctx, run, cells, reqs); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportRun(ctx, run, cells, reqs); !errors.Is(err, ErrConflict) {
		t.Errorf("re-import err = %v", err)
	}

	got, _ := s.GetRun(ctx, "imp")
	if got.ImportedAt == nil || got.DefinitionID != "def" || got.DefinitionVersion != 3 {
		t.Errorf("imported run = %+v", got)
	}

	var ids []string
	var withTimeline int
	if err := s.EachRunRequest(ctx, "imp", func(r Request, tl *Timeline) error {
		ids = append(ids, r.ID)
		if tl != nil {
			withTimeline++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != "r3" || withTimeline != 1 {
		t.Errorf("requests = %v, timelines = %d", ids, withTimeline)
	}

	samples, err := s.RunSLOSamples(ctx, "imp")
	if err != nil || len(samples["c"]) != 2 || !samples["c"][0].OK || samples["c"][1].OK || *samples["c"][0].TTFTMs != 100 {
		t.Errorf("SLO samples = %+v, %v", samples, err)
	}

	if err := s.UpdateRunConfig(ctx, "imp", json.RawMessage(`{"slo":{"ttft_ms":500}}`)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetRun(ctx, "imp"); string(got.Config) != `{"slo":{"ttft_ms":500}}` {
		t.Errorf("config = %s", got.Config)
	}
	if err := s.UpdateRunConfig(ctx, "missing", json.RawMessage(`{}`)); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing err = %v", err)
	}
}
