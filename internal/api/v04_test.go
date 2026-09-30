package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/metalgeekhub/llmbench/internal/metrics"
	"github.com/metalgeekhub/llmbench/internal/runner"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
)

func (e *testEnv) raw(t *testing.T, method, path, contentType string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func (e *testEnv) waitRun(t *testing.T, id string) runner.RunView {
	t.Helper()
	var v runner.RunView
	deadline := time.Now().Add(10 * time.Second)
	for {
		e.do(t, "GET", "/api/v1/runs/"+id, nil, &v)
		if v.Status != store.StatusRunning || time.Now().After(deadline) {
			return v
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDefinitionsGoodputExportImport(t *testing.T) {
	e := newTestEnv(t)
	llm := newRecordingLLM(t)
	var src sources.Source
	e.do(t, "POST", "/api/v1/sources", map[string]any{"name": "rec", "type": "openai", "base_url": llm.URL, "models": []string{"m"}}, &src)

	// Saved definition, two versions, then run it.
	cfg := map[string]any{
		"type": "concurrency_sweep", "targets": []map[string]string{{"source_id": src.ID, "model": "m"}},
		"prompt": map[string]any{"mode": "fixed", "text": "hi"}, "concurrency": []int{1, 2}, "requests_per_cell": 3,
	}
	var def store.Definition
	if code := e.do(t, "POST", "/api/v1/definitions", map[string]any{"name": "Smoke test", "config": cfg}, &def); code != http.StatusCreated {
		t.Fatalf("create definition status = %d", code)
	}
	cfg["slo"] = map[string]any{"ttft_ms": 60000}
	e.do(t, "PUT", "/api/v1/definitions/"+def.ID, map[string]any{"name": "Smoke test", "config": cfg}, &def)
	if def.Version != 2 {
		t.Errorf("definition version = %d", def.Version)
	}
	var versions struct{ Versions []store.DefinitionVersion }
	e.do(t, "GET", "/api/v1/definitions/"+def.ID+"/versions", nil, &versions)
	if len(versions.Versions) != 2 {
		t.Errorf("versions = %d", len(versions.Versions))
	}

	resp, yamlBody := e.raw(t, "GET", "/api/v1/definitions/"+def.ID+"/export?format=yaml", "", nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), `filename="Smoke-test.yaml"`) ||
		!strings.Contains(string(yamlBody), "kind: llmbench-definition") {
		t.Fatalf("yaml export: %d %v %s", resp.StatusCode, resp.Header, yamlBody)
	}
	resp, body := e.raw(t, "POST", "/api/v1/definitions/import", "application/yaml", yamlBody)
	var imported store.Definition
	_ = json.Unmarshal(body, &imported)
	if resp.StatusCode != http.StatusCreated || imported.ID != def.ID || imported.Version != 3 {
		t.Errorf("definition import: %d %+v", resp.StatusCode, imported)
	}

	var started runner.RunView
	if code := e.do(t, "POST", "/api/v1/definitions/"+def.ID+"/run", nil, &started); code != http.StatusCreated {
		t.Fatalf("run definition status = %d", code)
	}
	run := e.waitRun(t, started.ID)
	if run.Status != store.StatusCompleted || run.DefinitionID != def.ID || run.DefinitionVersion != 3 {
		t.Fatalf("run = %+v", run.TestRun)
	}

	// Goodput: saved SLO (60 s TTFT) passes everything; a what-if target
	// of 0.001 ms passes nothing.
	var gp struct {
		SLO   metrics.SLO
		Cells map[string]metrics.Goodput
	}
	e.do(t, "GET", "/api/v1/runs/"+run.ID+"/goodput", nil, &gp)
	if gp.SLO.TTFTMs == nil || *gp.SLO.TTFTMs != 60000 || gp.Cells[run.Cells[0].ID].Ratio != 1 || gp.Cells[run.Cells[0].ID].Good != 3 {
		t.Errorf("saved-SLO goodput = %+v", gp)
	}
	e.do(t, "GET", "/api/v1/runs/"+run.ID+"/goodput?ttft_ms=0.001", nil, &gp)
	if g := gp.Cells[run.Cells[1].ID]; g.Ratio != 0 || g.Violations["ttft"] != 3 {
		t.Errorf("what-if goodput = %+v", g)
	}
	if code := e.do(t, "GET", "/api/v1/runs/"+run.ID+"/goodput?ttft_ms=abc", nil, nil); code != 400 {
		t.Errorf("bad SLO query status = %d", code)
	}
	var updated runner.RunView
	e.do(t, "PATCH", "/api/v1/runs/"+run.ID, map[string]any{"slo": nil}, &updated)
	if runner.RunSLO(updated.TestRun) != nil {
		t.Errorf("SLO not cleared: %s", updated.Config)
	}
	if code := e.do(t, "GET", "/api/v1/runs/"+run.ID+"/goodput", nil, nil); code != 400 {
		t.Errorf("goodput without SLO status = %d", code)
	}
	e.do(t, "PATCH", "/api/v1/runs/"+run.ID, map[string]any{"slo": map[string]any{"e2e_ms": 30000}}, &updated)
	if s := runner.RunSLO(updated.TestRun); s == nil || *s.E2EMs != 30000 {
		t.Errorf("SLO not saved: %s", updated.Config)
	}

	// CSV exports.
	resp, body = e.raw(t, "GET", "/api/v1/runs/"+run.ID+"/export.csv?table=requests", "", nil)
	rows, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") || len(rows) != 7 || rows[0][len(rows[0])-1] != "slo_met" {
		t.Errorf("requests csv: %d, %d rows, %v", resp.StatusCode, len(rows), err)
	}
	resp, body = e.raw(t, "GET", "/api/v1/runs/"+run.ID+"/export.csv?table=cells", "", nil)
	rows, _ = csv.NewReader(bytes.NewReader(body)).ReadAll()
	if resp.StatusCode != 200 || len(rows) != 3 || rows[0][len(rows[0])-1] != "goodput_per_second" {
		t.Errorf("cells csv: %d, %d rows", resp.StatusCode, len(rows))
	}

	// JSON export → import into another instance.
	resp, bundle := e.raw(t, "GET", "/api/v1/export?runs="+run.ID, "", nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), ".llmbench.json") || !json.Valid(bundle) {
		t.Fatalf("json export: %d %v", resp.StatusCode, resp.Header)
	}
	other := newTestEnv(t)
	resp, body = other.raw(t, "POST", "/api/v1/import", "application/json", bundle)
	var res struct {
		Results []struct{ Status, Name string }
	}
	_ = json.Unmarshal(body, &res)
	if resp.StatusCode != 200 || len(res.Results) != 1 || res.Results[0].Status != "imported" {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	var imp runner.RunView
	other.do(t, "GET", "/api/v1/runs/"+run.ID, nil, &imp)
	if imp.ImportedAt == nil || len(imp.Cells) != 2 || imp.Cells[0].Summary == nil {
		t.Errorf("imported run = %+v", imp.TestRun)
	}
	var reqs struct{ Total int }
	other.do(t, "GET", "/api/v1/requests?run_id="+run.ID, nil, &reqs)
	if reqs.Total != 6 {
		t.Errorf("imported requests = %d", reqs.Total)
	}
	resp, _ = other.raw(t, "POST", "/api/v1/import", "application/json", []byte(`{"format":"nope"}`))
	if resp.StatusCode != 400 {
		t.Errorf("bad import status = %d", resp.StatusCode)
	}
	if code := e.do(t, "GET", "/api/v1/export", nil, nil); code != 400 {
		t.Errorf("export without runs status = %d", code)
	}
}
