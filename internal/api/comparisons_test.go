package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/metalgeekhub/llmbench/internal/store"
)

func TestComparisonsAPI(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	for _, id := range []string{"r1", "r2", "r3"} {
		if err := e.st.CreateRun(ctx, &store.TestRun{ID: id, Type: "single", Status: store.StatusCompleted}, nil); err != nil {
			t.Fatal(err)
		}
	}

	var c store.Comparison
	code := e.do(t, "POST", "/api/v1/comparisons", map[string]any{
		"name": "vLLM vs SGLang", "run_ids": []string{"r1", "r2"}, "settings": map[string]any{"metric": "ttft_ms"},
	}, &c)
	if code != http.StatusCreated || c.ID == "" || len(c.RunIDs) != 2 {
		t.Fatalf("create: %d %+v", code, c)
	}
	var settings map[string]any
	_ = json.Unmarshal(c.Settings, &settings)
	if settings["metric"] != "ttft_ms" {
		t.Errorf("settings = %s", c.Settings)
	}

	var upd store.Comparison
	if code := e.do(t, "PUT", "/api/v1/comparisons/"+c.ID, map[string]any{"name": "three-way", "run_ids": []string{"r1", "r2", "r3"}}, &upd); code != 200 || len(upd.RunIDs) != 3 {
		t.Errorf("update: %d %+v", code, upd)
	}

	var list struct{ Comparisons []store.Comparison }
	e.do(t, "GET", "/api/v1/comparisons", nil, &list)
	if len(list.Comparisons) != 1 || list.Comparisons[0].Name != "three-way" {
		t.Errorf("list = %+v", list)
	}

	for name, body := range map[string]map[string]any{
		"no name":     {"name": " ", "run_ids": []string{"r1", "r2"}},
		"one run":     {"name": "x", "run_ids": []string{"r1"}},
		"unknown run": {"name": "x", "run_ids": []string{"r1", "nope"}},
		"duplicate":   {"name": "x", "run_ids": []string{"r1", "r1"}},
	} {
		var apiErr struct{ Error string }
		if code := e.do(t, "POST", "/api/v1/comparisons", body, &apiErr); code != http.StatusBadRequest || apiErr.Error == "" {
			t.Errorf("%s: %d %+v", name, code, apiErr)
		}
	}

	if code := e.do(t, "DELETE", "/api/v1/comparisons/"+c.ID, nil, nil); code != http.StatusNoContent {
		t.Errorf("delete status = %d", code)
	}
	if code := e.do(t, "GET", "/api/v1/comparisons/"+c.ID, nil, nil); code != http.StatusNotFound {
		t.Errorf("get deleted status = %d", code)
	}
}
