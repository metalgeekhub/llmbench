package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/metalgeekhub/llmbench/internal/chat"
	"github.com/metalgeekhub/llmbench/internal/config"
	"github.com/metalgeekhub/llmbench/internal/secrets"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
)

// fakeLLM is an OpenAI-compatible server that streams a fixed answer.
func fakeLLM(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"fake-model"}]}`)
		case "/v1/chat/completions":
			if r.Header.Get("Authorization") != "Bearer sk-ui" {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":{"message":"bad key"}}`)
				return
			}
			var body struct {
				Messages []struct{ Role, Content string }
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			last := body.Messages[len(body.Messages)-1].Content
			for _, tok := range []string{"You", " said", ": ", last} {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", tok)
				w.(http.Flusher).Flush()
				time.Sleep(2 * time.Millisecond)
			}
			fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":4}}\n\n", 5*len(body.Messages))
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type testEnv struct {
	srv *httptest.Server
	llm *httptest.Server
	st  *store.SQLite
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	llm := fakeLLM(t)
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.New("k")
	srcs := sources.New(st, box, []config.SourceConfig{{
		Name: "envsrc", Type: "openai", BaseURL: llm.URL + "/v1", ModelsAuto: true,
	}})
	ui := fstest.MapFS{
		"index.html":                  {Data: []byte("<html>app</html>")},
		"_app/immutable/entry/app.js": {Data: []byte("console.log(1)")},
	}
	h := New(Deps{Version: "test", Store: st, Sources: srcs, Chat: chat.NewService(st, srcs), UI: ui})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &testEnv{srv: srv, llm: llm, st: st}
}

func (e *testEnv) do(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: decoding %q: %v", method, path, data, err)
		}
	}
	return resp.StatusCode
}

type sseEvent struct {
	name string
	data string
}

func readSSE(t *testing.T, r io.Reader) []sseEvent {
	t.Helper()
	var events []sseEvent
	var cur sseEvent
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if cur.name != "" {
				events = append(events, cur)
			}
			cur = sseEvent{}
		}
	}
	return events
}

func TestSourcesAPI(t *testing.T) {
	e := newTestEnv(t)

	var list struct{ Sources []sources.Source }
	e.do(t, "GET", "/api/v1/sources", nil, &list)
	if len(list.Sources) != 1 || list.Sources[0].ID != "env-envsrc" || !list.Sources[0].ReadOnly {
		t.Fatalf("sources = %+v", list.Sources)
	}
	if code := e.do(t, "PUT", "/api/v1/sources/env-envsrc", map[string]any{"name": "x"}, nil); code != http.StatusForbidden {
		t.Errorf("update env source status = %d", code)
	}
	if code := e.do(t, "DELETE", "/api/v1/sources/env-envsrc", nil, nil); code != http.StatusForbidden {
		t.Errorf("delete env source status = %d", code)
	}

	var ml sources.ModelList
	e.do(t, "GET", "/api/v1/sources/env-envsrc/models", nil, &ml)
	if !ml.Auto || len(ml.Models) != 1 || ml.Models[0] != "fake-model" {
		t.Errorf("models = %+v", ml)
	}

	// Create with API key; the response must not contain it.
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/sources", strings.NewReader(
		`{"name":"mine","type":"openai","base_url":"`+e.llm.URL+`/v1","api_key":"sk-ui","models":["fake-model"]}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || strings.Contains(string(raw), "sk-ui") {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	var created sources.Source
	_ = json.Unmarshal(raw, &created)
	if !created.HasAPIKey || created.ManagedBy != "ui" {
		t.Errorf("created = %+v", created)
	}

	var discovered struct{ Models []string }
	if code := e.do(t, "POST", "/api/v1/sources/"+created.ID+"/models/discover", nil, &discovered); code != 200 || len(discovered.Models) != 1 {
		t.Errorf("discover: %d %+v", code, discovered)
	}

	var apiErr struct{ Error string }
	if code := e.do(t, "POST", "/api/v1/sources", map[string]any{"name": "bad", "type": "openai", "base_url": "nope"}, &apiErr); code != 400 || apiErr.Error == "" {
		t.Errorf("invalid create: %d %+v", code, apiErr)
	}
	if code := e.do(t, "GET", "/api/v1/sources/missing", nil, nil); code != 404 {
		t.Errorf("missing source status = %d", code)
	}
	if code := e.do(t, "DELETE", "/api/v1/sources/"+created.ID, nil, nil); code != 204 {
		t.Errorf("delete status = %d", code)
	}
}

func TestChatFlow(t *testing.T) {
	e := newTestEnv(t)

	var src sources.Source
	e.do(t, "POST", "/api/v1/sources", map[string]any{
		"name": "mine", "type": "openai", "base_url": e.llm.URL + "/v1", "api_key": "sk-ui", "models": []string{"fake-model"},
	}, &src)

	var sess store.ChatSession
	if code := e.do(t, "POST", "/api/v1/chat/sessions", map[string]any{"source_id": src.ID, "model": "fake-model"}, &sess); code != 201 {
		t.Fatalf("create session status = %d", code)
	}

	send := func(content string) []sseEvent {
		b, _ := json.Marshal(map[string]any{"content": content})
		resp, err := http.Post(e.srv.URL+"/api/v1/chat/sessions/"+sess.ID+"/messages", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Fatalf("content type = %q", ct)
		}
		return readSSE(t, resp.Body)
	}

	events := send("hello")
	if len(events) != 6 || events[0].name != "user_message" || events[5].name != "done" {
		t.Fatalf("events = %+v", events)
	}
	var text strings.Builder
	for _, ev := range events[1:5] {
		var d chat.Delta
		_ = json.Unmarshal([]byte(ev.data), &d)
		if ev.name != "delta" || d.Kind != "content" {
			t.Errorf("unexpected event %+v", ev)
		}
		text.WriteString(d.Text)
	}
	if text.String() != "You said: hello" {
		t.Errorf("streamed text = %q", text.String())
	}
	var done chat.Done
	_ = json.Unmarshal([]byte(events[5].data), &done)
	r := done.Message.Request
	if done.Message.Content != "You said: hello" || r == nil || r.Status != "ok" {
		t.Fatalf("done = %+v", done)
	}
	m := r.Metrics
	if m.TTFTMs == nil || m.TPOTMs == nil || m.OutputTPS == nil || m.OutputTokens != 4 || m.InputTokens != 5 ||
		m.TokensEstimated || m.E2EMs < *m.TTFTMs || m.ChunkCount != 4 {
		t.Errorf("metrics = %+v", m)
	}

	// Second turn includes history (3 messages → prompt_tokens 15).
	events = send("again")
	_ = json.Unmarshal([]byte(events[len(events)-1].data), &done)
	if done.Message.Request.Metrics.InputTokens != 15 {
		t.Errorf("history not sent: input tokens = %d", done.Message.Request.Metrics.InputTokens)
	}

	var detail chat.SessionDetail
	e.do(t, "GET", "/api/v1/chat/sessions/"+sess.ID, nil, &detail)
	if detail.Title != "hello" || len(detail.Messages) != 4 || detail.Messages[1].Request == nil || detail.Messages[0].Request != nil {
		t.Errorf("session detail = %+v", detail)
	}

	var reqs struct {
		Requests []store.Request
		Total    int
	}
	e.do(t, "GET", "/api/v1/requests?model=fake-model", nil, &reqs)
	if reqs.Total != 2 || len(reqs.Requests) != 2 || reqs.Requests[0].SourceName != "mine" {
		t.Errorf("requests = %+v", reqs)
	}
	var one struct {
		Request  store.Request
		Timeline *store.Timeline
	}
	e.do(t, "GET", "/api/v1/requests/"+reqs.Requests[0].ID, nil, &one)
	if one.Timeline == nil || len(one.Timeline.OffsetsMs) != 4 || one.Timeline.Kinds != "cccc" {
		t.Errorf("timeline = %+v", one.Timeline)
	}
	var models struct{ Models []string }
	e.do(t, "GET", "/api/v1/requests/models", nil, &models)
	if len(models.Models) != 1 || models.Models[0] != "fake-model" {
		t.Errorf("request models = %+v", models)
	}
}

func TestChatErrors(t *testing.T) {
	e := newTestEnv(t)

	// Env source has no API key, so the fake server returns 401: the request
	// is still recorded as a failed request.
	var sess store.ChatSession
	e.do(t, "POST", "/api/v1/chat/sessions", map[string]any{"source_id": "env-envsrc", "model": "fake-model"}, &sess)
	resp, err := http.Post(e.srv.URL+"/api/v1/chat/sessions/"+sess.ID+"/messages", "application/json",
		strings.NewReader(`{"content":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	events := readSSE(t, resp.Body)
	resp.Body.Close()
	var done chat.Done
	_ = json.Unmarshal([]byte(events[len(events)-1].data), &done)
	if r := done.Message.Request; r == nil || r.Status != "error" || r.HTTPStatus != 401 || r.ErrorMessage != "bad key" {
		t.Errorf("failed request = %+v", done.Message.Request)
	}

	var apiErr struct{ Error string }
	if code := e.do(t, "POST", "/api/v1/chat/sessions/"+sess.ID+"/messages", map[string]any{"content": "  "}, &apiErr); code != 400 {
		t.Errorf("empty message status = %d", code)
	}
	if code := e.do(t, "POST", "/api/v1/chat/sessions/nope/messages", map[string]any{"content": "x"}, nil); code != 404 {
		t.Errorf("unknown session status = %d", code)
	}
	var noModel store.ChatSession
	e.do(t, "POST", "/api/v1/chat/sessions", map[string]any{}, &noModel)
	if code := e.do(t, "POST", "/api/v1/chat/sessions/"+noModel.ID+"/messages", map[string]any{"content": "x"}, nil); code != 400 {
		t.Errorf("session without model status = %d", code)
	}
	if code := e.do(t, "DELETE", "/api/v1/chat/sessions/"+sess.ID, nil, nil); code != 204 {
		t.Errorf("delete session status = %d", code)
	}
}

func TestSPAFallback(t *testing.T) {
	e := newTestEnv(t)
	get := func(path string) (int, string, http.Header) {
		resp, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	for _, p := range []string{"/", "/chat", "/history/abc", "/index.html"} {
		if code, body, _ := get(p); code != 200 || body != "<html>app</html>" {
			t.Errorf("GET %s = %d %q", p, code, body)
		}
	}
	code, body, h := get("/_app/immutable/entry/app.js")
	if code != 200 || body != "console.log(1)" || !strings.Contains(h.Get("Cache-Control"), "immutable") {
		t.Errorf("asset = %d %q %v", code, body, h)
	}
	if code, body, _ := get("/api/v1/nope"); code != 404 || !strings.Contains(body, "error") {
		t.Errorf("unknown API path = %d %q", code, body)
	}
	if code, _, _ := get("/api/v1/health"); code != 200 {
		t.Errorf("health = %d", code)
	}
}

func TestPlaceholderWithoutUI(t *testing.T) {
	srv := httptest.NewServer(spaHandler(nil))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/anything")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "built without the web UI") {
		t.Errorf("placeholder = %d %q", resp.StatusCode, b)
	}
}
