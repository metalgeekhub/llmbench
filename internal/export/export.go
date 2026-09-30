// Package export writes benchmark runs as full-fidelity JSON bundles and
// CSV files, and imports bundles produced by another LLMBench instance.
package export

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/metalgeekhub/llmbench/internal/store"
)

// Bundle format identifiers. Bump Version on incompatible changes.
const (
	Format  = "llmbench"
	Version = 1
)

// Bundle is the JSON export of one or more runs.
type Bundle struct {
	Format     string      `json:"format"`
	Version    int         `json:"version"`
	ExportedAt time.Time   `json:"exported_at"`
	AppVersion string      `json:"app_version"`
	Runs       []RunExport `json:"runs"`
}

// RunExport is a complete run: config, cells with summaries, and every
// request (warm-ups included) with its chunk timeline.
type RunExport struct {
	Run      store.TestRun   `json:"run"`
	Cells    []store.RunCell `json:"cells"`
	Requests []RequestExport `json:"requests"`
}

// RequestExport is a request with its optional timeline.
type RequestExport struct {
	store.Request
	Timeline *store.Timeline `json:"timeline,omitempty"`
}

// WriteBundle streams runs to w as a Bundle without loading all requests
// into memory.
func WriteBundle(ctx context.Context, w io.Writer, st store.Store, runIDs []string, appVersion string) error {
	header, err := json.Marshal(struct {
		Format     string    `json:"format"`
		Version    int       `json:"version"`
		ExportedAt time.Time `json:"exported_at"`
		AppVersion string    `json:"app_version"`
	}{Format, Version, time.Now().UTC(), appVersion})
	if err != nil {
		return err
	}
	// Reopen the header object to append the runs array.
	if _, err := fmt.Fprintf(w, "%s,\"runs\":[", header[:len(header)-1]); err != nil {
		return err
	}
	for i, id := range runIDs {
		if i > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if err := writeRun(ctx, w, st, id); err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "]}\n")
	return err
}

func writeRun(ctx context.Context, w io.Writer, st store.Store, id string) error {
	run, err := st.GetRun(ctx, id)
	if err != nil {
		return err
	}
	cells, err := st.ListCells(ctx, id)
	if err != nil {
		return err
	}
	runJSON, err := json.Marshal(run)
	if err != nil {
		return err
	}
	cellsJSON, err := json.Marshal(cells)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, `{"run":%s,"cells":%s,"requests":[`, runJSON, cellsJSON); err != nil {
		return err
	}
	first := true
	enc := json.NewEncoder(w)
	err = st.EachRunRequest(ctx, id, func(r store.Request, tl *store.Timeline) error {
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		first = false
		return enc.Encode(RequestExport{Request: r, Timeline: tl})
	})
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "]}")
	return err
}

// ErrNotBundle is returned when a file isn't an LLMBench export.
var ErrNotBundle = errors.New("not an LLMBench export file")

// ReadBundle decodes and checks a bundle.
func ReadBundle(r io.Reader) (*Bundle, error) {
	var b Bundle
	if err := json.NewDecoder(r).Decode(&b); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotBundle, err)
	}
	if b.Format != Format {
		return nil, ErrNotBundle
	}
	if b.Version > Version {
		return nil, fmt.Errorf("export format version %d is newer than this LLMBench supports (%d); upgrade LLMBench", b.Version, Version)
	}
	return &b, nil
}

// ImportResult reports what happened to one run of a bundle.
type ImportResult struct {
	RunID  string `json:"run_id"`
	Name   string `json:"name"`
	Status string `json:"status"` // "imported" or "skipped"
	Reason string `json:"reason,omitempty"`
}

// Import stores every run of b. Runs that already exist are skipped, so
// importing the same file twice is harmless.
func Import(ctx context.Context, st store.Store, b *Bundle) ([]ImportResult, error) {
	out := make([]ImportResult, 0, len(b.Runs))
	now := time.Now().UTC()
	for _, re := range b.Runs {
		run := re.Run
		res := ImportResult{RunID: run.ID, Name: run.Name}
		if run.ID == "" {
			res.Status, res.Reason = "skipped", "run has no ID"
			out = append(out, res)
			continue
		}
		// A run exported mid-flight can't continue here.
		if run.Status == store.StatusRunning || run.Status == store.StatusPending {
			run.Status = store.StatusInterrupted
		}
		run.ImportedAt = &now
		records := make([]store.RequestRecord, len(re.Requests))
		for i, rq := range re.Requests {
			records[i] = store.RequestRecord{Request: rq.Request, Timeline: rq.Timeline}
		}
		switch err := st.ImportRun(ctx, &run, re.Cells, records); {
		case err == nil:
			res.Status = "imported"
		case errors.Is(err, store.ErrConflict):
			res.Status, res.Reason = "skipped", "already present"
		default:
			return out, fmt.Errorf("importing run %q: %w", run.Name, err)
		}
		out = append(out, res)
	}
	return out, nil
}
