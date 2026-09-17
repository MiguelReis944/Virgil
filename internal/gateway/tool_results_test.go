package gateway

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func toolResultServer(t *testing.T, token, upstreamURL string) (http.Handler, *sql.DB) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(journal.Close)
	engine, err := policies.NewEngine(journal, policies.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstreamURL + "/v1", Model: "fixture-model"},
	}}
	handler, err := NewServer(cfg, Dependencies{
		DB: db, Recorder: journal, InstallationID: journal.InstallationID(), Policy: engine,
		Getenv: func(name string) string {
			if name == "VIRGIL_LOCAL_APP_TOKEN" {
				return token
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, db
}

func postToolResult(handler http.Handler, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/tool-results", strings.NewReader(body))
	if token != "" {
		req.Header.Set("X-Virgil-App-Token", token)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func TestToolResultsDisabledWithoutToken(t *testing.T) {
	handler, _ := toolResultServer(t, "", "http://127.0.0.1:1")
	resp := postToolResult(handler, "", `{"run_id":"run_fixture","tool_call_id":"call_1","tool_name":"lookup","status":"error","error_code":"timeout"}`)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestToolResultsRejectsWrongTokenAndContent(t *testing.T) {
	handler, _ := toolResultServer(t, "synthetic-local-token", "http://127.0.0.1:1")
	valid := `{"run_id":"run_fixture","tool_call_id":"call_1","tool_name":"lookup","status":"error","error_code":"timeout"}`
	if resp := postToolResult(handler, "wrong", valid); resp.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status=%d", resp.Code)
	}
	withContent := `{"run_id":"run_fixture","tool_call_id":"call_1","tool_name":"lookup","status":"error","error_code":"timeout","result":"SYNTHETIC_CANARY"}`
	if resp := postToolResult(handler, "synthetic-local-token", withContent); resp.Code != http.StatusBadRequest {
		t.Fatalf("content status=%d body=%s", resp.Code, resp.Body.String())
	}
	for _, runID := range []string{"run:unreachable", strings.Repeat("a", 65)} {
		body := `{"run_id":"` + runID + `","tool_call_id":"call_1","tool_name":"lookup","status":"error","error_code":"timeout"}`
		if resp := postToolResult(handler, "synthetic-local-token", body); resp.Code != http.StatusBadRequest {
			t.Fatalf("invalid run ID %q status=%d", runID, resp.Code)
		}
	}
}

func TestToolResultsBlockFourthProviderRequest(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"synthetic","model":"fixture-model","choices":[]}`))
	}))
	defer upstream.Close()
	handler, db := toolResultServer(t, "synthetic-local-token", upstream.URL)
	for i := range 3 {
		body := `{"run_id":"run_fixture","tool_call_id":"call_` + string(rune('a'+i)) + `","tool_name":"lookup","status":"error","error_code":"timeout"}`
		if resp := postToolResult(handler, "synthetic-local-token", body); resp.Code != http.StatusNoContent {
			t.Fatalf("feedback %d status=%d body=%s", i, resp.Code, resp.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatFixture(t)))
	req.Header.Set("Authorization", "Bearer synthetic-provider-key")
	req.Header.Set("X-Virgil-Run-ID", "run_fixture")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), "repeated_tool_error") || calls.Load() != 0 {
		t.Fatalf("fourth status=%d calls=%d body=%s", resp.Code, calls.Load(), resp.Body.String())
	}
	var blocks int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE status='policy_block'").Scan(&blocks); err != nil {
		t.Fatal(err)
	}
	if blocks != 1 {
		t.Fatalf("block events=%d", blocks)
	}
}

func TestRepeatedProviderErrorsBlockFourth(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"SYNTHETIC_CANARY"}`))
	}))
	defer upstream.Close()
	handler, db := toolResultServer(t, "synthetic-local-token", upstream.URL)
	for i := range 4 {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatFixture(t)))
		req.Header.Set("Authorization", "Bearer synthetic-provider-key")
		req.Header.Set("X-Virgil-Run-ID", "run_provider_errors")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		want := http.StatusBadGateway
		if i == 3 {
			want = http.StatusForbidden
		}
		if resp.Code != want || strings.Contains(resp.Body.String(), "SYNTHETIC_CANARY") {
			t.Fatalf("attempt %d status=%d body=%s", i+1, resp.Code, resp.Body.String())
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("provider calls=%d", calls.Load())
	}
	var blocks int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE status='policy_block'").Scan(&blocks); err != nil {
		t.Fatal(err)
	}
	if blocks != 1 {
		t.Fatalf("block events=%d", blocks)
	}
}

func TestRepeatedProviderToolCallsBlockFourth(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"fixture-model","choices":[{"message":{"tool_calls":[{"id":"call_synthetic","type":"function","function":{"name":"lookup","arguments":"{\"value\":\"SYNTHETIC_CANARY\"}"}}]}}]}`))
	}))
	defer upstream.Close()
	handler, db := toolResultServer(t, "synthetic-local-token", upstream.URL)
	for i := range 4 {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatFixture(t)))
		req.Header.Set("Authorization", "Bearer synthetic-provider-key")
		req.Header.Set("X-Virgil-Run-ID", "run_provider_calls")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusForbidden
		}
		if resp.Code != want {
			t.Fatalf("attempt %d status=%d body=%s", i+1, resp.Code, resp.Body.String())
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("provider calls=%d", calls.Load())
	}
	rows, err := db.Query("SELECT payload FROM events")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(payload, "SYNTHETIC_CANARY") {
			t.Fatal("tool argument persisted in event")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
