package profiles

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/metalgeekhub/llmbench/internal/config"
	"github.com/metalgeekhub/llmbench/internal/secrets"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
)

func newService(t *testing.T) *Service {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.New("k")
	return NewService(st, sources.New(st, box, []config.SourceConfig{{Name: "vllm", Type: "openai", BaseURL: "http://x", Models: []string{"qwen3"}}}))
}

func TestProfiles(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	p, err := s.Create(ctx, Input{Name: " Qwen thinking-high ", SourceID: "env-vllm", Model: "qwen3",
		Params: store.ChatParams{Thinking: "high", ThinkingStyle: "chat_template_kwargs"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Qwen thinking-high" || p.ID == "" {
		t.Errorf("created = %+v", p)
	}
	if _, err := s.Create(ctx, Input{Name: "Qwen thinking-high", SourceID: "env-vllm", Model: "qwen3"}); !isValidation(err) {
		t.Errorf("duplicate name err = %v", err)
	}
	upd, err := s.Update(ctx, p.ID, Input{Name: "Qwen no-thinking", SourceID: "env-vllm", Model: "qwen3",
		Params: store.ChatParams{Thinking: "off", ThinkingStyle: "chat_template_kwargs"}})
	if err != nil || upd.Params.Thinking != "off" {
		t.Errorf("updated = %+v, %v", upd, err)
	}
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, p.ID, Input{Name: "x", SourceID: "env-vllm", Model: "qwen3"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update deleted err = %v", err)
	}
}

func TestProfileValidation(t *testing.T) {
	s := newService(t)
	temp := 3.0
	zero := 0
	cases := map[string]Input{
		"no name":        {SourceID: "env-vllm", Model: "m"},
		"no model":       {Name: "a", SourceID: "env-vllm"},
		"unknown source": {Name: "a", SourceID: "nope", Model: "m"},
		"bad level":      {Name: "a", SourceID: "env-vllm", Model: "m", Params: store.ChatParams{Thinking: "max", ThinkingStyle: "reasoning_effort"}},
		"no style":       {Name: "a", SourceID: "env-vllm", Model: "m", Params: store.ChatParams{Thinking: "high"}},
		"temperature":    {Name: "a", SourceID: "env-vllm", Model: "m", Params: store.ChatParams{Temperature: &temp}},
		"max tokens":     {Name: "a", SourceID: "env-vllm", Model: "m", Params: store.ChatParams{MaxTokens: &zero}},
	}
	for name, in := range cases {
		if _, err := s.Create(context.Background(), in); !isValidation(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func isValidation(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}
