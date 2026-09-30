// Package sources manages LLM endpoint connections from two origins:
// environment/YAML (read-only) and the UI (stored in the database with
// secrets encrypted).
package sources

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/metalgeekhub/llmbench/internal/config"
	"github.com/metalgeekhub/llmbench/internal/providers"
	"github.com/metalgeekhub/llmbench/internal/secrets"
	"github.com/metalgeekhub/llmbench/internal/store"
)

const (
	ManagedByEnv = "env"
	ManagedByUI  = "ui"

	envIDPrefix   = "env-"
	modelCacheTTL = 5 * time.Minute
	maxNameLength = 64
)

var (
	ErrNotFound = errors.New("source not found")
	ErrReadOnly = errors.New("source is managed by environment and cannot be modified")
)

// ValidationError reports invalid user input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Source is the public view of a source. It never contains secrets.
type Source struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Type           string         `json:"type"`
	BaseURL        string         `json:"base_url"`
	HasAPIKey      bool           `json:"has_api_key"`
	HeaderNames    []string       `json:"header_names"`
	ExtraBody      map[string]any `json:"extra_body"`
	TimeoutSeconds float64        `json:"timeout_seconds"`
	TLSSkipVerify  bool           `json:"tls_skip_verify"`
	Models         []string       `json:"models"`
	ModelsAuto     bool           `json:"models_auto"`
	ManagedBy      string         `json:"managed_by"`
	ReadOnly       bool           `json:"read_only"`
}

// Input creates or updates a UI source. APIKey nil keeps the stored key
// (update only); "" clears it. Headers nil keeps stored headers; a non-nil
// map (even empty) replaces them.
type Input struct {
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	BaseURL        string            `json:"base_url"`
	APIKey         *string           `json:"api_key"`
	Headers        map[string]string `json:"headers"`
	ExtraBody      map[string]any    `json:"extra_body"`
	TimeoutSeconds float64           `json:"timeout_seconds"`
	TLSSkipVerify  bool              `json:"tls_skip_verify"`
	Models         []string          `json:"models"`
	ModelsAuto     bool              `json:"models_auto"`
}

// ModelList is the effective model list of a source.
type ModelList struct {
	Models []string `json:"models"`
	Auto   bool     `json:"auto"`
	// Error is set when auto discovery failed.
	Error string `json:"error,omitempty"`
}

type cachedModels struct {
	models  []string
	fetched time.Time
}

// Manager combines env and UI sources.
type Manager struct {
	store store.Store
	box   *secrets.Box
	env   map[string]config.SourceConfig // by ID

	mu        sync.Mutex
	providers map[string]providers.Provider
	models    map[string]cachedModels
}

// New creates a Manager. envSources are exposed read-only.
func New(st store.Store, box *secrets.Box, envSources []config.SourceConfig) *Manager {
	m := &Manager{
		store:     st,
		box:       box,
		env:       map[string]config.SourceConfig{},
		providers: map[string]providers.Provider{},
		models:    map[string]cachedModels{},
	}
	for _, s := range envSources {
		m.env[envIDPrefix+s.Name] = s
	}
	return m
}

// List returns env sources followed by UI sources, each sorted by name.
func (m *Manager) List(ctx context.Context) ([]Source, error) {
	out := make([]Source, 0, len(m.env))
	for id, s := range m.env {
		out = append(out, envView(id, s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	stored, err := m.store.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range stored {
		v, err := m.uiView(s)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Get returns one source by ID.
func (m *Manager) Get(ctx context.Context, id string) (Source, error) {
	if s, ok := m.env[id]; ok {
		return envView(id, s), nil
	}
	s, err := m.store.GetSource(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, err
	}
	return m.uiView(s)
}

// Create adds a UI source.
func (m *Manager) Create(ctx context.Context, in Input) (Source, error) {
	s := store.Source{ID: uuid.NewString()}
	if err := m.apply(&s, in, true); err != nil {
		return Source{}, err
	}
	if err := m.store.CreateSource(ctx, &s); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return Source{}, invalid("a source named %q already exists", s.Name)
		}
		return Source{}, err
	}
	return m.uiView(s)
}

// Update modifies a UI source.
func (m *Manager) Update(ctx context.Context, id string, in Input) (Source, error) {
	if _, ok := m.env[id]; ok {
		return Source{}, ErrReadOnly
	}
	s, err := m.store.GetSource(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, err
	}
	if err := m.apply(&s, in, false); err != nil {
		return Source{}, err
	}
	if err := m.store.UpdateSource(ctx, &s); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return Source{}, invalid("a source named %q already exists", s.Name)
		}
		return Source{}, err
	}
	m.invalidate(id)
	return m.uiView(s)
}

// Delete removes a UI source.
func (m *Manager) Delete(ctx context.Context, id string) error {
	if _, ok := m.env[id]; ok {
		return ErrReadOnly
	}
	err := m.store.DeleteSource(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	m.invalidate(id)
	return err
}

func (m *Manager) invalidate(id string) {
	m.mu.Lock()
	delete(m.providers, id)
	delete(m.models, id)
	m.mu.Unlock()
}

// apply validates in and writes it onto s.
func (m *Manager) apply(s *store.Source, in Input, create bool) error {
	name := strings.TrimSpace(in.Name)
	switch {
	case name == "":
		return invalid("name is required")
	case len(name) > maxNameLength:
		return invalid("name must be at most %d characters", maxNameLength)
	}
	for _, e := range m.env {
		if strings.EqualFold(e.Name, name) {
			return invalid("name %q is already used by an environment source", name)
		}
	}

	typ := strings.ToLower(strings.TrimSpace(in.Type))
	if !slices.Contains(providers.SupportedTypes, typ) {
		return invalid("unsupported type %q (supported: %s)", in.Type, strings.Join(providers.SupportedTypes, ", "))
	}

	baseURL := strings.TrimSpace(in.BaseURL)
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalid("base URL must be an absolute http(s) URL")
	}

	if in.TimeoutSeconds < 0 {
		return invalid("timeout must not be negative")
	}

	for k := range in.Headers {
		if strings.TrimSpace(k) == "" {
			return invalid("header names must not be empty")
		}
	}

	s.Name = name
	s.Type = typ
	s.BaseURL = strings.TrimRight(baseURL, "/")
	s.ExtraBody = in.ExtraBody
	s.Timeout = time.Duration(in.TimeoutSeconds * float64(time.Second))
	s.TLSSkipVerify = in.TLSSkipVerify
	s.Models = cleanModels(in.Models)
	s.ModelsAuto = in.ModelsAuto || len(s.Models) == 0

	if in.APIKey != nil || create {
		key := ""
		if in.APIKey != nil {
			key = strings.TrimSpace(*in.APIKey)
		}
		if s.APIKeyEnc, err = m.box.Encrypt(key); err != nil {
			return err
		}
	}
	if in.Headers != nil || create {
		enc := ""
		if len(in.Headers) > 0 {
			if enc, err = m.box.Encrypt(encodeHeaders(in.Headers)); err != nil {
				return err
			}
		}
		s.HeadersEnc = enc
	}
	return nil
}

func cleanModels(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

func envView(id string, s config.SourceConfig) Source {
	return Source{
		ID:             id,
		Name:           s.Name,
		Type:           s.Type,
		BaseURL:        s.BaseURL,
		HasAPIKey:      s.APIKey != "",
		HeaderNames:    headerNames(s.Headers),
		ExtraBody:      s.ExtraBody,
		TimeoutSeconds: s.Timeout.Seconds(),
		TLSSkipVerify:  s.TLSSkipVerify,
		Models:         orEmpty(s.Models),
		ModelsAuto:     s.ModelsAuto,
		ManagedBy:      ManagedByEnv,
		ReadOnly:       true,
	}
}

func (m *Manager) uiView(s store.Source) (Source, error) {
	headers, err := m.decryptHeaders(s.HeadersEnc)
	if err != nil {
		// Still list the source so the user can fix it by re-entering headers.
		headers = nil
	}
	return Source{
		ID:             s.ID,
		Name:           s.Name,
		Type:           s.Type,
		BaseURL:        s.BaseURL,
		HasAPIKey:      s.APIKeyEnc != "",
		HeaderNames:    headerNames(headers),
		ExtraBody:      s.ExtraBody,
		TimeoutSeconds: s.Timeout.Seconds(),
		TLSSkipVerify:  s.TLSSkipVerify,
		Models:         orEmpty(s.Models),
		ModelsAuto:     s.ModelsAuto,
		ManagedBy:      ManagedByUI,
		ReadOnly:       false,
	}, nil
}

func headerNames(h map[string]string) []string {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// providerConfig resolves the decrypted adapter config for a source.
func (m *Manager) providerConfig(ctx context.Context, id string) (providers.Config, error) {
	if s, ok := m.env[id]; ok {
		return providers.Config{
			Type: s.Type, BaseURL: s.BaseURL, APIKey: s.APIKey, Headers: s.Headers,
			ExtraBody: s.ExtraBody, Timeout: s.Timeout, TLSSkipVerify: s.TLSSkipVerify,
		}, nil
	}
	s, err := m.store.GetSource(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return providers.Config{}, ErrNotFound
	}
	if err != nil {
		return providers.Config{}, err
	}
	key, err := m.box.Decrypt(s.APIKeyEnc)
	if err != nil {
		return providers.Config{}, fmt.Errorf("source %q API key: %w", s.Name, err)
	}
	headers, err := m.decryptHeaders(s.HeadersEnc)
	if err != nil {
		return providers.Config{}, fmt.Errorf("source %q headers: %w", s.Name, err)
	}
	return providers.Config{
		Type: s.Type, BaseURL: s.BaseURL, APIKey: key, Headers: headers,
		ExtraBody: s.ExtraBody, Timeout: s.Timeout, TLSSkipVerify: s.TLSSkipVerify,
	}, nil
}

// Provider returns the (cached) adapter for a source.
func (m *Manager) Provider(ctx context.Context, id string) (providers.Provider, error) {
	m.mu.Lock()
	p, ok := m.providers[id]
	m.mu.Unlock()
	if ok {
		return p, nil
	}
	cfg, err := m.providerConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	p, err = providers.New(cfg)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.providers[id] = p
	m.mu.Unlock()
	return p, nil
}

// Discover fetches the model list from the source's /models endpoint.
func (m *Manager) Discover(ctx context.Context, id string) ([]string, error) {
	p, err := m.Provider(ctx, id)
	if err != nil {
		return nil, err
	}
	models, err := p.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.models[id] = cachedModels{models: models, fetched: time.Now()}
	m.mu.Unlock()
	return models, nil
}

// Models returns the effective model list: the configured list, or for
// auto sources the discovered list (cached; refresh bypasses the cache).
func (m *Manager) Models(ctx context.Context, id string, refresh bool) (ModelList, error) {
	src, err := m.Get(ctx, id)
	if err != nil {
		return ModelList{}, err
	}
	if !src.ModelsAuto {
		return ModelList{Models: src.Models}, nil
	}
	if !refresh {
		m.mu.Lock()
		c, ok := m.models[id]
		m.mu.Unlock()
		if ok && time.Since(c.fetched) < modelCacheTTL {
			return ModelList{Models: c.models, Auto: true}, nil
		}
	}
	models, err := m.Discover(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ModelList{}, err
		}
		return ModelList{Models: []string{}, Auto: true, Error: err.Error()}, nil
	}
	return ModelList{Models: models, Auto: true}, nil
}
