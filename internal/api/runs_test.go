package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/metalgeekhub/llmbench/internal/runner"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
)

func TestRunsAPI(t *testing.T) {
	e := newTestEnv(t)

	var src sources.Source
	e.do(t, "POST", "/api/v1/sources", map[string]any{
		"name": "mine", "type": "openai", "base_url": e.llm.URL + "/v1", "api_key": "sk-ui", "models": []string{"fake-model"},
	}, &src)

	cfg := map[string]any{
		"type":              "concurrency_sweep",
		"targets":           []map[string]string{{"source_id": src.ID, "model": "fake-model"}},
		"prompt":            map[string]any{"mode": "fixed", "text": "hi"},
		"concurrency":       []int{1, 2},
		"requests_per_cell": 3,
	}
	var started runner.RunView
	if code := e.do(t, "POST", "/api/v1/runs", cfg, &started); code != http.StatusCreated {
		t.Fatalf("start status = %d", code)
	}
	if started.Status != store.StatusRunning || len(started.Cells) != 2 {
		t.Fatalf("started = %+v", started)
	}

	// Poll until finished, as the UI does.
	var got runner.RunView
	deadline := time.Now().Add(10 * time.Second)
	for {
		e.do(t, "GET", "/api/v1/runs/"+started.ID, nil, &got)
		if got.Status != store.StatusRunning || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got.Status != store.StatusCompleted || got.Progress != nil {
		t.Fatalf("run = %+v", got.TestRun)
	}
	for _, c := range got.Cells {
		if c.Status != store.StatusCompleted || c.Summary == nil || c.Summary.Succeeded != 3 || c.Summary.TTFT.Count != 3 {
			t.Errorf("cell = %+v summary = %+v", c, c.Summary)
		}
	}

	var reqs struct {
		Requests []store.Request
		Total    int
	}
	e.do(t, "GET", "/api/v1/requests?run_id="+started.ID+"&cell_id="+got.Cells[1].ID, nil, &reqs)
	if reqs.Total != 3 || reqs.Requests[0].Kind != "test" {
		t.Errorf("cell requests = %+v", reqs)
	}

	var list struct{ Runs []runner.RunView }
	e.do(t, "GET", "/api/v1/runs", nil, &list)
	if len(list.Runs) != 1 || len(list.Runs[0].Cells) != 2 {
		t.Errorf("list = %+v", list)
	}

	var dist struct {
		Metric string
		Cells  map[string][]float64
	}
	if code := e.do(t, "GET", "/api/v1/runs/"+started.ID+"/distribution?metric=e2e_ms", nil, &dist); code != 200 {
		t.Fatalf("distribution status = %d", code)
	}
	if dist.Metric != "e2e_ms" || len(dist.Cells) != 2 || len(dist.Cells[got.Cells[0].ID]) != 3 || dist.Cells[got.Cells[0].ID][0] <= 0 {
		t.Errorf("distribution = %+v", dist)
	}
	if code := e.do(t, "GET", "/api/v1/runs/"+started.ID+"/distribution?metric=secret_column", nil, nil); code != 400 {
		t.Errorf("unknown metric status = %d", code)
	}

	var apiErr struct{ Error string }
	bad := map[string]any{"type": "single", "targets": []any{}, "prompt": map[string]any{"mode": "fixed", "text": "x"}}
	if code := e.do(t, "POST", "/api/v1/runs", bad, &apiErr); code != http.StatusBadRequest || apiErr.Error == "" {
		t.Errorf("invalid config: %d %+v", code, apiErr)
	}
	if code := e.do(t, "POST", "/api/v1/runs/"+started.ID+"/stop", nil, nil); code != http.StatusConflict {
		t.Errorf("stop finished run status = %d", code)
	}
	if code := e.do(t, "DELETE", "/api/v1/runs/"+started.ID, nil, nil); code != http.StatusNoContent {
		t.Errorf("delete status = %d", code)
	}
	if code := e.do(t, "GET", "/api/v1/runs/"+started.ID, nil, nil); code != http.StatusNotFound {
		t.Errorf("get deleted status = %d", code)
	}
}
