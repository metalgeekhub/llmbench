package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOpenAIStreamChat(t *testing.T) {
	var gotBody map[string]any
	var gotAuth, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		gotHeader = r.Header.Get("X-Custom")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"choices":[{"delta":{"role":"assistant"}}]}`,
			`{"choices":[{"delta":{"reasoning_content":"Let me think."}}]}`,
			`{"choices":[{"delta":{"content":"Hello"}}]}`,
			`{"choices":[{"delta":{"content":" world"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":3}}}`,
		}
		fmt.Fprint(w, ": keep-alive\n\n")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p, err := New(Config{
		Type:      TypeOpenAI,
		BaseURL:   srv.URL + "/v1/",
		APIKey:    "sk-test",
		Headers:   map[string]string{"X-Custom": "yes"},
		ExtraBody: map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": true, "a": 1}, "top_k": 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	temp := 0.2
	maxTok := 64
	var events []Event
	err = p.StreamChat(context.Background(), ChatRequest{
		Model:       "qwen",
		Messages:    []Message{{Role: "user", Content: "hi"}},
		Temperature: &temp,
		MaxTokens:   &maxTok,
		ExtraBody:   map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}},
	}, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}

	if gotAuth != "Bearer sk-test" || gotHeader != "yes" {
		t.Errorf("auth=%q header=%q", gotAuth, gotHeader)
	}
	if gotBody["model"] != "qwen" || gotBody["stream"] != true || gotBody["temperature"] != 0.2 || gotBody["max_tokens"] != 64.0 {
		t.Errorf("body = %v", gotBody)
	}
	if so, _ := gotBody["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Errorf("stream_options = %v", gotBody["stream_options"])
	}
	wantKw := map[string]any{"enable_thinking": false, "a": 1.0}
	if !reflect.DeepEqual(gotBody["chat_template_kwargs"], wantKw) || gotBody["top_k"] != 5.0 {
		t.Errorf("extra body not deep-merged: %v", gotBody)
	}

	if len(events) != 4 {
		t.Fatalf("got %d events: %+v", len(events), events)
	}
	if events[0].Type != EventReasoning || events[0].Text != "Let me think." {
		t.Errorf("event 0 = %+v", events[0])
	}
	if events[1].Type != EventContent || events[1].Text != "Hello" || events[2].Text != " world" {
		t.Errorf("content events = %+v %+v", events[1], events[2])
	}
	u := events[3].Usage
	if events[3].Type != EventUsage || u == nil || *u != (Usage{InputTokens: 12, OutputTokens: 5, ReasoningTokens: 3, CachedTokens: 4}) {
		t.Errorf("usage event = %+v", events[3])
	}
	for _, e := range events {
		if e.At.IsZero() {
			t.Error("event without timestamp")
		}
	}
}

func TestOpenAIReasoningField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"hmm\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		// No [DONE]: EOF should still complete.
	}))
	defer srv.Close()
	p, _ := New(Config{Type: TypeOpenAI, BaseURL: srv.URL})
	var kinds []EventType
	if err := p.StreamChat(context.Background(), ChatRequest{Model: "m"}, func(e Event) { kinds = append(kinds, e.Type) }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kinds, []EventType{EventReasoning, EventContent}) {
		t.Errorf("kinds = %v", kinds)
	}
}

func TestOpenAIErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		errType string
		status  int
		msg     string
	}{
		{"rate limit", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
		}, ErrRateLimit, 429, "slow down"},
		{"server error", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":"overloaded"}`)
		}, ErrHTTP5xx, 503, "overloaded"},
		{"bad request", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			fmt.Fprint(w, `plain text`)
		}, ErrHTTP4xx, 400, "plain text"},
		{"stream error", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "data: {\"error\":{\"message\":\"model crashed\"}}\n\n")
		}, ErrStream, 0, "model crashed"},
		{"bad chunk", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "data: {not json\n\n")
		}, ErrStream, 0, "invalid stream chunk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			p, _ := New(Config{Type: TypeOpenAI, BaseURL: srv.URL})
			err := p.StreamChat(context.Background(), ChatRequest{Model: "m"}, func(Event) {})
			pe := AsError(err)
			if pe == nil || pe.Type != tt.errType || pe.HTTPStatus != tt.status || !strings.Contains(pe.Message, tt.msg) {
				t.Errorf("err = %#v", pe)
			}
		})
	}
}

func TestOpenAITimeoutAndConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	p, _ := New(Config{Type: TypeOpenAI, BaseURL: srv.URL, Timeout: 50 * time.Millisecond})
	err := p.StreamChat(context.Background(), ChatRequest{Model: "m"}, func(Event) {})
	if pe := AsError(err); pe == nil || pe.Type != ErrTimeout {
		t.Errorf("timeout err = %#v", pe)
	}

	p, _ = New(Config{Type: TypeOpenAI, BaseURL: "http://127.0.0.1:1"})
	err = p.StreamChat(context.Background(), ChatRequest{Model: "m"}, func(Event) {})
	if pe := AsError(err); pe == nil || pe.Type != ErrConnection {
		t.Errorf("connection err = %#v", pe)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p, _ = New(Config{Type: TypeOpenAI, BaseURL: srv.URL})
	err = p.StreamChat(ctx, ChatRequest{Model: "m"}, func(Event) {})
	if pe := AsError(err); pe == nil || pe.Type != ErrCanceled {
		t.Errorf("canceled err = %#v", pe)
	}
}

func TestOpenAIListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"object":"list","data":[{"id":"zeta"},{"id":"alpha"},{"id":""}]}`)
	}))
	defer srv.Close()
	p, _ := New(Config{Type: TypeOpenAI, BaseURL: srv.URL + "/v1"})
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(models, []string{"alpha", "zeta"}) {
		t.Errorf("models = %v", models)
	}
}

func TestThinkingMapping(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	// The source enables thinking by default; the control must override it.
	p, _ := New(Config{Type: TypeOpenAI, BaseURL: srv.URL,
		ExtraBody: map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": true, "keep": 1}}})

	send := func(level, style string) {
		t.Helper()
		th, err := ParseThinking(level, style)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.StreamChat(context.Background(), ChatRequest{Model: "m", Thinking: th}, func(Event) {}); err != nil {
			t.Fatal(err)
		}
	}

	send("off", StyleChatTemplate)
	if kw := body["chat_template_kwargs"].(map[string]any); kw["enable_thinking"] != false || kw["keep"] != 1.0 {
		t.Errorf("off/chat_template: %v", body)
	}
	send("high", StyleChatTemplate)
	if kw := body["chat_template_kwargs"].(map[string]any); kw["enable_thinking"] != true {
		t.Errorf("high/chat_template: %v", body)
	}
	send("medium", StyleReasoningEffort)
	if body["reasoning_effort"] != "medium" {
		t.Errorf("medium/reasoning_effort: %v", body)
	}
	send("off", StyleReasoningEffort)
	if body["reasoning_effort"] != "none" {
		t.Errorf("off/reasoning_effort: %v", body)
	}
	send("", "")
	if _, ok := body["reasoning_effort"]; ok {
		t.Errorf("server default must not send reasoning_effort: %v", body)
	}

	for _, bad := range [][2]string{{"max", StyleChatTemplate}, {"high", ""}, {"low", "magic"}} {
		if _, err := ParseThinking(bad[0], bad[1]); err == nil {
			t.Errorf("ParseThinking(%q, %q) should fail", bad[0], bad[1])
		}
	}
}

func TestNewUnsupported(t *testing.T) {
	if _, err := New(Config{Type: "bedrock"}); err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestMergeJSON(t *testing.T) {
	src := map[string]any{"a": map[string]any{"x": 1}}
	got := MergeJSON(map[string]any{"a": map[string]any{"y": 2}, "b": 3}, src)
	want := map[string]any{"a": map[string]any{"x": 1, "y": 2}, "b": 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	// src maps must not be aliased into dst.
	merged := MergeJSON(nil, src)
	merged["a"].(map[string]any)["z"] = 9
	if _, ok := src["a"].(map[string]any)["z"]; ok {
		t.Error("MergeJSON aliased a nested source map")
	}
}
