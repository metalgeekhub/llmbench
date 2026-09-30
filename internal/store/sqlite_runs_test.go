package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/metalgeekhub/llmbench/internal/metrics"
)

func TestRunsLifecycle(t *testing.T) {
	s, path := openTest(t)
	ctx := context.Background()

	now := time.Now().UTC()
	run := &TestRun{ID: "run1", Name: "sweep", Type: "concurrency_sweep", Status: StatusRunning,
		Config: json.RawMessage(`{"concurrency":[1,2]}`), StartedAt: &now}
	cells := []RunCell{
		{ID: "c1", Index: 0, SourceID: "env-a", SourceName: "a", Model: "m", Concurrency: 1, Status: StatusRunning},
		{ID: "c2", Index: 1, SourceID: "env-a", SourceName: "a", Model: "m", Concurrency: 2, Status: StatusPending},
	}
	if err := s.CreateRun(ctx, run, cells); err != nil {
		t.Fatal(err)
	}

	// Batch-write test requests for cell 1, including a warm-up.
	var batch []RequestRecord
	for i, warm := range []bool{true, false, false} {
		batch = append(batch, RequestRecord{
			Request: Request{ID: string(rune('a' + i)), Kind: KindTest, SourceID: "env-a", SourceName: "a", Model: "m",
				RunID: "run1", CellID: "c1", Warmup: warm, Status: "ok", StartedAt: now, Metrics: metrics.Result{E2EMs: 10}},
			Timeline: &Timeline{OffsetsMs: []float64{1, 2}, Kinds: "cc"},
		})
	}
	if err := s.SaveRequests(ctx, batch); err != nil {
		t.Fatal(err)
	}
	reqs, total, err := s.ListRequests(ctx, RequestFilter{RunID: "run1", CellID: "c1"})
	if err != nil || total != 3 {
		t.Fatalf("run requests = %d, %v", total, err)
	}
	warmups := 0
	for _, r := range reqs {
		if r.Warmup {
			warmups++
		}
	}
	if warmups != 1 {
		t.Errorf("warm-up flag not persisted: %d", warmups)
	}

	cpu := 42.5
	cells[0].Status = StatusCompleted
	cells[0].Summary = &metrics.Aggregate{Requests: 2, Succeeded: 2, OutputThroughput: 123.4, TTFT: metrics.Summary{Count: 2, P50: 50}}
	cells[0].ClientCPUPct = &cpu
	cells[0].FinishedAt = &now
	if err := s.UpdateCell(ctx, &cells[0]); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// A new process marks leftover running runs as interrupted.
	s2, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if n, err := s2.MarkInterruptedRuns(ctx); err != nil || n != 1 {
		t.Fatalf("MarkInterruptedRuns = %d, %v", n, err)
	}
	got, err := s2.GetRun(ctx, "run1")
	if err != nil || got.Status != StatusInterrupted || got.FinishedAt == nil || string(got.Config) != `{"concurrency":[1,2]}` {
		t.Errorf("run = %+v, %v", got, err)
	}
	gotCells, err := s2.ListCells(ctx, "run1")
	if err != nil || len(gotCells) != 2 {
		t.Fatalf("cells = %+v, %v", gotCells, err)
	}
	c1, c2 := gotCells[0], gotCells[1]
	if c1.Status != StatusCompleted || c1.Summary == nil || c1.Summary.OutputThroughput != 123.4 ||
		c1.Summary.TTFT.P50 != 50 || c1.ClientCPUPct == nil || *c1.ClientCPUPct != 42.5 {
		t.Errorf("completed cell = %+v", c1)
	}
	if c2.Status != StatusInterrupted || c2.Summary != nil {
		t.Errorf("pending cell = %+v", c2)
	}

	runs, err := s2.ListRuns(ctx, 10)
	if err != nil || len(runs) != 1 {
		t.Errorf("ListRuns = %+v, %v", runs, err)
	}

	// Deleting a run removes its cells, requests, and timelines.
	if err := s2.DeleteRun(ctx, "run1"); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := s2.ListRequests(ctx, RequestFilter{RunID: "run1"}); total != 0 {
		t.Errorf("requests left after delete: %d", total)
	}
	if _, err := s2.GetTimeline(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("timeline left after delete: %v", err)
	}
	if cells, _ := s2.ListCells(ctx, "run1"); len(cells) != 0 {
		t.Errorf("cells left after delete: %d", len(cells))
	}
	if err := s2.DeleteRun(ctx, "run1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete err = %v", err)
	}
}
