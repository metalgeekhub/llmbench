package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestProfilesCRUD(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()

	maxTok := 512
	p := &Profile{ID: "p1", Name: "Qwen thinking-high", SourceID: "env-a", Model: "qwen3",
		Params: ChatParams{MaxTokens: &maxTok, Thinking: "high", ThinkingStyle: "chat_template_kwargs"}}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProfile(ctx, &Profile{ID: "p2", Name: "Qwen thinking-high", SourceID: "x", Model: "y"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate name err = %v", err)
	}
	got, err := s.GetProfile(ctx, "p1")
	if err != nil || got.Params.Thinking != "high" || *got.Params.MaxTokens != 512 || got.Params.ThinkingStyle != "chat_template_kwargs" {
		t.Fatalf("profile = %+v, %v", got, err)
	}
	got.Name = "Qwen no-thinking"
	got.Params.Thinking = "off"
	if err := s.UpdateProfile(ctx, &got); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListProfiles(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "Qwen no-thinking" || list[0].Params.Thinking != "off" {
		t.Errorf("list = %+v, %v", list, err)
	}
	if err := s.DeleteProfile(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProfile(ctx, "p1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get deleted err = %v", err)
	}
}

func TestCompareGroups(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()

	mk := func(id, compare, title string) {
		t.Helper()
		if err := s.CreateSession(ctx, &ChatSession{ID: id, CompareID: compare, Title: title, ProfileID: "p-" + id}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct timestamps for ordering
	}
	mk("plain", "", "just chatting")
	mk("a1", "cmpA", "hello")
	mk("a2", "cmpA", "hello")
	mk("b1", "cmpB", "second")

	plain, err := s.ListSessions(ctx, "")
	if err != nil || len(plain) != 1 || plain[0].ID != "plain" {
		t.Errorf("plain chats = %+v, %v", plain, err)
	}
	cols, err := s.ListSessions(ctx, "cmpA")
	if err != nil || len(cols) != 2 || cols[0].ID != "a1" || cols[1].ID != "a2" || cols[0].ProfileID != "p-a1" {
		t.Errorf("compare columns = %+v, %v", cols, err)
	}

	groups, err := s.ListCompares(ctx)
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	// Most recently updated first.
	if groups[0].ID != "cmpB" || groups[1].ID != "cmpA" || len(groups[1].Sessions) != 2 || groups[1].Title != "hello" {
		t.Errorf("groups = %+v", groups)
	}

	if err := s.DeleteCompare(ctx, "cmpA"); err != nil {
		t.Fatal(err)
	}
	if cols, _ := s.ListSessions(ctx, "cmpA"); len(cols) != 0 {
		t.Errorf("compare sessions left: %d", len(cols))
	}
	if err := s.DeleteCompare(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty compare id err = %v", err)
	}
	if plain, _ := s.ListSessions(ctx, ""); len(plain) != 1 {
		t.Error("deleting a comparison must not touch plain chats")
	}
}

func TestRunTimelinePersisted(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	run := &TestRun{ID: "r", Type: "single", Status: StatusRunning}
	if err := s.CreateRun(ctx, run, []RunCell{{ID: "c", Index: 0, SourceID: "s", SourceName: "s", Model: "m",
		ProfileID: "p1", ProfileName: "fast", Concurrency: 1, Status: StatusPending}}); err != nil {
		t.Fatal(err)
	}
	run.Status = StatusCompleted
	run.Timeline = json.RawMessage(`[{"t":1}]`)
	if err := s.UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	// A later update without a timeline keeps it.
	run.Timeline = nil
	run.Name = "renamed"
	if err := s.UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRun(ctx, "r")
	if string(got.Timeline) != `[{"t":1}]` || got.Name != "renamed" {
		t.Errorf("run = %+v timeline=%s", got, got.Timeline)
	}
	list, _ := s.ListRuns(ctx, 10)
	if len(list) != 1 || list[0].Timeline != nil {
		t.Errorf("ListRuns should omit the timeline: %s", list[0].Timeline)
	}
	cells, _ := s.ListCells(ctx, "r")
	if cells[0].ProfileID != "p1" || cells[0].ProfileName != "fast" {
		t.Errorf("cell profile = %+v", cells[0])
	}
}

func TestMatrixCellsAndComparisons(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	rate, lag := 5.5, 12.0
	run := &TestRun{ID: "r", Type: "matrix", Status: StatusRunning}
	cell := RunCell{ID: "c", Index: 0, SourceID: "s", SourceName: "s", Model: "m", Concurrency: 64, Status: StatusPending,
		ContextTokens: 4096, Thinking: "high", ArrivalRate: &rate}
	if err := s.CreateRun(ctx, run, []RunCell{cell}); err != nil {
		t.Fatal(err)
	}
	cell.RunID = "r"
	cell.ScheduleLagP95Ms = &lag
	cell.SampleOutput = "answer"
	cell.SampleReasoning = "thoughts"
	if err := s.UpdateCell(ctx, &cell); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListCells(ctx, "r")
	c := got[0]
	if c.ContextTokens != 4096 || c.Thinking != "high" || *c.ArrivalRate != 5.5 || *c.ScheduleLagP95Ms != 12 ||
		c.SampleOutput != "answer" || c.SampleReasoning != "thoughts" {
		t.Errorf("cell = %+v", c)
	}

	cmp := &Comparison{ID: "cmp", Name: "A vs B", RunIDs: []string{"r1", "r2"}, Settings: json.RawMessage(`{"x":"load"}`)}
	if err := s.CreateComparison(ctx, cmp); err != nil {
		t.Fatal(err)
	}
	cmp.Name = "renamed"
	if err := s.UpdateComparison(ctx, cmp); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListComparisons(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "renamed" || len(list[0].RunIDs) != 2 || string(list[0].Settings) != `{"x":"load"}` {
		t.Errorf("comparisons = %+v, %v", list, err)
	}
	if err := s.DeleteComparison(ctx, "cmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetComparison(ctx, "cmp"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get deleted err = %v", err)
	}
}
