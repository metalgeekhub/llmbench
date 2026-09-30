// Package api exposes the REST + SSE API under /api/v1 and serves the
// embedded single-page app for every other path.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/metalgeekhub/llmbench/internal/chat"
	"github.com/metalgeekhub/llmbench/internal/definitions"
	"github.com/metalgeekhub/llmbench/internal/profiles"
	"github.com/metalgeekhub/llmbench/internal/runner"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
)

// Deps are the services the API is built on.
type Deps struct {
	Version     string
	Store       store.Store
	Sources     *sources.Manager
	Chat        *chat.Service
	Runner      *runner.Runner
	Profiles    *profiles.Service
	Definitions *definitions.Service
	// UI is the frontend file system; nil or without index.html serves a placeholder.
	UI fs.FS
}

type server struct {
	Deps
}

// New returns the root HTTP handler.
func New(d Deps) http.Handler {
	s := &server{Deps: d}
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(logRequests)
		r.Get("/health", s.health)

		r.Get("/sources", s.listSources)
		r.Post("/sources", s.createSource)
		r.Get("/sources/{id}", s.getSource)
		r.Put("/sources/{id}", s.updateSource)
		r.Delete("/sources/{id}", s.deleteSource)
		r.Get("/sources/{id}/models", s.sourceModels)
		r.Post("/sources/{id}/models/discover", s.discoverModels)

		r.Get("/profiles", s.listProfiles)
		r.Post("/profiles", s.createProfile)
		r.Get("/profiles/{id}", s.getProfile)
		r.Put("/profiles/{id}", s.updateProfile)
		r.Delete("/profiles/{id}", s.deleteProfile)

		r.Get("/chat/compares", s.listCompares)
		r.Get("/chat/compares/{id}", s.getCompare)
		r.Delete("/chat/compares/{id}", s.deleteCompare)
		r.Get("/chat/sessions", s.listSessions)
		r.Post("/chat/sessions", s.createSession)
		r.Get("/chat/sessions/{id}", s.getSession)
		r.Patch("/chat/sessions/{id}", s.updateSession)
		r.Delete("/chat/sessions/{id}", s.deleteSession)
		r.Post("/chat/sessions/{id}/messages", s.sendMessage)

		r.Get("/requests", s.listRequests)
		r.Get("/requests/models", s.listRequestModels)
		r.Get("/requests/{id}", s.getRequest)

		r.Get("/runs", s.listRuns)
		r.Post("/runs", s.startRun)
		r.Get("/runs/{id}", s.getRun)
		r.Get("/comparisons", s.listComparisons)
		r.Post("/comparisons", s.createComparison)
		r.Get("/comparisons/{id}", s.getComparison)
		r.Put("/comparisons/{id}", s.updateComparison)
		r.Delete("/comparisons/{id}", s.deleteComparison)

		r.Get("/runs/{id}/events", s.runEvents)
		r.Get("/runs/{id}/distribution", s.runDistribution)
		r.Get("/runs/{id}/goodput", s.runGoodput)
		r.Patch("/runs/{id}", s.updateRun)
		r.Get("/runs/{id}/export.csv", s.exportRunCSV)
		r.Get("/export", s.exportRuns)
		r.Post("/import", s.importRuns)

		r.Get("/definitions", s.listDefinitions)
		r.Post("/definitions", s.createDefinition)
		r.Post("/definitions/import", s.importDefinition)
		r.Get("/definitions/{id}", s.getDefinition)
		r.Put("/definitions/{id}", s.updateDefinition)
		r.Delete("/definitions/{id}", s.deleteDefinition)
		r.Get("/definitions/{id}/versions", s.definitionVersions)
		r.Post("/definitions/{id}/run", s.runDefinition)
		r.Get("/definitions/{id}/export", s.exportDefinition)
		r.Post("/runs/{id}/stop", s.stopRun)
		r.Delete("/runs/{id}", s.deleteRun)

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "not found")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		})
	})

	r.Handle("/*", spaHandler(d.UI))
	return r
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.Version})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("writing response", "err", err)
	}
}

func logError(msg string, err error) { slog.Error(msg, "err", err) }

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeErr maps service errors to HTTP statuses.
func writeErr(w http.ResponseWriter, err error) {
	var ve *sources.ValidationError
	var rve *runner.ValidationError
	var pve *profiles.ValidationError
	var dve *definitions.ValidationError
	switch {
	case errors.As(err, &ve), errors.As(err, &rve), errors.As(err, &pve), errors.As(err, &dve), errors.Is(err, chat.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNotFound), errors.Is(err, sources.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, sources.ErrReadOnly):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, runner.ErrBusy), errors.Is(err, runner.ErrActive), errors.Is(err, runner.ErrNotActive):
		writeError(w, http.StatusConflict, err.Error())
	default:
		slog.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

const maxBodyBytes = 4 << 20

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		slog.Debug("api", "method", r.Method, "path", r.URL.Path, "status", ww.Status(),
			"duration", time.Since(start).Round(time.Microsecond))
	})
}
