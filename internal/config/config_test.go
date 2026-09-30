package config

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func noFile(string) ([]byte, error) { return nil, errors.New("no file") }

func TestParseSourceKey(t *testing.T) {
	tests := []struct {
		key, name, field string
		ok               bool
	}{
		{"LLMB_SOURCE_LOCALVLLM_TYPE", "localvllm", "TYPE", true},
		{"LLMB_SOURCE_LOCALVLLM_BASE_URL", "localvllm", "BASE_URL", true},
		{"LLMB_SOURCE_MY_VLLM_API_KEY", "my_vllm", "API_KEY", true},
		{"LLMB_SOURCE_X_TLS_SKIP_VERIFY", "x", "TLS_SKIP_VERIFY", true},
		{"LLMB_SOURCE_GW_EXTRA_BODY", "gw", "EXTRA_BODY", true},
		{"LLMB_SOURCE_A_B_C_MODELS", "a_b_c", "MODELS", true},
		{"LLMB_SOURCE_TYPE", "", "", false},      // no name
		{"LLMB_SOURCE_FOO_COLOR", "", "", false}, // unknown field
		{"LLMB_LISTEN", "", "", false},           // not a source key
		{"LLMB_SOURCE__TYPE", "", "", false},     // empty name
	}
	for _, tt := range tests {
		name, field, ok := parseSourceKey(tt.key)
		if name != tt.name || field != tt.field || ok != tt.ok {
			t.Errorf("parseSourceKey(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.key, name, field, ok, tt.name, tt.field, tt.ok)
		}
	}
}

func TestLoadFromEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"LLMB_LISTEN=:9090",
		"LLMB_DATA_DIR=/data",
		"LLMB_SECRET_KEY=s3cret",
		"LLMB_SOURCE_LOCALVLLM_TYPE=openai",
		"LLMB_SOURCE_LOCALVLLM_BASE_URL=http://10.0.0.5:8000/v1",
		"LLMB_SOURCE_LOCALVLLM_MODELS=qwen3-32b, llama-3.3-70b",
		`LLMB_SOURCE_LOCALVLLM_EXTRA_BODY={"chat_template_kwargs":{"enable_thinking":true}}`,
		"LLMB_SOURCE_LOCALVLLM_TIMEOUT=90",
		"LLMB_SOURCE_OPENAI_TYPE=OpenAI",
		"LLMB_SOURCE_OPENAI_BASE_URL=https://api.openai.com/v1",
		"LLMB_SOURCE_OPENAI_API_KEY=sk-test",
		"LLMB_SOURCE_OPENAI_MODELS=auto",
		"LLMB_SOURCE_INTERNAL_GW_TYPE=openai",
		"LLMB_SOURCE_INTERNAL_GW_BASE_URL=https://llm.internal/v1",
		`LLMB_SOURCE_INTERNAL_GW_HEADERS={"X-Api-Token":"abc123"}`,
		"LLMB_SOURCE_INTERNAL_GW_TLS_SKIP_VERIFY=true",
		"LLMB_SOURCE_INTERNAL_GW_TIMEOUT=2m",
	}
	cfg, err := LoadFrom(env, noFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9090" || cfg.DataDir != "/data" || cfg.SecretKey != "s3cret" {
		t.Errorf("app settings = %+v", cfg)
	}
	if len(cfg.Sources) != 3 {
		t.Fatalf("got %d sources, want 3", len(cfg.Sources))
	}

	// Sorted by name: internal_gw, localvllm, openai.
	gw, vllm, oai := cfg.Sources[0], cfg.Sources[1], cfg.Sources[2]

	if gw.Name != "internal_gw" || !gw.TLSSkipVerify || gw.Timeout != 2*time.Minute ||
		!reflect.DeepEqual(gw.Headers, map[string]string{"X-Api-Token": "abc123"}) {
		t.Errorf("gateway source = %+v", gw)
	}
	if !gw.ModelsAuto {
		t.Errorf("source without MODELS should default to auto discovery")
	}

	if vllm.Type != "openai" || vllm.BaseURL != "http://10.0.0.5:8000/v1" || vllm.Timeout != 90*time.Second {
		t.Errorf("vllm source = %+v", vllm)
	}
	if !reflect.DeepEqual(vllm.Models, []string{"qwen3-32b", "llama-3.3-70b"}) || vllm.ModelsAuto {
		t.Errorf("vllm models = %v auto=%v", vllm.Models, vllm.ModelsAuto)
	}
	kw, _ := vllm.ExtraBody["chat_template_kwargs"].(map[string]any)
	if kw["enable_thinking"] != true {
		t.Errorf("vllm extra body = %v", vllm.ExtraBody)
	}

	if oai.Type != "openai" || oai.APIKey != "sk-test" || !oai.ModelsAuto || len(oai.Models) != 0 {
		t.Errorf("openai source = %+v", oai)
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(nil, noFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != DefaultListen || cfg.DataDir != DefaultDataDir || len(cfg.Sources) != 0 {
		t.Errorf("defaults = %+v", cfg)
	}
}

func TestLoadFromEnvErrors(t *testing.T) {
	tests := map[string][]string{
		"missing type":     {"LLMB_SOURCE_A_BASE_URL=http://x"},
		"missing base url": {"LLMB_SOURCE_A_TYPE=openai"},
		"bad headers":      {"LLMB_SOURCE_A_TYPE=openai", "LLMB_SOURCE_A_BASE_URL=http://x", "LLMB_SOURCE_A_HEADERS=nope"},
		"bad extra body":   {"LLMB_SOURCE_A_TYPE=openai", "LLMB_SOURCE_A_BASE_URL=http://x", "LLMB_SOURCE_A_EXTRA_BODY=[1]"},
		"bad bool":         {"LLMB_SOURCE_A_TYPE=openai", "LLMB_SOURCE_A_BASE_URL=http://x", "LLMB_SOURCE_A_TLS_SKIP_VERIFY=maybe"},
		"bad timeout":      {"LLMB_SOURCE_A_TYPE=openai", "LLMB_SOURCE_A_BASE_URL=http://x", "LLMB_SOURCE_A_TIMEOUT=soon"},
		"negative timeout": {"LLMB_SOURCE_A_TYPE=openai", "LLMB_SOURCE_A_BASE_URL=http://x", "LLMB_SOURCE_A_TIMEOUT=-5"},
		"missing file":     {"LLMB_CONFIG_FILE=/nope.yaml"},
	}
	for name, env := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadFrom(env, noFile); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestParseTimeout(t *testing.T) {
	tests := map[string]time.Duration{
		"":    0,
		"30":  30 * time.Second,
		"1.5": 1500 * time.Millisecond,
		"90s": 90 * time.Second,
		"2m":  2 * time.Minute,
		" 5 ": 5 * time.Second,
	}
	for in, want := range tests {
		got, err := ParseTimeout(in)
		if err != nil || got != want {
			t.Errorf("ParseTimeout(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestYAMLWithEnvOverride(t *testing.T) {
	yamlData := `
listen: ":7000"
data_dir: /var/lib/llmbench
secret_key: from-yaml
sources:
  localvllm:
    type: openai
    base_url: http://yaml-host:8000/v1
    models: [qwen3-32b, llama-3.3-70b]
    extra_body:
      chat_template_kwargs:
        enable_thinking: false
    timeout: 45s
  hosted:
    type: openai
    base_url: https://api.example.com/v1
    api_key: sk-yaml
    models: auto
    headers:
      X-Org: team-a
`
	read := func(path string) ([]byte, error) {
		if path != "/etc/llmbench.yaml" {
			t.Fatalf("unexpected path %q", path)
		}
		return []byte(yamlData), nil
	}
	env := []string{
		"LLMB_CONFIG_FILE=/etc/llmbench.yaml",
		"LLMB_SECRET_KEY=from-env",
		"LLMB_SOURCE_LOCALVLLM_BASE_URL=http://env-host:8000/v1",
	}
	cfg, err := LoadFrom(env, read)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":7000" || cfg.DataDir != "/var/lib/llmbench" || cfg.SecretKey != "from-env" {
		t.Errorf("app settings = %+v", cfg)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("got %d sources", len(cfg.Sources))
	}
	hosted, vllm := cfg.Sources[0], cfg.Sources[1]
	if !hosted.ModelsAuto || hosted.APIKey != "sk-yaml" || hosted.Headers["X-Org"] != "team-a" {
		t.Errorf("hosted = %+v", hosted)
	}
	if vllm.BaseURL != "http://env-host:8000/v1" {
		t.Errorf("env should override YAML base URL, got %q", vllm.BaseURL)
	}
	if vllm.Timeout != 45*time.Second || !reflect.DeepEqual(vllm.Models, []string{"qwen3-32b", "llama-3.3-70b"}) {
		t.Errorf("vllm = %+v", vllm)
	}
}
