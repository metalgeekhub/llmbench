package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/metalgeekhub/llmbench/internal/chat"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
)

// --- sources ---

func (s *server) listSources(w http.ResponseWriter, r *http.Request) {
	list, err := s.Sources.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": list})
}

func (s *server) getSource(w http.ResponseWriter, r *http.Request) {
	src, err := s.Sources.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, src)
}

func (s *server) createSource(w http.ResponseWriter, r *http.Request) {
	var in sources.Input
	if !decode(w, r, &in) {
		return
	}
	src, err := s.Sources.Create(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, src)
}

func (s *server) updateSource(w http.ResponseWriter, r *http.Request) {
	var in sources.Input
	if !decode(w, r, &in) {
		return
	}
	src, err := s.Sources.Update(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, src)
}

func (s *server) deleteSource(w http.ResponseWriter, r *http.Request) {
	if err := s.Sources.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) sourceModels(w http.ResponseWriter, r *http.Request) {
	refresh, _ := strconv.ParseBool(r.URL.Query().Get("refresh"))
	ml, err := s.Sources.Models(r.Context(), chi.URLParam(r, "id"), refresh)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ml)
}

func (s *server) discoverModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.Sources.Discover(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, sources.ErrNotFound) {
			writeErr(w, err)
			return
		}
		writeError(w, http.StatusBadGateway, "fetching /models failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// --- chat ---

func (s *server) listSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListSessions(r.Context(), r.URL.Query().Get("compare_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": list})
}

func (s *server) createSession(w http.ResponseWriter, r *http.Request) {
	var in chat.SessionInput
	if !decode(w, r, &in) {
		return
	}
	sess, err := s.Chat.CreateSession(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *server) getSession(w http.ResponseWriter, r *http.Request) {
	d, err := s.Chat.GetSession(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) updateSession(w http.ResponseWriter, r *http.Request) {
	var in chat.SessionInput
	if !decode(w, r, &in) {
		return
	}
	sess, err := s.Chat.UpdateSession(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *server) deleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteSession(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sendMessage streams the assistant response as Server-Sent Events. The SSE
// response starts with the first event, so failures before that (validation,
// unknown session) are returned as normal JSON errors.
func (s *server) sendMessage(w http.ResponseWriter, r *http.Request) {
	var in chat.SendInput
	if !decode(w, r, &in) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	started := false
	emit := func(event string, data any) {
		if !started {
			h := w.Header()
			h.Set("Content-Type", "text/event-stream")
			h.Set("Cache-Control", "no-cache")
			h.Set("Connection", "keep-alive")
			h.Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
			started = true
		}
		if err := writeSSE(w, event, data); err != nil {
			return // client gone; the request context cancels the upstream call
		}
		flusher.Flush()
	}

	err := s.Chat.Send(r.Context(), chi.URLParam(r, "id"), in, emit)
	if err == nil {
		return
	}
	if !started {
		writeErr(w, err)
		return
	}
	slog.Error("chat stream failed", "err", err)
	emit("error", map[string]string{"error": err.Error()})
}

func writeSSE(w http.ResponseWriter, event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	return err
}

// --- requests ---

func (s *server) listRequests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	f := store.RequestFilter{
		Kind:      q.Get("kind"),
		SourceID:  q.Get("source_id"),
		Model:     q.Get("model"),
		Status:    q.Get("status"),
		SessionID: q.Get("session_id"),
		RunID:     q.Get("run_id"),
		CellID:    q.Get("cell_id"),
		Limit:     min(max(limit, 0), 500),
		Offset:    offset,
	}
	list, total, err := s.Store.ListRequests(r.Context(), f)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": list, "total": total})
}

func (s *server) listRequestModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.Store.ListRequestModels(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *server) getRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	req, err := s.Store.GetRequest(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	tl, err := s.Store.GetTimeline(r.Context(), id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": req, "timeline": tl})
}
