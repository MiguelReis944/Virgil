package gateway

import (
	"context"
	"encoding/json"
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

func TestMessagesNativeForward(t *testing.T) {
	var path, key, version, beta string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key, version, beta = r.URL.String(), r.Header.Get("x-api-key"), r.Header.Get("anthropic-version"), r.Header.Get("anthropic-beta")
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","model":"fixture-model","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":3,"output_tokens":2}}`))
	}))
	defer upstream.Close()
	db, err := storage.Open(t.TempDir() + "/virgil.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"anthropic": {Type: "anthropic", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true}}}
	h, err := NewServer(cfg, Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", strings.NewReader(`{"model":"fixture-model","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("x-api-key", "synthetic-key")
	req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || path != "/v1/messages?beta=true" || key != "synthetic-key" || version == "" || beta == "" {
		t.Fatalf("status=%d path=%q key=%q version=%q beta=%q body=%s", rec.Code, path, key, version, beta, rec.Body.String())
	}
}

func TestMessagesPolicyBlocksBeforeProviderAndRecordsUsage(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","model":"fixture-model","content":[],"usage":{"input_tokens":3,"output_tokens":2,"cache_read_input_tokens":4}}`))
	}))
	defer upstream.Close()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	j, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	engine, err := policies.NewEngine(j, policies.Limits{MaxCallsPerRun: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"anthropic": {Type: "anthropic", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true}}}
	h, err := NewServer(cfg, Dependencies{DB: db, Recorder: j, Policy: engine, InstallationID: j.InstallationID()})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"fixture-model","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"Read","input_schema":{"type":"object"}}]}`))
		req.Header.Set("x-api-key", "synthetic-key")
		req.Header.Set("X-Virgil-Run-ID", "run_messages")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if i == 0 && rec.Code != 200 {
			t.Fatalf("first status=%d %s", rec.Code, rec.Body.String())
		}
		if i == 1 && (rec.Code != 403 || !strings.Contains(rec.Body.String(), "call_limit")) {
			t.Fatalf("blocked status=%d %s", rec.Code, rec.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls=%d", calls.Load())
	}
	var payload []byte
	if err := db.QueryRowContext(context.Background(), "SELECT payload FROM events WHERE status='success' LIMIT 1").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var event struct {
		InputTokens  *int64 `json:"input_tokens"`
		OutputTokens *int64 `json:"output_tokens"`
		CachedTokens *int64 `json:"cached_tokens"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.InputTokens == nil || *event.InputTokens != 7 || event.OutputTokens == nil || *event.OutputTokens != 2 || event.CachedTokens == nil || *event.CachedTokens != 4 {
		t.Fatalf("usage=%s", payload)
	}
}

func TestMessagesStreamTracksUsageAndPreservesPing(t *testing.T) {
	stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"fixture-model\",\"usage\":{\"input_tokens\":3,\"cache_read_input_tokens\":4}}}\n\n" +
		"event: ping\ndata: {\"type\":\"ping\"}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	rec := httptest.NewRecorder()
	result, err := streamMessages(context.Background(), strings.NewReader(stream), rec)
	if err != nil || result.Status != "success" || result.InputTokens == nil || *result.InputTokens != 7 || result.OutputTokens == nil || *result.OutputTokens != 2 || result.CachedTokens == nil || *result.CachedTokens != 4 || !strings.Contains(rec.Body.String(), "event: ping") {
		t.Fatalf("result=%+v err=%v body=%s", result, err, rec.Body.String())
	}
}

func TestMessagesRejectServerToolBeforeProvider(t *testing.T) {
	_, _, err := parseMessagesRequest([]byte(`{"model":"fixture-model","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"web_search_20250305","name":"web_search"}]}`))
	if err == nil {
		t.Fatal("server tool accepted")
	}
}

func TestMessagesDesktopTokenUsesConfiguredProviderKey(t *testing.T) {
	var seenKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get("x-api-key")
		_, _ = w.Write([]byte(`{"model":"fixture-model","content":[],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"anthropic": {Type: "anthropic", BaseURL: upstream.URL + "/v1", Model: "fixture-model", APIKeyEnv: "TEST_ANTHROPIC_KEY", Local: true}}}
	getenv := func(name string) string {
		switch name {
		case "VIRGIL_DESKTOP_TOKEN":
			return strings.Repeat("d", 32)
		case "TEST_ANTHROPIC_KEY":
			return "provider-secret"
		}
		return ""
	}
	h, err := NewServer(cfg, Dependencies{DB: db, Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"fixture-model","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", strings.Repeat("d", 32))
	req.Header.Set("x-claude-code-session-id", "session-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || seenKey != "provider-secret" || !strings.HasPrefix(rec.Header().Get("X-Virgil-Run-ID"), "desktop_claude_") {
		t.Fatalf("status=%d key=%q run=%q body=%s", rec.Code, seenKey, rec.Header().Get("X-Virgil-Run-ID"), rec.Body.String())
	}
}
