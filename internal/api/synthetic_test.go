package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/metalgeekhub/llmbench/internal/chat"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
	"github.com/metalgeekhub/llmbench/internal/tokenizer"
)

func TestSyntheticChatPrompt(t *testing.T) {
	e := newTestEnv(t)
	llm := newRecordingLLM(t)
	var src sources.Source
	e.do(t, "POST", "/api/v1/sources", map[string]any{"name": "rec", "type": "openai", "base_url": llm.URL, "models": []string{"m"}}, &src)
	var sess store.ChatSession
	e.do(t, "POST", "/api/v1/chat/sessions", map[string]any{"source_id": src.ID, "model": "m"}, &sess)

	send := func(body string) {
		t.Helper()
		resp, err := http.Post(e.srv.URL+"/api/v1/chat/sessions/"+sess.ID+"/messages", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		readSSE(t, resp.Body)
		resp.Body.Close()
	}
	send(`{"content":"Summarize the text above.","synthetic_tokens":512}`)
	first := lastUserContent(llm.last())
	send(`{"synthetic_tokens":512}`)
	second := lastUserContent(llm.last())

	if n := tokenizer.Count(first); n < 505 || n > 525 {
		t.Errorf("synthetic prompt has %d tokens, want ~512", n)
	}
	if !strings.HasSuffix(first, "Summarize the text above.") || !strings.HasSuffix(second, tokenizer.DefaultInstruction) {
		t.Errorf("instructions not appended: %q / %q", first[len(first)-40:], second[len(second)-40:])
	}
	if first[:18] == second[:18] {
		t.Error("each synthetic prompt should start with a unique cache-busting marker")
	}

	var d chat.SessionDetail
	e.do(t, "GET", "/api/v1/chat/sessions/"+sess.ID, nil, &d)
	if d.Title != "Synthetic prompt · 512 tokens" || d.Messages[0].SyntheticTokens != 512 || d.Messages[1].SyntheticTokens != 0 {
		t.Errorf("session = %q, messages = %+v", d.Title, d.Messages[0].SyntheticTokens)
	}

	var apiErr struct{ Error string }
	if code := e.do(t, "POST", "/api/v1/chat/sessions/"+sess.ID+"/messages", map[string]any{"synthetic_tokens": 3}, &apiErr); code != http.StatusBadRequest {
		t.Errorf("too-short synthetic prompt status = %d", code)
	}
}

func lastUserContent(body map[string]any) string {
	msgs := body["messages"].([]any)
	return msgs[len(msgs)-1].(map[string]any)["content"].(string)
}
