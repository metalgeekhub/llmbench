// Package chat implements chat sessions with streamed, measured responses.
package chat

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/metalgeekhub/llmbench/internal/metrics"
	"github.com/metalgeekhub/llmbench/internal/providers"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
	"github.com/metalgeekhub/llmbench/internal/tokenizer"
)

const titleLength = 60

// ErrInvalid wraps input validation failures.
var ErrInvalid = errors.New("invalid request")

// Service manages chat sessions.
type Service struct {
	store   store.Store
	sources *sources.Manager
}

func NewService(st store.Store, src *sources.Manager) *Service {
	return &Service{store: st, sources: src}
}

// SessionInput creates or updates a session. Nil/empty fields are left
// unchanged on update.
type SessionInput struct {
	Title     *string           `json:"title"`
	SourceID  *string           `json:"source_id"`
	Model     *string           `json:"model"`
	Params    *store.ChatParams `json:"params"`
	ProfileID *string           `json:"profile_id"`
	// CompareID is only honoured on create: it makes the session a column
	// of a side-by-side comparison.
	CompareID string `json:"compare_id"`
}

func (in SessionInput) applyTo(s *store.ChatSession) error {
	if in.Params != nil {
		if err := in.Params.Validate(); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
		}
		s.Params = *in.Params
	}
	if in.Title != nil {
		s.Title = strings.TrimSpace(*in.Title)
	}
	if in.SourceID != nil {
		s.SourceID = *in.SourceID
	}
	if in.Model != nil {
		s.Model = *in.Model
	}
	if in.ProfileID != nil {
		s.ProfileID = *in.ProfileID
	}
	return nil
}

func (s *Service) CreateSession(ctx context.Context, in SessionInput) (store.ChatSession, error) {
	sess := store.ChatSession{ID: uuid.NewString(), CompareID: strings.TrimSpace(in.CompareID)}
	if err := in.applyTo(&sess); err != nil {
		return store.ChatSession{}, err
	}
	if err := s.store.CreateSession(ctx, &sess); err != nil {
		return store.ChatSession{}, err
	}
	return sess, nil
}

func (s *Service) UpdateSession(ctx context.Context, id string, in SessionInput) (store.ChatSession, error) {
	sess, err := s.store.GetSession(ctx, id)
	if err != nil {
		return store.ChatSession{}, err
	}
	if err := in.applyTo(&sess); err != nil {
		return store.ChatSession{}, err
	}
	if err := s.store.UpdateSession(ctx, &sess); err != nil {
		return store.ChatSession{}, err
	}
	return sess, nil
}

// CompareDetail is a comparison with every column's full history.
type CompareDetail struct {
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Sessions []SessionDetail `json:"sessions"`
}

func (s *Service) GetCompare(ctx context.Context, compareID string) (CompareDetail, error) {
	sessions, err := s.store.ListSessions(ctx, compareID)
	if err != nil {
		return CompareDetail{}, err
	}
	if compareID == "" || len(sessions) == 0 {
		return CompareDetail{}, store.ErrNotFound
	}
	d := CompareDetail{ID: compareID, Sessions: make([]SessionDetail, 0, len(sessions))}
	for _, sess := range sessions {
		sd, err := s.GetSession(ctx, sess.ID)
		if err != nil {
			return CompareDetail{}, err
		}
		if d.Title == "" {
			d.Title = sd.Title
		}
		d.Sessions = append(d.Sessions, sd)
	}
	return d, nil
}

// MessageView is a chat message with the metrics of the request that
// produced it (assistant messages only).
type MessageView struct {
	store.ChatMessage
	Request *store.Request `json:"request"`
}

// SessionDetail is a session with its full message history.
type SessionDetail struct {
	store.ChatSession
	Messages []MessageView `json:"messages"`
}

func (s *Service) GetSession(ctx context.Context, id string) (SessionDetail, error) {
	sess, err := s.store.GetSession(ctx, id)
	if err != nil {
		return SessionDetail{}, err
	}
	msgs, err := s.store.ListMessages(ctx, id)
	if err != nil {
		return SessionDetail{}, err
	}
	reqs, _, err := s.store.ListRequests(ctx, store.RequestFilter{SessionID: id, Limit: 10000})
	if err != nil {
		return SessionDetail{}, err
	}
	byID := make(map[string]*store.Request, len(reqs))
	for i := range reqs {
		byID[reqs[i].ID] = &reqs[i]
	}
	views := make([]MessageView, len(msgs))
	for i, m := range msgs {
		views[i] = MessageView{ChatMessage: m, Request: byID[m.RequestID]}
	}
	return SessionDetail{ChatSession: sess, Messages: views}, nil
}

// SendInput is a new user message. SourceID, Model and Params, when set,
// update the session before sending.
type SendInput struct {
	Content   string            `json:"content"`
	SourceID  string            `json:"source_id"`
	Model     string            `json:"model"`
	Params    *store.ChatParams `json:"params"`
	ProfileID *string           `json:"profile_id"`
	// SyntheticTokens > 0 sends a synthetic prompt of that many tokens
	// instead of typed text; Content, if set, becomes its closing
	// instruction. Each request gets a unique prefix so prefix caching can't
	// favor one compare column over another.
	SyntheticTokens int `json:"synthetic_tokens"`
}

// Synthetic prompt limits for chat.
const (
	MinSyntheticTokens = 16
	MaxSyntheticTokens = 500_000
)

// Stream event names.
const (
	EventUserMessage = "user_message"
	EventDelta       = "delta"
	EventDone        = "done"
)

// Delta is a streamed piece of the response.
type Delta struct {
	Kind string `json:"kind"` // "content" or "reasoning"
	Text string `json:"text"`
}

// Done is sent once the response is complete (successfully or not).
type Done struct {
	Message MessageView `json:"message"`
}

// Send appends a user message to the session, streams the assistant
// response through emit, and persists the measured request. An error is
// returned only if the request could not be started; provider failures are
// reported in the Done event's request.
func (s *Service) Send(ctx context.Context, sessionID string, in SendInput, emit func(event string, data any)) error {
	content := strings.TrimSpace(in.Content)
	title := content
	switch n := in.SyntheticTokens; {
	case n > 0:
		if n < MinSyntheticTokens || n > MaxSyntheticTokens {
			return fmt.Errorf("%w: synthetic prompts must be %d to %d tokens", ErrInvalid, MinSyntheticTokens, MaxSyntheticTokens)
		}
		title = fmt.Sprintf("Synthetic prompt · %d tokens", n)
		content = fmt.Sprintf("[%016x] %s", rand.Uint64(), tokenizer.SyntheticPrompt(n, content))
	case n < 0:
		return fmt.Errorf("%w: synthetic token count must be positive", ErrInvalid)
	case content == "":
		return fmt.Errorf("%w: message content is empty", ErrInvalid)
	}
	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if in.SourceID != "" {
		sess.SourceID = in.SourceID
	}
	if in.Model != "" {
		sess.Model = in.Model
	}
	if in.Params != nil {
		sess.Params = *in.Params
	}
	if in.ProfileID != nil {
		sess.ProfileID = *in.ProfileID
	}
	if sess.SourceID == "" || sess.Model == "" {
		return fmt.Errorf("%w: select a source and model first", ErrInvalid)
	}
	thinking, err := sess.Params.ThinkingControl()
	if err == nil {
		err = sess.Params.Validate()
	}
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	src, err := s.sources.Get(ctx, sess.SourceID)
	if err != nil {
		return err
	}
	provider, err := s.sources.Provider(ctx, sess.SourceID)
	if err != nil {
		return err
	}

	history, err := s.store.ListMessages(ctx, sessionID)
	if err != nil {
		return err
	}
	userMsg := store.ChatMessage{ID: uuid.NewString(), SessionID: sessionID, Role: "user", Content: content,
		SyntheticTokens: in.SyntheticTokens}
	if err := s.store.AddMessage(ctx, &userMsg); err != nil {
		return err
	}
	if sess.Title == "" {
		sess.Title = truncate(title, titleLength)
	}
	emit(EventUserMessage, MessageView{ChatMessage: userMsg})

	req := providers.ChatRequest{
		Model:       sess.Model,
		Messages:    buildMessages(sess.Params.SystemPrompt, history, content),
		Temperature: sess.Params.Temperature,
		MaxTokens:   sess.Params.MaxTokens,
		ExtraBody:   sess.Params.ExtraBody,
		Thinking:    thinking,
	}
	m := metrics.Collect(ctx, provider, req, func(e providers.Event) {
		switch e.Type {
		case providers.EventContent:
			emit(EventDelta, Delta{Kind: "content", Text: e.Text})
		case providers.EventReasoning:
			emit(EventDelta, Delta{Kind: "reasoning", Text: e.Text})
		}
	})

	// Persist even if the client went away mid-stream.
	saveCtx := context.WithoutCancel(ctx)
	record := store.Request{
		ID:         uuid.NewString(),
		Kind:       store.KindChat,
		SourceID:   src.ID,
		SourceName: src.Name,
		Model:      sess.Model,
		SessionID:  sessionID,
		Status:     m.Status,
		StartedAt:  m.StartedAt,
		Metrics:    m.Result,
		Params:     sess.Params.Summary(),
	}
	if m.Err != nil {
		record.ErrorType = m.Err.Type
		record.ErrorMessage = m.Err.Message
		record.HTTPStatus = m.Err.HTTPStatus
	}
	if err := s.store.SaveRequest(saveCtx, &record, store.TimelineFromChunks(m.Chunks)); err != nil {
		return err
	}

	assistant := store.ChatMessage{
		ID:        uuid.NewString(),
		SessionID: sessionID,
		Role:      "assistant",
		Content:   m.Content,
		Reasoning: m.Reasoning,
		SourceID:  src.ID,
		Model:     sess.Model,
		RequestID: record.ID,
	}
	if err := s.store.AddMessage(saveCtx, &assistant); err != nil {
		return err
	}
	if err := s.store.UpdateSession(saveCtx, &sess); err != nil {
		return err
	}
	emit(EventDone, Done{Message: MessageView{ChatMessage: assistant, Request: &record}})
	return nil
}

// buildMessages assembles the conversation sent to the model. Assistant
// turns that produced no content (failed requests) are skipped.
func buildMessages(systemPrompt string, history []store.ChatMessage, next string) []providers.Message {
	msgs := make([]providers.Message, 0, len(history)+2)
	if sp := strings.TrimSpace(systemPrompt); sp != "" {
		msgs = append(msgs, providers.Message{Role: "system", Content: sp})
	}
	for _, h := range history {
		if h.Role == "assistant" && h.Content == "" {
			continue
		}
		msgs = append(msgs, providers.Message{Role: h.Role, Content: h.Content})
	}
	return append(msgs, providers.Message{Role: "user", Content: next})
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}
