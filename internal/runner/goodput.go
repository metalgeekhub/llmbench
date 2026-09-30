package runner

import (
	"context"
	"encoding/json"

	"github.com/metalgeekhub/llmbench/internal/metrics"
	"github.com/metalgeekhub/llmbench/internal/store"
)

// RunSLO returns the SLO stored in a run's config, or nil.
func RunSLO(run store.TestRun) *metrics.SLO {
	var cfg struct {
		SLO *metrics.SLO `json:"slo"`
	}
	if json.Unmarshal(run.Config, &cfg) != nil || cfg.SLO == nil || cfg.SLO.IsZero() {
		return nil
	}
	return cfg.SLO
}

// Goodput evaluates slo against the stored requests of every cell of a run.
// Goodput per second uses each cell's measured wall time.
func Goodput(ctx context.Context, st store.Store, runID string, cells []store.RunCell, slo metrics.SLO) (map[string]metrics.Goodput, error) {
	samples, err := st.RunSLOSamples(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]metrics.Goodput, len(cells))
	for _, c := range cells {
		wall := 0.0
		if c.Summary != nil {
			wall = c.Summary.WallSeconds
		}
		out[c.ID] = metrics.EvaluateGoodput(slo, samples[c.ID], wall)
	}
	return out, nil
}

// SetRunSLO stores new SLO targets in a run's config (nil clears them).
func SetRunSLO(ctx context.Context, st store.Store, run store.TestRun, slo *metrics.SLO) error {
	var cfg map[string]any
	if err := json.Unmarshal(run.Config, &cfg); err != nil || cfg == nil {
		cfg = map[string]any{}
	}
	if slo == nil || slo.IsZero() {
		delete(cfg, "slo")
	} else {
		cfg["slo"] = slo
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return st.UpdateRunConfig(ctx, run.ID, b)
}
