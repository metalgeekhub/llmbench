package api

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/metalgeekhub/llmbench/internal/definitions"
	"github.com/metalgeekhub/llmbench/internal/export"
	"github.com/metalgeekhub/llmbench/internal/metrics"
	"github.com/metalgeekhub/llmbench/internal/runner"
)

const (
	maxDefinitionBytes = 1 << 20
	maxImportBytes     = 1 << 30
)

var unsafeFilename = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// attachment marks a response as a download named after name.
func attachment(w http.ResponseWriter, name, ext, contentType string) {
	base := strings.Trim(unsafeFilename.ReplaceAllString(name, "-"), "-")
	if base == "" {
		base = "llmbench"
	}
	if len(base) > 80 {
		base = base[:80]
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, base, ext))
}

// --- test definitions ---

func (s *server) listDefinitions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Definitions.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"definitions": list})
}

func (s *server) createDefinition(w http.ResponseWriter, r *http.Request) {
	var in definitions.Input
	if !decode(w, r, &in) {
		return
	}
	d, err := s.Definitions.Create(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *server) getDefinition(w http.ResponseWriter, r *http.Request) {
	d, err := s.Definitions.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) updateDefinition(w http.ResponseWriter, r *http.Request) {
	var in definitions.Input
	if !decode(w, r, &in) {
		return
	}
	d, err := s.Definitions.Update(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) deleteDefinition(w http.ResponseWriter, r *http.Request) {
	if err := s.Definitions.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) definitionVersions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Definitions.Versions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": list})
}

func (s *server) runDefinition(w http.ResponseWriter, r *http.Request) {
	run, err := s.Definitions.Run(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *server) exportDefinition(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "yaml"
	}
	data, err := s.Definitions.Export(r.Context(), id, format)
	if err != nil {
		writeErr(w, err)
		return
	}
	d, _ := s.Definitions.Get(r.Context(), id)
	contentType := "application/yaml"
	if format == "json" {
		contentType = "application/json"
	}
	attachment(w, d.Name, format, contentType)
	_, _ = w.Write(data)
}

func (s *server) importDefinition(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDefinitionBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "definition file too large or unreadable")
		return
	}
	d, err := s.Definitions.Import(r.Context(), data)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

// --- export & import of runs ---

// exportRuns streams a JSON bundle of the runs in ?runs=id1,id2.
func (s *server) exportRuns(w http.ResponseWriter, r *http.Request) {
	ids := strings.FieldsFunc(r.URL.Query().Get("runs"), func(c rune) bool { return c == ',' })
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "pass the runs to export as ?runs=id1,id2")
		return
	}
	name := "llmbench-runs"
	for _, id := range ids {
		run, err := s.Store.GetRun(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		if len(ids) == 1 {
			name = run.Name
		}
	}
	if s.Runner.IsActive(ids...) {
		writeError(w, http.StatusConflict, "a run is still in progress; export it when it has finished")
		return
	}
	attachment(w, name, "llmbench.json", "application/json")
	if err := export.WriteBundle(r.Context(), w, s.Store, ids, s.Version); err != nil {
		// Headers are sent; all we can do is log and cut the stream.
		logError("export failed", err)
	}
}

// exportRunCSV writes ?table=requests (default) or ?table=cells as CSV.
func (s *server) exportRunCSV(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	cells, err := s.Store.ListCells(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	slo := runner.RunSLO(run)
	switch table := r.URL.Query().Get("table"); table {
	case "", "requests":
		attachment(w, run.Name+"-requests", "csv", "text/csv; charset=utf-8")
		if err := export.WriteRequestsCSV(r.Context(), w, s.Store, id, cells, slo); err != nil {
			logError("csv export failed", err)
		}
	case "cells":
		var goodput map[string]metrics.Goodput
		if slo != nil {
			if goodput, err = runner.Goodput(r.Context(), s.Store, id, cells, *slo); err != nil {
				writeErr(w, err)
				return
			}
		}
		attachment(w, run.Name+"-steps", "csv", "text/csv; charset=utf-8")
		if err := export.WriteCellsCSV(w, cells, goodput); err != nil {
			logError("csv export failed", err)
		}
	default:
		writeError(w, http.StatusBadRequest, "table must be requests or cells")
	}
}

// importRuns stores the runs of an uploaded JSON bundle.
func (s *server) importRuns(w http.ResponseWriter, r *http.Request) {
	b, err := export.ReadBundle(http.MaxBytesReader(w, r.Body, maxImportBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	results, err := export.Import(r.Context(), s.Store, b)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// --- goodput / SLOs ---

// runGoodput evaluates SLO targets against a run's stored requests. Targets
// come from the query (?ttft_ms=&tpot_ms=&e2e_ms=&min_output_tps=), falling
// back to the SLO saved with the run.
func (s *server) runGoodput(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	slo, err := sloFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if slo.IsZero() {
		if saved := runner.RunSLO(run); saved != nil {
			slo = *saved
		}
	}
	if slo.IsZero() {
		writeError(w, http.StatusBadRequest, metrics.ErrNoSLO.Error())
		return
	}
	cells, err := s.Store.ListCells(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	goodput, err := runner.Goodput(r.Context(), s.Store, id, cells, slo)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slo": slo, "cells": goodput})
}

func sloFromQuery(r *http.Request) (metrics.SLO, error) {
	var slo metrics.SLO
	q := r.URL.Query()
	for key, dst := range map[string]**float64{"ttft_ms": &slo.TTFTMs, "tpot_ms": &slo.TPOTMs, "e2e_ms": &slo.E2EMs, "min_output_tps": &slo.MinOutputTPS} {
		v := q.Get(key)
		if v == "" {
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return slo, fmt.Errorf("%s must be a number", key)
		}
		*dst = &f
	}
	return slo, slo.Validate()
}

// updateRun changes a run's saved SLO targets: {"slo": {...}} or {"slo": null}.
func (s *server) updateRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SLO *metrics.SLO `json:"slo"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.SLO != nil {
		if err := in.SLO.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	id := chi.URLParam(r, "id")
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := runner.SetRunSLO(r.Context(), s.Store, run, in.SLO); err != nil {
		writeErr(w, err)
		return
	}
	v, err := s.Runner.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
