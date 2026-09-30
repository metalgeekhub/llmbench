// Package definitions manages saved, versioned benchmark configurations
// that can be re-run, and exported/imported as YAML or JSON.
package definitions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/metalgeekhub/llmbench/internal/runner"
	"github.com/metalgeekhub/llmbench/internal/store"
)

const maxNameLength = 100

// FileKind marks exported definition files.
const FileKind = "llmbench-definition"

// ValidationError reports invalid input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Input creates or updates a definition.
type Input struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Config      runner.Config `json:"config"`
}

type Service struct {
	store  store.Store
	runner *runner.Runner
}

func NewService(st store.Store, r *runner.Runner) *Service {
	return &Service{store: st, runner: r}
}

func (s *Service) List(ctx context.Context) ([]store.Definition, error) {
	return s.store.ListDefinitions(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (store.Definition, error) {
	return s.store.GetDefinition(ctx, id)
}

func (s *Service) Versions(ctx context.Context, id string) ([]store.DefinitionVersion, error) {
	if _, err := s.store.GetDefinition(ctx, id); err != nil {
		return nil, err
	}
	return s.store.ListDefinitionVersions(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.DeleteDefinition(ctx, id)
}

// Create saves a new definition at version 1.
func (s *Service) Create(ctx context.Context, in Input) (store.Definition, error) {
	return s.save(ctx, uuid.NewString(), in)
}

// Update saves in as the next version of an existing definition.
func (s *Service) Update(ctx context.Context, id string, in Input) (store.Definition, error) {
	if _, err := s.store.GetDefinition(ctx, id); err != nil {
		return store.Definition{}, err
	}
	return s.save(ctx, id, in)
}

func (s *Service) save(ctx context.Context, id string, in Input) (store.Definition, error) {
	name := strings.TrimSpace(in.Name)
	switch {
	case name == "":
		return store.Definition{}, invalid("name is required")
	case len(name) > maxNameLength:
		return store.Definition{}, invalid("name must be at most %d characters", maxNameLength)
	}
	cfg := in.Config
	if err := cfg.Validate(); err != nil {
		return store.Definition{}, err
	}
	// The run name comes from the definition unless the config sets one.
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return store.Definition{}, err
	}
	d := store.Definition{ID: id, Name: name, Description: strings.TrimSpace(in.Description), Config: cfgJSON}
	if err := s.store.SaveDefinition(ctx, &d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return store.Definition{}, invalid("a definition named %q already exists", name)
		}
		return store.Definition{}, err
	}
	return d, nil
}

// Run starts the latest version of a definition.
func (s *Service) Run(ctx context.Context, id string) (runner.RunView, error) {
	d, err := s.store.GetDefinition(ctx, id)
	if err != nil {
		return runner.RunView{}, err
	}
	var cfg runner.Config
	if err := json.Unmarshal(d.Config, &cfg); err != nil {
		return runner.RunView{}, fmt.Errorf("definition %q has an unreadable config: %w", d.Name, err)
	}
	if cfg.Name == "" {
		cfg.Name = fmt.Sprintf("%s (v%d)", d.Name, d.Version)
	}
	return s.runner.StartWith(ctx, cfg, runner.StartOptions{DefinitionID: d.ID, DefinitionVersion: d.Version})
}

// file is the exported form of a definition.
type file struct {
	Kind        string         `json:"kind" yaml:"kind"`
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Version     int            `json:"version,omitempty" yaml:"version,omitempty"`
	Config      map[string]any `json:"config" yaml:"config"`
}

// Export renders a definition as "yaml" or "json".
func (s *Service) Export(ctx context.Context, id, format string) ([]byte, error) {
	d, err := s.store.GetDefinition(ctx, id)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(d.Config, &cfg); err != nil {
		return nil, err
	}
	f := file{Kind: FileKind, Name: d.Name, Description: d.Description, Version: d.Version, Config: cfg}
	switch format {
	case "json":
		return json.MarshalIndent(f, "", "  ")
	case "yaml", "":
		return yaml.Marshal(f)
	default:
		return nil, invalid("format must be yaml or json")
	}
}

// Import creates a definition from an exported YAML or JSON file. If a
// definition with that name exists, the file is saved as its next version.
func (s *Service) Import(ctx context.Context, data []byte) (store.Definition, error) {
	// YAML is a superset of JSON, so one decoder handles both.
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil || raw == nil {
		return store.Definition{}, invalid("not a YAML or JSON definition file")
	}
	if kind, _ := raw["kind"].(string); kind != FileKind {
		return store.Definition{}, invalid("not an LLMBench definition file (expected kind: %s)", FileKind)
	}
	cfgJSON, err := json.Marshal(raw["config"])
	if err != nil {
		return store.Definition{}, invalid("config: %v", err)
	}
	var in Input
	if err := json.Unmarshal(cfgJSON, &in.Config); err != nil {
		return store.Definition{}, invalid("config: %v", err)
	}
	in.Name, _ = raw["name"].(string)
	in.Description, _ = raw["description"].(string)

	list, err := s.store.ListDefinitions(ctx)
	if err != nil {
		return store.Definition{}, err
	}
	for _, d := range list {
		if strings.EqualFold(d.Name, strings.TrimSpace(in.Name)) {
			return s.save(ctx, d.ID, in)
		}
	}
	return s.Create(ctx, in)
}
