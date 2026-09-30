package export

import (
	"context"
	"encoding/csv"
	"io"
	"strconv"
	"time"

	"github.com/metalgeekhub/llmbench/internal/metrics"
	"github.com/metalgeekhub/llmbench/internal/store"
)

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func ptr(v *float64) string {
	if v == nil {
		return ""
	}
	return num(*v)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// cellDims are the dimension columns shared by both CSV files.
var cellDimHeader = []string{"cell_index", "model", "source", "profile", "context_tokens", "thinking", "concurrency", "arrival_rate"}

func cellDims(c store.RunCell) []string {
	return []string{strconv.Itoa(c.Index), c.Model, c.SourceName, c.ProfileName, strconv.Itoa(c.ContextTokens), c.Thinking,
		strconv.Itoa(c.Concurrency), ptr(c.ArrivalRate)}
}

// WriteRequestsCSV writes one row per request of a run (warm-ups included,
// flagged). If slo is set, a slo_met column is added.
func WriteRequestsCSV(ctx context.Context, w io.Writer, st store.Store, runID string, cells []store.RunCell, slo *metrics.SLO) error {
	byID := make(map[string]store.RunCell, len(cells))
	for _, c := range cells {
		byID[c.ID] = c
	}
	cw := csv.NewWriter(w)
	header := append([]string{"request_id", "cell_id"}, cellDimHeader...)
	header = append(header, "warmup", "status", "http_status", "error_type", "error_message", "started_at",
		"ttft_ms", "ttfat_ms", "e2e_ms", "tpot_ms", "output_tps", "prefill_tps",
		"input_tokens", "output_tokens", "reasoning_tokens", "cached_tokens", "tokens_estimated",
		"chunk_count", "itl_mean_ms", "itl_p50_ms", "itl_p99_ms", "itl_max_ms")
	if slo != nil {
		header = append(header, "slo_met")
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	err := st.EachRunRequest(ctx, runID, func(r store.Request, _ *store.Timeline) error {
		m := r.Metrics
		row := append([]string{r.ID, r.CellID}, cellDims(byID[r.CellID])...)
		row = append(row, boolStr(r.Warmup), r.Status, strconv.Itoa(r.HTTPStatus), r.ErrorType, r.ErrorMessage,
			r.StartedAt.UTC().Format(time.RFC3339Nano),
			ptr(m.TTFTMs), ptr(m.TTFATMs), num(m.E2EMs), ptr(m.TPOTMs), ptr(m.OutputTPS), ptr(m.PrefillTPS),
			strconv.Itoa(m.InputTokens), strconv.Itoa(m.OutputTokens), strconv.Itoa(m.ReasoningTokens),
			strconv.Itoa(m.CachedTokens), boolStr(m.TokensEstimated), strconv.Itoa(m.ChunkCount))
		if m.ITL.Count > 0 {
			row = append(row, num(m.ITL.Mean), num(m.ITL.P50), num(m.ITL.P99), num(m.ITL.Max))
		} else {
			row = append(row, "", "", "", "")
		}
		if slo != nil {
			met := ""
			if !r.Warmup && r.Status != "canceled" {
				ok, _ := slo.Meets(metrics.SLOSample{OK: r.Status == "ok", TTFTMs: m.TTFTMs, TPOTMs: m.TPOTMs, E2EMs: m.E2EMs, OutputTPS: m.OutputTPS})
				met = boolStr(ok)
			}
			row = append(row, met)
		}
		return cw.Write(row)
	})
	if err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// WriteCellsCSV writes one row per cell with its summary. goodput (by cell
// ID) adds goodput columns when non-nil.
func WriteCellsCSV(w io.Writer, cells []store.RunCell, goodput map[string]metrics.Goodput) error {
	cw := csv.NewWriter(w)
	stats := func(prefix string) []string {
		return []string{prefix + "_min", prefix + "_mean", prefix + "_p50", prefix + "_p90", prefix + "_p95", prefix + "_p99", prefix + "_max"}
	}
	header := append([]string{"cell_id"}, cellDimHeader...)
	header = append(header, "status", "requests", "succeeded", "failed", "canceled", "error_rate", "wall_seconds",
		"request_throughput", "output_throughput", "input_tokens", "output_tokens", "reasoning_tokens", "tokens_estimated")
	for _, p := range []string{"ttft_ms", "ttfat_ms", "e2e_ms", "tpot_ms", "itl_ms", "output_tps", "prefill_tps"} {
		header = append(header, stats(p)...)
	}
	header = append(header, "client_cpu_pct", "schedule_lag_p95_ms")
	if goodput != nil {
		header = append(header, "slo_good_requests", "goodput_ratio", "goodput_per_second")
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, c := range cells {
		row := append([]string{c.ID}, cellDims(c)...)
		row = append(row, c.Status)
		if s := c.Summary; s != nil {
			row = append(row, strconv.Itoa(s.Requests), strconv.Itoa(s.Succeeded), strconv.Itoa(s.Failed), strconv.Itoa(s.Canceled),
				num(s.ErrorRate), num(s.WallSeconds), num(s.RequestThroughput), num(s.OutputThroughput),
				strconv.Itoa(s.InputTokens), strconv.Itoa(s.OutputTokens), strconv.Itoa(s.ReasoningTokens), boolStr(s.TokensEstimated))
			for _, d := range []metrics.Summary{s.TTFT, s.TTFAT, s.E2E, s.TPOT, s.ITL, s.OutputTPS, s.PrefillTPS} {
				if d.Count == 0 {
					row = append(row, "", "", "", "", "", "", "")
					continue
				}
				row = append(row, num(d.Min), num(d.Mean), num(d.P50), num(d.P90), num(d.P95), num(d.P99), num(d.Max))
			}
		} else {
			row = append(row, make([]string, 12+7*7)...)
		}
		row = append(row, ptr(c.ClientCPUPct), ptr(c.ScheduleLagP95Ms))
		if goodput != nil {
			if g, ok := goodput[c.ID]; ok {
				row = append(row, strconv.Itoa(g.Good), num(g.Ratio), num(g.PerSecond))
			} else {
				row = append(row, "", "", "")
			}
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
