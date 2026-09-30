package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/metalgeekhub/llmbench/internal/store"
)

const maxCompareRuns = 8

type comparisonInput struct {
	Name     string          `json:"name"`
	RunIDs   []string        `json:"run_ids"`
	Settings json.RawMessage `json:"settings"`
}

// validate checks the input and that every run exists.
func (s *server) validateComparison(r *http.Request, in *comparisonInput) (string, bool) {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		return "name is required", false
	case len(in.Name) > 100:
		return "name must be at most 100 characters", false
	case len(in.RunIDs) < 2 || len(in.RunIDs) > maxCompareRuns:
		return "select between 2 and 8 runs", false
	}
	seen := map[string]bool{}
	for _, id := range in.RunIDs {
		if seen[id] {
			return "a run is listed twice", false
		}
		seen[id] = true
		if _, err := s.Store.GetRun(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return "unknown run " + id, false
			}
			return err.Error(), false
		}
	}
	if len(in.Settings) > 0 && !json.Valid(in.Settings) {
		return "settings must be JSON", false
	}
	return "", true
}

func (s *server) listComparisons(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListComparisons(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comparisons": list})
}

func (s *server) getComparison(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.GetComparison(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *server) createComparison(w http.ResponseWriter, r *http.Request) {
	var in comparisonInput
	if !decode(w, r, &in) {
		return
	}
	if msg, ok := s.validateComparison(r, &in); !ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	c := store.Comparison{ID: uuid.NewString(), Name: in.Name, RunIDs: in.RunIDs, Settings: in.Settings}
	if err := s.Store.CreateComparison(r.Context(), &c); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *server) updateComparison(w http.ResponseWriter, r *http.Request) {
	var in comparisonInput
	if !decode(w, r, &in) {
		return
	}
	c, err := s.Store.GetComparison(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if msg, ok := s.validateComparison(r, &in); !ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	c.Name, c.RunIDs, c.Settings = in.Name, in.RunIDs, in.Settings
	if err := s.Store.UpdateComparison(r.Context(), &c); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *server) deleteComparison(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteComparison(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
