package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/metalgeekhub/llmbench/internal/metrics"
)

func openTest(t *testing.T) (*SQLite, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := OpenSQLite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestSourcesCRUD(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()

	src := &Source{
		ID: "s1", Name: "local", Type: "openai", BaseURL: "http://x/v1", APIKeyEnc: "v1:abc",
		ExtraBody: map[string]any{"top_k": 5.0}, Timeout: 30 * time.Second, Models: []string{"a", "b"},
	}
	if err := s.CreateSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSource(ctx, &Source{ID: "s2", Name: "local", Type: "openai", BaseURL: "x"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate name err = %v", err)
	}

	got, err := s.GetSource(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "local" || got.APIKeyEnc != "v1:abc" || got.Timeout != 30*time.Second ||
		!reflect.DeepEqual(got.Models, []string{"a", "b"}) || got.ExtraBody["top_k"] != 5.0 {
		t.Errorf("GetSource = %+v", got)
	}

	got.ModelsAuto = true
	got.Models = nil
	if err := s.UpdateSource(ctx, &got); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListSources(ctx)
	if err != nil || len(list) != 1 || !list[0].ModelsAuto || len(list[0].Models) != 0 {
		t.Errorf("ListSources = %+v, %v", list, err)
	}

	if err := s.DeleteSource(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSource(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete err = %v", err)
	}
	if err := s.DeleteSource(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete err = %v", err)
	}
}

func TestChatAndRequestsPersistAcrossReopen(t *testing.T) {
	s, path := openTest(t)
	ctx := context.Background()

	temp := 0.7
	sess := &ChatSession{ID: "c1", Title: "hello", SourceID: "env-local", Model: "m",
		Params: ChatParams{Temperature: &temp, SystemPrompt: "be brief"}}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(ctx, &ChatMessage{ID: "m1", SessionID: "c1", Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	ttft, tpot := 120.5, 15.25
	req := &Request{
		ID: "r1", Kind: KindChat, SourceID: "env-local", SourceName: "local", Model: "m", SessionID: "c1",
		Status: "ok", StartedAt: time.Now(),
		Metrics: metrics.Result{TTFTMs: &ttft, E2EMs: 900, TPOTMs: &tpot, InputTokens: 10, OutputTokens: 50,
			TokensEstimated: true, ChunkCount: 3, ITL: metrics.Summary{Count: 2, P50: 12}},
		Params: map[string]any{"temperature": 0.7},
	}
	tl := &Timeline{OffsetsMs: []float64{120.5, 130, 142}, Kinds: "rcc"}
	if err := s.SaveRequest(ctx, req, tl); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(ctx, &ChatMessage{ID: "m2", SessionID: "c1", Role: "assistant", Content: "hello!", RequestID: "r1"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen: data must survive, migrations must not re-run.
	s2, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	sessions, err := s2.ListSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].Params.SystemPrompt != "be brief" || *sessions[0].Params.Temperature != 0.7 {
		t.Fatalf("sessions = %+v, %v", sessions, err)
	}
	msgs, err := s2.ListMessages(ctx, "c1")
	if err != nil || len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].RequestID != "r1" {
		t.Fatalf("messages = %+v, %v", msgs, err)
	}

	got, err := s2.GetRequest(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	m := got.Metrics
	if *m.TTFTMs != 120.5 || *m.TPOTMs != 15.25 || m.TTFATMs != nil || m.OutputTPS != nil ||
		m.E2EMs != 900 || !m.TokensEstimated || m.ITL.P50 != 12 || got.Params["temperature"] != 0.7 {
		t.Errorf("request = %+v metrics = %+v", got, m)
	}

	gotTL, err := s2.GetTimeline(ctx, "r1")
	if err != nil || !reflect.DeepEqual(gotTL, tl) {
		t.Errorf("timeline = %+v, %v", gotTL, err)
	}

	list, total, err := s2.ListRequests(ctx, RequestFilter{Model: "m"})
	if err != nil || total != 1 || len(list) != 1 {
		t.Errorf("ListRequests = %d/%d, %v", len(list), total, err)
	}
	list, total, _ = s2.ListRequests(ctx, RequestFilter{Model: "other"})
	if total != 0 || len(list) != 0 {
		t.Errorf("filtered ListRequests = %d/%d", len(list), total)
	}

	// Deleting a session cascades to messages but keeps request history.
	if err := s2.DeleteSession(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := s2.ListMessages(ctx, "c1"); len(msgs) != 0 {
		t.Error("messages should be deleted with session")
	}
	if _, err := s2.GetRequest(ctx, "r1"); err != nil {
		t.Errorf("request should survive session delete: %v", err)
	}
}
