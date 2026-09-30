package api

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/metalgeekhub/llmbench/internal/runner"
	"github.com/metalgeekhub/llmbench/internal/store"
)

func (s *server) listRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.Runner.List(r.Context(), min(max(limit, 0), 500))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *server) startRun(w http.ResponseWriter, r *http.Request) {
	var cfg runner.Config
	if !decode(w, r, &cfg) {
		return
	}
	run, err := s.Runner.Start(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *server) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.Runner.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// runEvents streams live dashboard updates as Server-Sent Events: an
// "update" event per sampling interval with the run state and new live
// points, then a final "done" event.
func (s *server) runEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	u, err := s.Runner.Live(r.Context(), id, 0)
	if err != nil {
		writeErr(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	tick := time.NewTicker(s.Runner.LiveInterval)
	defer tick.Stop()
	for {
		event := "update"
		if u.Done {
			event = "done"
		}
		if err := writeSSE(w, event, u); err != nil {
			return
		}
		flusher.Flush()
		if u.Done {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
		if u, err = s.Runner.Live(r.Context(), id, u.Next); err != nil {
			return
		}
	}
}

func (s *server) stopRun(w http.ResponseWriter, r *http.Request) {
	if err := s.Runner.Stop(chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) deleteRun(w http.ResponseWriter, r *http.Request) {
	if err := s.Runner.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// maxDistributionValues caps values per cell; larger cells are evenly sampled.
const maxDistributionValues = 5000

// runDistribution returns per-request values of one metric for each cell,
// for histograms: {"metric": "...", "cells": {"<cell id>": [values...]}}.
func (s *server) runDistribution(w http.ResponseWriter, r *http.Request) {
	metric := r.URL.Query().Get("metric")
	if metric == "" {
		metric = "ttft_ms"
	}
	if !slices.Contains(store.DistributionMetrics, metric) {
		writeError(w, http.StatusBadRequest, "metric must be one of "+strings.Join(store.DistributionMetrics, ", "))
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := s.Store.GetRun(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	values, err := s.Store.RunMetricValues(r.Context(), id, metric)
	if err != nil {
		writeErr(w, err)
		return
	}
	for cell, v := range values {
		if len(v) > maxDistributionValues {
			sampled := make([]float64, 0, maxDistributionValues)
			step := float64(len(v)) / maxDistributionValues
			for i := range maxDistributionValues {
				sampled = append(sampled, v[int(float64(i)*step)])
			}
			values[cell] = sampled
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"metric": metric, "cells": values})
}
