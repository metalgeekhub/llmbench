package sources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/metalgeekhub/llmbench/internal/config"
	"github.com/metalgeekhub/llmbench/internal/secrets"
	"github.com/metalgeekhub/llmbench/internal/store"
)

func newManager(t *testing.T, env []config.SourceConfig) (*Manager, *store.SQLite) {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.New("test")
	return New(st, box, env), st
}

func strp(s string) *string { return &s }

func TestEnvSourcesAreReadOnly(t *testing.T) {
	m, _ := newManager(t, []config.SourceConfig{{
		Name: "local", Type: "openai", BaseURL: "http://x/v1", APIKey: "sk-env",
		Headers: map[string]string{"X-B": "1", "X-A": "2"}, Models: []string{"m1"},
	}})
	ctx := context.Background()
	list, err := m.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	s := list[0]
	if s.ID != "env-local" || !s.ReadOnly || s.ManagedBy != ManagedByEnv || !s.HasAPIKey ||
		!reflect.DeepEqual(s.HeaderNames, []string{"X-A", "X-B"}) {
		t.Errorf("env source view = %+v", s)
	}
	if _, err := m.Update(ctx, "env-local", Input{}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("update err = %v", err)
	}
	if err := m.Delete(ctx, "env-local"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("delete err = %v", err)
	}
	var ve *ValidationError
	if _, err := m.Create(ctx, Input{Name: "LOCAL", Type: "openai", BaseURL: "http://y"}); !errors.As(err, &ve) {
		t.Errorf("name clash with env source should fail validation, got %v", err)
	}
}

func TestUISourceSecretsEncryptedAndWriteOnly(t *testing.T) {
	m, st := newManager(t, nil)
	ctx := context.Background()

	created, err := m.Create(ctx, Input{
		Name: "gw", Type: "OpenAI", BaseURL: "https://gw.example/v1/", APIKey: strp("sk-secret"),
		Headers: map[string]string{"X-Token": "tok"}, Models: []string{" a ", "b", "a", ""}, TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Type != "openai" || created.BaseURL != "https://gw.example/v1" || !created.HasAPIKey ||
		!reflect.DeepEqual(created.Models, []string{"a", "b"}) || created.ModelsAuto || created.ReadOnly {
		t.Errorf("created = %+v", created)
	}

	raw, _ := st.GetSource(ctx, created.ID)
	if strings.Contains(raw.APIKeyEnc, "sk-secret") || strings.Contains(raw.HeadersEnc, "tok") || raw.APIKeyEnc == "" {
		t.Errorf("secrets not encrypted at rest: %+v", raw)
	}
	cfg, err := m.providerConfig(ctx, created.ID)
	if err != nil || cfg.APIKey != "sk-secret" || cfg.Headers["X-Token"] != "tok" {
		t.Errorf("decrypted config = %+v, %v", cfg, err)
	}

	// Update without api_key/headers keeps them.
	updated, err := m.Update(ctx, created.ID, Input{Name: "gw2", Type: "openai", BaseURL: "https://gw.example/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.HasAPIKey || !updated.ModelsAuto || updated.Name != "gw2" {
		t.Errorf("updated = %+v", updated)
	}
	cfg, _ = m.providerConfig(ctx, created.ID)
	if cfg.APIKey != "sk-secret" || cfg.Headers["X-Token"] != "tok" {
		t.Errorf("secrets lost on update: %+v", cfg)
	}

	// Explicit empty values clear them.
	updated, err = m.Update(ctx, created.ID, Input{Name: "gw2", Type: "openai", BaseURL: "https://gw.example/v1",
		APIKey: strp(""), Headers: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.HasAPIKey || len(updated.HeaderNames) != 0 {
		t.Errorf("secrets not cleared: %+v", updated)
	}

	if err := m.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete err = %v", err)
	}
}

func TestValidation(t *testing.T) {
	m, _ := newManager(t, nil)
	ctx := context.Background()
	cases := []Input{
		{Type: "openai", BaseURL: "http://x"},
		{Name: "a", Type: "bedrock", BaseURL: "http://x"},
		{Name: "a", Type: "openai", BaseURL: "ftp://x"},
		{Name: "a", Type: "openai", BaseURL: "not a url"},
		{Name: "a", Type: "openai", BaseURL: "http://x", TimeoutSeconds: -1},
		{Name: strings.Repeat("n", 65), Type: "openai", BaseURL: "http://x"},
	}
	for i, in := range cases {
		var ve *ValidationError
		if _, err := m.Create(ctx, in); !errors.As(err, &ve) {
			t.Errorf("case %d: err = %v, want ValidationError", i, err)
		}
	}
	if _, err := m.Create(ctx, Input{Name: "dup", Type: "openai", BaseURL: "http://x"}); err != nil {
		t.Fatal(err)
	}
	var ve *ValidationError
	if _, err := m.Create(ctx, Input{Name: "dup", Type: "openai", BaseURL: "http://x"}); !errors.As(err, &ve) {
		t.Errorf("duplicate name err = %v", err)
	}
}

func TestModelsAutoDiscoveryIsCached(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"data":[{"id":"m2"},{"id":"m1"}]}`)
	}))
	defer srv.Close()

	m, _ := newManager(t, []config.SourceConfig{
		{Name: "auto", Type: "openai", BaseURL: srv.URL, ModelsAuto: true},
		{Name: "fixed", Type: "openai", BaseURL: srv.URL, Models: []string{"x"}},
		{Name: "down", Type: "openai", BaseURL: "http://127.0.0.1:1", ModelsAuto: true},
	})
	ctx := context.Background()

	ml, err := m.Models(ctx, "env-auto", false)
	if err != nil || !ml.Auto || !reflect.DeepEqual(ml.Models, []string{"m1", "m2"}) {
		t.Fatalf("auto models = %+v, %v", ml, err)
	}
	_, _ = m.Models(ctx, "env-auto", false)
	if calls.Load() != 1 {
		t.Errorf("expected cached result, got %d calls", calls.Load())
	}
	_, _ = m.Models(ctx, "env-auto", true)
	if calls.Load() != 2 {
		t.Errorf("refresh should bypass cache, got %d calls", calls.Load())
	}

	ml, err = m.Models(ctx, "env-fixed", false)
	if err != nil || ml.Auto || !reflect.DeepEqual(ml.Models, []string{"x"}) {
		t.Errorf("fixed models = %+v, %v", ml, err)
	}

	ml, err = m.Models(ctx, "env-down", false)
	if err != nil || ml.Error == "" {
		t.Errorf("unreachable source should report error in list: %+v, %v", ml, err)
	}

	if _, err := m.Models(ctx, "nope", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown source err = %v", err)
	}
}
