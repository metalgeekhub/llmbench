// Package config loads application settings and env-defined sources from
// environment variables and an optional YAML file.
//
// Precedence is defaults < YAML file < environment variables.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	envPrefix       = "LLMB_"
	sourceEnvPrefix = "LLMB_SOURCE_"

	DefaultListen  = ":8080"
	DefaultDataDir = "./data"
)

// Config is the fully resolved application configuration.
type Config struct {
	Listen        string
	DataDir       string
	SecretKey     string
	AdminPassword string
	ConfigFile    string
	Sources       []SourceConfig
}

// SourceConfig is a source defined outside the UI (env or YAML).
type SourceConfig struct {
	Name          string
	Type          string
	BaseURL       string
	APIKey        string
	Models        []string
	ModelsAuto    bool
	Headers       map[string]string
	ExtraBody     map[string]any
	Timeout       time.Duration
	TLSSkipVerify bool
}

// sourceFields lists recognised LLMB_SOURCE_<NAME>_<FIELD> suffixes. Names
// may contain underscores, so fields are matched by suffix, longest first.
var sourceFields = []string{
	"TLS_SKIP_VERIFY",
	"EXTRA_BODY",
	"BASE_URL",
	"API_KEY",
	"HEADERS",
	"TIMEOUT",
	"MODELS",
	"TYPE",
}

// Load resolves configuration from the process environment and, if
// LLMB_CONFIG_FILE is set, the YAML file it points to.
func Load() (*Config, error) {
	return LoadFrom(os.Environ(), os.ReadFile)
}

// LoadFrom resolves configuration from the given environment (KEY=VALUE
// entries) using readFile to read the optional YAML file.
func LoadFrom(environ []string, readFile func(string) ([]byte, error)) (*Config, error) {
	env := envMap(environ)
	cfg := &Config{Listen: DefaultListen, DataDir: DefaultDataDir}

	cfg.ConfigFile = env["LLMB_CONFIG_FILE"]
	sources := map[string]*SourceConfig{}
	if cfg.ConfigFile != "" {
		data, err := readFile(cfg.ConfigFile)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		if err := applyYAML(cfg, sources, data); err != nil {
			return nil, fmt.Errorf("parsing config file %s: %w", cfg.ConfigFile, err)
		}
	}

	if v, ok := env["LLMB_LISTEN"]; ok && v != "" {
		cfg.Listen = v
	}
	if v, ok := env["LLMB_DATA_DIR"]; ok && v != "" {
		cfg.DataDir = v
	}
	if v, ok := env["LLMB_SECRET_KEY"]; ok {
		cfg.SecretKey = v
	}
	if v, ok := env["LLMB_ADMIN_PASSWORD"]; ok {
		cfg.AdminPassword = v
	}

	if err := applySourceEnv(sources, env); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := sources[n]
		if err := validateSource(s); err != nil {
			return nil, err
		}
		cfg.Sources = append(cfg.Sources, *s)
	}
	return cfg, nil
}

func envMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, envPrefix) {
			continue
		}
		m[k] = v
	}
	return m
}

// parseSourceKey splits "LLMB_SOURCE_MY_VLLM_BASE_URL" into ("my_vllm", "BASE_URL").
func parseSourceKey(key string) (name, field string, ok bool) {
	rest, ok := strings.CutPrefix(key, sourceEnvPrefix)
	if !ok {
		return "", "", false
	}
	for _, f := range sourceFields {
		if n, found := strings.CutSuffix(rest, "_"+f); found && n != "" {
			return strings.ToLower(n), f, true
		}
	}
	return "", "", false
}

func applySourceEnv(sources map[string]*SourceConfig, env map[string]string) error {
	// Sort keys so errors are deterministic.
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		name, field, ok := parseSourceKey(key)
		if !ok {
			continue
		}
		s := sources[name]
		if s == nil {
			s = &SourceConfig{Name: name}
			sources[name] = s
		}
		if err := setSourceField(s, field, env[key]); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

func setSourceField(s *SourceConfig, field, raw string) error {
	v := strings.TrimSpace(raw)
	switch field {
	case "TYPE":
		s.Type = strings.ToLower(v)
	case "BASE_URL":
		s.BaseURL = v
	case "API_KEY":
		s.APIKey = v
	case "MODELS":
		s.Models, s.ModelsAuto = parseModels(v)
	case "HEADERS":
		if v == "" {
			s.Headers = nil
			return nil
		}
		var h map[string]string
		if err := json.Unmarshal([]byte(v), &h); err != nil {
			return fmt.Errorf("invalid JSON object of strings: %w", err)
		}
		s.Headers = h
	case "EXTRA_BODY":
		if v == "" {
			s.ExtraBody = nil
			return nil
		}
		var b map[string]any
		if err := json.Unmarshal([]byte(v), &b); err != nil {
			return fmt.Errorf("invalid JSON object: %w", err)
		}
		s.ExtraBody = b
	case "TIMEOUT":
		d, err := ParseTimeout(v)
		if err != nil {
			return err
		}
		s.Timeout = d
	case "TLS_SKIP_VERIFY":
		if v == "" {
			s.TLSSkipVerify = false
			return nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid boolean %q", v)
		}
		s.TLSSkipVerify = b
	}
	return nil
}

// parseModels parses a comma-separated model list; "auto" means fetch from
// the provider's /models endpoint.
func parseModels(v string) (models []string, auto bool) {
	if strings.EqualFold(strings.TrimSpace(v), "auto") {
		return nil, true
	}
	for _, m := range strings.Split(v, ",") {
		if m = strings.TrimSpace(m); m != "" {
			models = append(models, m)
		}
	}
	return models, false
}

// ParseTimeout accepts a Go duration ("90s", "2m") or a plain number of seconds.
func ParseTimeout(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if secs < 0 {
			return 0, fmt.Errorf("timeout must not be negative")
		}
		return time.Duration(secs * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q (use seconds or a duration like 90s)", v)
	}
	if d < 0 {
		return 0, fmt.Errorf("timeout must not be negative")
	}
	return d, nil
}

func validateSource(s *SourceConfig) error {
	if s.Type == "" {
		return fmt.Errorf("source %q: TYPE is required", s.Name)
	}
	if s.BaseURL == "" {
		return fmt.Errorf("source %q: BASE_URL is required", s.Name)
	}
	if !s.ModelsAuto && len(s.Models) == 0 {
		// No explicit list: discover from /models.
		s.ModelsAuto = true
	}
	return nil
}

// yamlFile mirrors the env var structure.
type yamlFile struct {
	Listen        string                `yaml:"listen"`
	DataDir       string                `yaml:"data_dir"`
	SecretKey     string                `yaml:"secret_key"`
	AdminPassword string                `yaml:"admin_password"`
	Sources       map[string]yamlSource `yaml:"sources"`
}

type yamlSource struct {
	Type          string            `yaml:"type"`
	BaseURL       string            `yaml:"base_url"`
	APIKey        string            `yaml:"api_key"`
	Models        yamlModels        `yaml:"models"`
	Headers       map[string]string `yaml:"headers"`
	ExtraBody     map[string]any    `yaml:"extra_body"`
	Timeout       string            `yaml:"timeout"`
	TLSSkipVerify bool              `yaml:"tls_skip_verify"`
}

// yamlModels accepts either a list or a scalar ("auto" or comma-separated).
type yamlModels struct {
	list []string
	auto bool
}

func (m *yamlModels) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		m.list, m.auto = parseModels(n.Value)
		return nil
	case yaml.SequenceNode:
		var l []string
		if err := n.Decode(&l); err != nil {
			return err
		}
		m.list, m.auto = parseModels(strings.Join(l, ","))
		return nil
	default:
		return fmt.Errorf("models must be a list or a string")
	}
}

func applyYAML(cfg *Config, sources map[string]*SourceConfig, data []byte) error {
	var f yamlFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return err
	}
	if f.Listen != "" {
		cfg.Listen = f.Listen
	}
	if f.DataDir != "" {
		cfg.DataDir = f.DataDir
	}
	cfg.SecretKey = f.SecretKey
	cfg.AdminPassword = f.AdminPassword

	for name, ys := range f.Sources {
		timeout, err := ParseTimeout(ys.Timeout)
		if err != nil {
			return fmt.Errorf("source %q: %w", name, err)
		}
		key := strings.ToLower(name)
		sources[key] = &SourceConfig{
			Name:          key,
			Type:          strings.ToLower(ys.Type),
			BaseURL:       ys.BaseURL,
			APIKey:        ys.APIKey,
			Models:        ys.Models.list,
			ModelsAuto:    ys.Models.auto,
			Headers:       ys.Headers,
			ExtraBody:     ys.ExtraBody,
			Timeout:       timeout,
			TLSSkipVerify: ys.TLSSkipVerify,
		}
	}
	return nil
}
