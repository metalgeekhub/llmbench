package export

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/metalgeekhub/llmbench/internal/metrics"
	"github.com/metalgeekhub/llmbench/internal/store"
)

func openStore(t *testing.T) *store.SQLite {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func f(v float64) *float64 { return &v }

// seed creates a finished run with two cells and a few requests.
func seed(t *testing.T, st store.Store) (store.TestRun, []store.RunCell) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	run := store.TestRun{ID: "run1", Name: "sweep, \"quoted\"", Type: "concurrency_sweep", Status: store.StatusCompleted,
		Config: json.RawMessage(`{"type":"concurrency_sweep","slo":{"ttft_ms":150}}`), CreatedAt: now, StartedAt: &now, FinishedAt: &now,
		Timeline: json.RawMessage(`[{"t":1,"cell":0}]`)}
	rate := 2.5
	cells := []store.RunCell{
		{ID: "c1", Index: 0, SourceID: "env-a", SourceName: "a", Model: "m", Concurrency: 1, Status: store.StatusCompleted,
			Summary: &metrics.Aggregate{Requests: 2, Succeeded: 2, WallSeconds: 2, OutputThroughput: 50, TTFT: metrics.Summary{Count: 2, P50: 100}}},
		{ID: "c2", Index: 1, SourceID: "env-a", SourceName: "a", Model: "m", Concurrency: 64, ArrivalRate: &rate, Status: store.StatusCompleted,
			Summary: &metrics.Aggregate{Requests: 2, Succeeded: 1, Failed: 1, WallSeconds: 1}},
	}
	if err := st.CreateRun(ctx, &run, cells); err != nil {
		t.Fatal(err)
	}
	for _, c := range cells {
		if err := st.UpdateCell(ctx, &c); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(id, cell, status string, ttft *float64, warm bool, offset time.Duration) store.RequestRecord {
		return store.RequestRecord{
			Request: store.Request{ID: id, Kind: store.KindTest, SourceID: "env-a", SourceName: "a", Model: "m", RunID: "run1", CellID: cell,
				Status: status, Warmup: warm, StartedAt: now.Add(offset), Metrics: metrics.Result{TTFTMs: ttft, E2EMs: 300, OutputTokens: 10}},
			Timeline: &store.Timeline{OffsetsMs: []float64{1, 2}, Kinds: "cc"},
		}
	}
	if err := st.SaveRequests(ctx, []store.RequestRecord{
		mk("w", "c1", "ok", f(90), true, 0),
		mk("r1", "c1", "ok", f(100), false, time.Millisecond),
		mk("r2", "c1", "ok", f(200), false, 2*time.Millisecond),
		mk("r3", "c2", "ok", f(120), false, 3*time.Millisecond),
		mk("r4", "c2", "error", nil, false, 4*time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}
	return run, cells
}

func TestBundleRoundTrip(t *testing.T) {
	src := openStore(t)
	run, _ := seed(t, src)
	ctx := context.Background()

	var buf bytes.Buffer
	if err := WriteBundle(ctx, &buf, src, []string{run.ID}, "test"); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(buf.Bytes()) {
		t.Fatalf("export is not valid JSON: %s", buf.String()[:200])
	}
	b, err := ReadBundle(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if b.Format != Format || b.AppVersion != "test" || len(b.Runs) != 1 {
		t.Fatalf("bundle = %+v", b)
	}
	re := b.Runs[0]
	if re.Run.Name != run.Name || len(re.Cells) != 2 || len(re.Requests) != 5 || re.Requests[0].Timeline == nil || string(re.Run.Timeline) == "" {
		t.Fatalf("run export = %+v", re.Run)
	}

	// Import into a fresh instance; importing again is a no-op.
	dst := openStore(t)
	res, err := Import(ctx, dst, b)
	if err != nil || len(res) != 1 || res[0].Status != "imported" {
		t.Fatalf("import = %+v, %v", res, err)
	}
	res, _ = Import(ctx, dst, b)
	if res[0].Status != "skipped" {
		t.Errorf("second import = %+v", res)
	}
	got, err := dst.GetRun(ctx, run.ID)
	if err != nil || got.ImportedAt == nil || got.Name != run.Name {
		t.Fatalf("imported run = %+v, %v", got, err)
	}
	cells, _ := dst.ListCells(ctx, run.ID)
	if len(cells) != 2 || cells[1].ArrivalRate == nil || *cells[1].ArrivalRate != 2.5 || cells[0].Summary.OutputThroughput != 50 {
		t.Errorf("imported cells = %+v", cells)
	}
	reqs, total, _ := dst.ListRequests(ctx, store.RequestFilter{RunID: run.ID, Limit: 10})
	if total != 5 {
		t.Errorf("imported requests = %d", total)
	}
	if tl, err := dst.GetTimeline(ctx, reqs[0].ID); err != nil || len(tl.OffsetsMs) != 2 {
		t.Errorf("imported timeline = %+v, %v", tl, err)
	}
}

func TestReadBundleRejects(t *testing.T) {
	for name, body := range map[string]string{
		"garbage":      "not json",
		"other format": `{"format":"something","version":1,"runs":[]}`,
	} {
		if _, err := ReadBundle(strings.NewReader(body)); !errors.Is(err, ErrNotBundle) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := ReadBundle(strings.NewReader(`{"format":"llmbench","version":99,"runs":[]}`)); err == nil {
		t.Error("newer version should be rejected")
	}
}

func TestCSV(t *testing.T) {
	st := openStore(t)
	run, cells := seed(t, st)
	ctx := context.Background()
	slo := metrics.SLO{TTFTMs: f(150)}

	var reqBuf bytes.Buffer
	if err := WriteRequestsCSV(ctx, &reqBuf, st, run.ID, cells, &slo); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&reqBuf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 {
		t.Fatalf("request rows = %d", len(rows))
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	byID := map[string][]string{}
	for _, r := range rows[1:] {
		byID[r[col["request_id"]]] = r
	}
	if byID["w"][col["warmup"]] != "true" || byID["w"][col["slo_met"]] != "" {
		t.Errorf("warm-up row = %v", byID["w"])
	}
	if byID["r1"][col["slo_met"]] != "true" || byID["r2"][col["slo_met"]] != "false" || byID["r4"][col["slo_met"]] != "false" {
		t.Errorf("slo_met = %v %v %v", byID["r1"][col["slo_met"]], byID["r2"][col["slo_met"]], byID["r4"][col["slo_met"]])
	}
	if byID["r4"][col["ttft_ms"]] != "" || byID["r3"][col["arrival_rate"]] != "2.5" || byID["r1"][col["ttft_ms"]] != "100" {
		t.Errorf("values = %v", byID["r4"])
	}

	goodput := map[string]metrics.Goodput{"c1": {Good: 1, Ratio: 0.5, PerSecond: 0.5}}
	var cellBuf bytes.Buffer
	if err := WriteCellsCSV(&cellBuf, cells, goodput); err != nil {
		t.Fatal(err)
	}
	crows, err := csv.NewReader(&cellBuf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(crows) != 3 {
		t.Fatalf("cell rows = %d", len(crows))
	}
	ccol := map[string]int{}
	for i, h := range crows[0] {
		ccol[h] = i
	}
	for _, r := range crows[1:] {
		if len(r) != len(crows[0]) {
			t.Fatalf("row width %d != header %d", len(r), len(crows[0]))
		}
	}
	if crows[1][ccol["ttft_ms_p50"]] != "100" || crows[1][ccol["goodput_ratio"]] != "0.5" || crows[2][ccol["goodput_ratio"]] != "" {
		t.Errorf("cell rows = %v", crows[1:])
	}
}
