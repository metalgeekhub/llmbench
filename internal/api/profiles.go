package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/metalgeekhub/llmbench/internal/profiles"
)

func (s *server) listProfiles(w http.ResponseWriter, r *http.Request) {
	list, err := s.Profiles.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": list})
}

func (s *server) createProfile(w http.ResponseWriter, r *http.Request) {
	var in profiles.Input
	if !decode(w, r, &in) {
		return
	}
	p, err := s.Profiles.Create(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *server) getProfile(w http.ResponseWriter, r *http.Request) {
	p, err := s.Profiles.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *server) updateProfile(w http.ResponseWriter, r *http.Request) {
	var in profiles.Input
	if !decode(w, r, &in) {
		return
	}
	p, err := s.Profiles.Update(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	if err := s.Profiles.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- comparisons ---

func (s *server) listCompares(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListCompares(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"compares": list})
}

func (s *server) getCompare(w http.ResponseWriter, r *http.Request) {
	d, err := s.Chat.GetCompare(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) deleteCompare(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteCompare(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
