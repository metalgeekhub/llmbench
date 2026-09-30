// Package profiles manages model profiles: a model on a source plus
// default parameters (thinking, temperature, max tokens, system prompt,
// extra body). Profiles can be chatted with, compared, and benchmarked like
// different models.
package profiles

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
)

const maxNameLength = 64

// ValidationError reports invalid input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Input creates or updates a profile.
type Input struct {
	Name     string           `json:"name"`
	SourceID string           `json:"source_id"`
	Model    string           `json:"model"`
	Params   store.ChatParams `json:"params"`
}

type Service struct {
	store   store.Store
	sources *sources.Manager
}

func NewService(st store.Store, src *sources.Manager) *Service {
	return &Service{store: st, sources: src}
}

func (s *Service) List(ctx context.Context) ([]store.Profile, error) {
	return s.store.ListProfiles(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (store.Profile, error) {
	return s.store.GetProfile(ctx, id)
}

func (s *Service) Create(ctx context.Context, in Input) (store.Profile, error) {
	p := store.Profile{ID: uuid.NewString()}
	if err := s.apply(ctx, &p, in); err != nil {
		return store.Profile{}, err
	}
	if err := s.store.CreateProfile(ctx, &p); err != nil {
		return store.Profile{}, conflict(err, p.Name)
	}
	return p, nil
}

func (s *Service) Update(ctx context.Context, id string, in Input) (store.Profile, error) {
	p, err := s.store.GetProfile(ctx, id)
	if err != nil {
		return store.Profile{}, err
	}
	if err := s.apply(ctx, &p, in); err != nil {
		return store.Profile{}, err
	}
	if err := s.store.UpdateProfile(ctx, &p); err != nil {
		return store.Profile{}, conflict(err, p.Name)
	}
	return p, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.DeleteProfile(ctx, id)
}

func conflict(err error, name string) error {
	if errors.Is(err, store.ErrConflict) {
		return invalid("a profile named %q already exists", name)
	}
	return err
}

func (s *Service) apply(ctx context.Context, p *store.Profile, in Input) error {
	name := strings.TrimSpace(in.Name)
	model := strings.TrimSpace(in.Model)
	switch {
	case name == "":
		return invalid("name is required")
	case len(name) > maxNameLength:
		return invalid("name must be at most %d characters", maxNameLength)
	case model == "":
		return invalid("model is required")
	}
	if _, err := s.sources.Get(ctx, in.SourceID); err != nil {
		if errors.Is(err, sources.ErrNotFound) {
			return invalid("unknown source %q", in.SourceID)
		}
		return err
	}
	if err := in.Params.Validate(); err != nil {
		return invalid("%s", err.Error())
	}
	p.Name = name
	p.SourceID = in.SourceID
	p.Model = model
	p.Params = in.Params
	return nil
}
