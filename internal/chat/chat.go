// Package chat implements chat sessions with streamed, measured responses.
package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/metalgeekhub/llmbench/internal/metrics"
	"github.com/metalgeekhub/llmbench/internal/providers"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
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
	Title    *string           `json:"title"`
	SourceID *string           `json:"source_id"`
	Model    *string           `json:"model"`
	Params   *store.ChatParams `json:"params"`
}

func (in SessionInput) applyTo(s *store.ChatSession) {
	if in.Title != nil {
		s.Title = strings.TrimSpace(*in.Title)
	}
	if in.SourceID != nil {
		s.SourceID = *in.SourceID
	}
	if in.Model != nil {
		s.Model = *in.Model
	}
	if in.Params != nil {
		s.Params = *in.Params
	}
}

func (s *Service) CreateSession(ctx context.Context, in SessionInput) (store.ChatSession, error) {
	sess := store.ChatSession{ID: uuid.NewString()}
	in.applyTo(&sess)
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
	in.applyTo(&sess)
	if err := s.store.UpdateSession(ctx, &sess); err != nil {
		return store.ChatSession{}, err
	}
	return sess, nil
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
	Content  string            `json:"content"`
	SourceID string            `json:"source_id"`
	Model    string            `json:"model"`
	Params   *store.ChatParams `json:"params"`
}

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
	if content == "" {
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
	if sess.SourceID == "" || sess.Model == "" {
		return fmt.Errorf("%w: select a source and model first", ErrInvalid)
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
	userMsg := store.ChatMessage{ID: uuid.NewString(), SessionID: sessionID, Role: "user", Content: content}
	if err := s.store.AddMessage(ctx, &userMsg); err != nil {
		return err
	}
	if sess.Title == "" {
		sess.Title = truncate(content, titleLength)
	}
	emit(EventUserMessage, MessageView{ChatMessage: userMsg})

	req := providers.ChatRequest{
		Model:       sess.Model,
		Messages:    buildMessages(sess.Params.SystemPrompt, history, content),
		Temperature: sess.Params.Temperature,
		MaxTokens:   sess.Params.MaxTokens,
		ExtraBody:   sess.Params.ExtraBody,
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
		Params:     requestParams(sess.Params),
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

func requestParams(p store.ChatParams) map[string]any {
	out := map[string]any{}
	if p.Temperature != nil {
		out["temperature"] = *p.Temperature
	}
	if p.MaxTokens != nil {
		out["max_tokens"] = *p.MaxTokens
	}
	if p.SystemPrompt != "" {
		out["system_prompt"] = p.SystemPrompt
	}
	if len(p.ExtraBody) > 0 {
		out["extra_body"] = p.ExtraBody
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}
