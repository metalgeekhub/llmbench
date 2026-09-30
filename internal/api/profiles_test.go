package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/metalgeekhub/llmbench/internal/chat"
	"github.com/metalgeekhub/llmbench/internal/runner"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
)

// recordingLLM answers with the model name and records request bodies.
type recordingLLM struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
}

func newRecordingLLM(t *testing.T) *recordingLLM {
	l := &recordingLLM{}
	l.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		l.mu.Lock()
		l.bodies = append(l.bodies, body)
		l.mu.Unlock()
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", "answer from "+fmt.Sprint(body["model"]))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(l.Close)
	return l
}

func (l *recordingLLM) last() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bodies[len(l.bodies)-1]
}

func TestProfilesAPIAndThinking(t *testing.T) {
	e := newTestEnv(t)
	llm := newRecordingLLM(t)
	var src sources.Source
	e.do(t, "POST", "/api/v1/sources", map[string]any{"name": "rec", "type": "openai", "base_url": llm.URL, "models": []string{"qwen3"}}, &src)

	var p store.Profile
	code := e.do(t, "POST", "/api/v1/profiles", map[string]any{
		"name": "qwen no-thinking", "source_id": src.ID, "model": "qwen3",
		"params": map[string]any{"thinking": "off", "thinking_style": "chat_template_kwargs", "max_tokens": 128},
	}, &p)
	if code != http.StatusCreated || p.Params.Thinking != "off" {
		t.Fatalf("create profile: %d %+v", code, p)
	}
	var list struct{ Profiles []store.Profile }
	e.do(t, "GET", "/api/v1/profiles", nil, &list)
	if len(list.Profiles) != 1 {
		t.Errorf("profiles = %+v", list)
	}
	var apiErr struct{ Error string }
	if code := e.do(t, "POST", "/api/v1/profiles", map[string]any{"name": "bad", "source_id": src.ID, "model": "q",
		"params": map[string]any{"thinking": "high"}}, &apiErr); code != http.StatusBadRequest || !strings.Contains(apiErr.Error, "reasoning_effort") {
		t.Errorf("thinking without style: %d %+v", code, apiErr)
	}

	// Chat with the profile's settings: thinking reaches the server.
	var sess store.ChatSession
	e.do(t, "POST", "/api/v1/chat/sessions", map[string]any{"source_id": src.ID, "model": "qwen3", "params": p.Params, "profile_id": p.ID}, &sess)
	if sess.ProfileID != p.ID {
		t.Errorf("session profile = %q", sess.ProfileID)
	}
	resp, err := http.Post(e.srv.URL+"/api/v1/chat/sessions/"+sess.ID+"/messages", "application/json", strings.NewReader(`{"content":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	events := readSSE(t, resp.Body)
	resp.Body.Close()
	var done chat.Done
	_ = json.Unmarshal([]byte(events[len(events)-1].data), &done)
	body := llm.last()
	kw, _ := body["chat_template_kwargs"].(map[string]any)
	if kw["enable_thinking"] != false || body["max_tokens"] != 128.0 {
		t.Errorf("request body = %v", body)
	}
	if done.Message.Request == nil || done.Message.Request.Params["thinking"] != "off" {
		t.Errorf("recorded params = %+v", done.Message.Request)
	}

	// Invalid thinking in a send is rejected before anything is recorded.
	if code := e.do(t, "POST", "/api/v1/chat/sessions/"+sess.ID+"/messages", map[string]any{"content": "x",
		"params": map[string]any{"thinking": "extreme", "thinking_style": "reasoning_effort"}}, &apiErr); code != http.StatusBadRequest {
		t.Errorf("invalid thinking status = %d", code)
	}

	if code := e.do(t, "DELETE", "/api/v1/profiles/"+p.ID, nil, nil); code != http.StatusNoContent {
		t.Errorf("delete profile status = %d", code)
	}
}

func TestCompareAPI(t *testing.T) {
	e := newTestEnv(t)
	llm := newRecordingLLM(t)
	var src sources.Source
	e.do(t, "POST", "/api/v1/sources", map[string]any{"name": "rec", "type": "openai", "base_url": llm.URL, "models": []string{"a", "b"}}, &src)

	var a, b store.ChatSession
	e.do(t, "POST", "/api/v1/chat/sessions", map[string]any{"source_id": src.ID, "model": "a", "compare_id": "cmp1"}, &a)
	e.do(t, "POST", "/api/v1/chat/sessions", map[string]any{"source_id": src.ID, "model": "b", "compare_id": "cmp1"}, &b)
	for _, id := range []string{a.ID, b.ID} {
		resp, err := http.Post(e.srv.URL+"/api/v1/chat/sessions/"+id+"/messages", "application/json", bytes.NewReader([]byte(`{"content":"same prompt"}`)))
		if err != nil {
			t.Fatal(err)
		}
		readSSE(t, resp.Body)
		resp.Body.Close()
	}

	// Compare columns don't show up among plain chats.
	var plain struct{ Sessions []store.ChatSession }
	e.do(t, "GET", "/api/v1/chat/sessions", nil, &plain)
	if len(plain.Sessions) != 0 {
		t.Errorf("plain chats = %+v", plain.Sessions)
	}
	var groups struct{ Compares []store.CompareGroup }
	e.do(t, "GET", "/api/v1/chat/compares", nil, &groups)
	if len(groups.Compares) != 1 || len(groups.Compares[0].Sessions) != 2 || groups.Compares[0].Title != "same prompt" {
		t.Fatalf("compares = %+v", groups)
	}
	var d chat.CompareDetail
	e.do(t, "GET", "/api/v1/chat/compares/cmp1", nil, &d)
	if len(d.Sessions) != 2 || d.Sessions[0].Model != "a" || len(d.Sessions[1].Messages) != 2 ||
		d.Sessions[1].Messages[1].Content != "answer from b" || d.Sessions[1].Messages[1].Request == nil {
		t.Errorf("compare detail = %+v", d)
	}
	if code := e.do(t, "GET", "/api/v1/chat/compares/nope", nil, nil); code != http.StatusNotFound {
		t.Errorf("unknown compare status = %d", code)
	}
	if code := e.do(t, "DELETE", "/api/v1/chat/compares/cmp1", nil, nil); code != http.StatusNoContent {
		t.Errorf("delete compare status = %d", code)
	}
	if code := e.do(t, "GET", "/api/v1/chat/sessions/"+a.ID, nil, nil); code != http.StatusNotFound {
		t.Errorf("session after compare delete status = %d", code)
	}
}

func TestRunEventsSSE(t *testing.T) {
	e := newTestEnv(t)
	llm := newRecordingLLM(t)
	var src sources.Source
	e.do(t, "POST", "/api/v1/sources", map[string]any{"name": "rec", "type": "openai", "base_url": llm.URL, "models": []string{"m"}}, &src)

	var started runner.RunView
	e.do(t, "POST", "/api/v1/runs", map[string]any{
		"type": "single", "targets": []map[string]string{{"source_id": src.ID, "model": "m"}},
		"prompt": map[string]any{"mode": "fixed", "text": "hi"}, "concurrency": []int{2}, "duration_seconds": 0.3,
	}, &started)

	resp, err := http.Get(e.srv.URL + "/api/v1/runs/" + started.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	var updates int
	var last runner.LiveUpdate
	var lastEvent string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		line := sc.Text()
		if ev, ok := strings.CutPrefix(line, "event: "); ok {
			lastEvent = ev
			if ev == "update" {
				updates++
			}
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			last = runner.LiveUpdate{}
			if err := json.Unmarshal([]byte(data), &last); err != nil {
				t.Fatal(err)
			}
		}
	}
	if updates < 2 || lastEvent != "done" || !last.Done || last.Run.Status != store.StatusCompleted {
		t.Errorf("updates=%d last event=%s last=%+v", updates, lastEvent, last.Run.TestRun)
	}

	// Once finished, the timeline is part of the run.
	var got runner.RunView
	e.do(t, "GET", "/api/v1/runs/"+started.ID, nil, &got)
	var points []runner.LivePoint
	if err := json.Unmarshal(got.Timeline, &points); err != nil || len(points) < 3 {
		t.Errorf("timeline = %d points, %v", len(points), err)
	}
}
