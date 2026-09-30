package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// openAI is the adapter for OpenAI-compatible servers (OpenAI, vLLM, SGLang,
// TGI, Ollama, LM Studio, llama.cpp, LiteLLM, OpenRouter, ...).
type openAI struct {
	cfg    Config
	client *http.Client
}

func newOpenAI(cfg Config) *openAI {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	return &openAI{cfg: cfg, client: newHTTPClient(cfg)}
}

func (o *openAI) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, o.cfg.BaseURL+path, r)
	if err != nil {
		return nil, &Error{Type: ErrRequest, Message: err.Error()}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if o.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)
	}
	for k, v := range o.cfg.Headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

func (o *openAI) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, min(o.cfg.Timeout, 30*time.Second))
	defer cancel()

	req, err := o.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, AsError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, httpStatusError(resp.StatusCode, readErrorBody(resp.Body))
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, &Error{Type: ErrStream, Message: "invalid /models response: " + err.Error()}
	}
	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}

// buildBody assembles the chat completion request body. Precedence (lowest
// to highest): source extra body, request extra body, core fields.
func (o *openAI) buildBody(req ChatRequest) ([]byte, error) {
	body := MergeJSON(nil, o.cfg.ExtraBody)
	body = MergeJSON(body, req.ExtraBody)
	body["model"] = req.Model
	body["messages"] = req.Messages
	body["stream"] = true
	so, _ := body["stream_options"].(map[string]any)
	body["stream_options"] = MergeJSON(so, map[string]any{"include_usage": true})
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		body["max_tokens"] = *req.MaxTokens
	}
	return json.Marshal(body)
}

type openAIChunk struct {
	Choices []struct {
		Delta struct {
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			Reasoning        *string `json:"reasoning"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func (o *openAI) StreamChat(ctx context.Context, req ChatRequest, emit func(Event)) error {
	ctx, cancel := context.WithTimeout(ctx, o.cfg.Timeout)
	defer cancel()

	body, err := o.buildBody(req)
	if err != nil {
		return &Error{Type: ErrRequest, Message: err.Error()}
	}
	httpReq, err := o.newRequest(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return AsError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return httpStatusError(resp.StatusCode, readErrorBody(resp.Body))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		now := time.Now()
		line := sc.Bytes()
		data, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue // comments, event names, blank separators
		}
		data = bytes.TrimSpace(data)
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(data, []byte("[DONE]")) {
			return nil
		}
		var chunk openAIChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return &Error{Type: ErrStream, Message: fmt.Sprintf("invalid stream chunk: %v", err)}
		}
		if len(chunk.Error) > 0 && !bytes.Equal(chunk.Error, []byte("null")) {
			return &Error{Type: ErrStream, Message: errorMessage(chunk.Error)}
		}
		for _, c := range chunk.Choices {
			reasoning := c.Delta.ReasoningContent
			if reasoning == nil {
				reasoning = c.Delta.Reasoning
			}
			if reasoning != nil && *reasoning != "" {
				emit(Event{Type: EventReasoning, Text: *reasoning, At: now})
			}
			if c.Delta.Content != nil && *c.Delta.Content != "" {
				emit(Event{Type: EventContent, Text: *c.Delta.Content, At: now})
			}
		}
		if u := chunk.Usage; u != nil {
			usage := &Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens}
			if u.PromptTokensDetails != nil {
				usage.CachedTokens = u.PromptTokensDetails.CachedTokens
			}
			if u.CompletionTokensDetails != nil {
				usage.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
			}
			emit(Event{Type: EventUsage, Usage: usage, At: now})
		}
	}
	if err := sc.Err(); err != nil {
		return AsError(err)
	}
	// Some servers close the stream without [DONE]; treat EOF as completion.
	return nil
}

// readErrorBody extracts a human-readable message from an error response.
func readErrorBody(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 4096))
	var e struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(b, &e) == nil {
		if len(e.Error) > 0 {
			return errorMessage(e.Error)
		}
		if e.Message != "" {
			return e.Message
		}
	}
	return strings.TrimSpace(string(b))
}

// errorMessage handles both {"error":"msg"} and {"error":{"message":"msg"}}.
func errorMessage(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	return string(raw)
}
