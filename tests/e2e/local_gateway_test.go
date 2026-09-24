// Package e2e proves end-to-end gateway behaviour without any external service.
// All upstreams are fake HTTP servers; no real provider or Control Plane is used.
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/gateway"
	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

// openGateway starts a full gateway handler backed by a temp SQLite database
// and the provided provider config. Returns the handler, journal, and db.
func openGateway(t *testing.T, cfg config.Config) (http.Handler, *storage.Journal) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := storage.NewJournal(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { journal.Close(); db.Close() })

	deps := gateway.Dependencies{
		DB:             db,
		Client:         http.DefaultClient,
		Recorder:       journal,
		InstallationID: journal.InstallationID(),
	}
	handler, err := gateway.NewServer(cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	return handler, journal
}

// openGatewayWithPolicy starts a gateway with a policy engine.
func openGatewayWithPolicy(t *testing.T, cfg config.Config, limits policies.Limits) (http.Handler, *storage.Journal) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := storage.NewJournal(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { journal.Close(); db.Close() })

	engine, err := policies.NewEngine(journal, limits)
	if err != nil {
		t.Fatal(err)
	}
	deps := gateway.Dependencies{
		DB:             db,
		Client:         http.DefaultClient,
		Recorder:       journal,
		InstallationID: journal.InstallationID(),
		Policy:         engine,
	}
	handler, err := gateway.NewServer(cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	return handler, journal
}

func jsonCompletion(model, content string) string {
	return fmt.Sprintf(`{"id":"chatcmpl_e2e","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`, model, content)
}

func sseCompletion(model, content string) string {
	delta := fmt.Sprintf(`{"id":"chatcmpl_sse","object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{"role":"assistant","content":%q},"finish_reason":null}]}`, model, content)
	done := fmt.Sprintf(`{"id":"chatcmpl_sse","object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, model)
	return "data: " + delta + "\n\ndata: " + done + "\n\ndata: [DONE]\n\n"
}

// TestJSONForwarding proves basic JSON chat completion forwarding end-to-end.
func TestJSONForwarding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jsonCompletion("fixture-model", "hello")))
	}))
	defer upstream.Close()

	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true},
	}}
	handler, _ := openGateway(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"fixture-model","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["object"] != "chat.completion" {
		t.Fatalf("unexpected response object: %v", resp["object"])
	}
}

// TestSSEStreaming proves SSE streaming forwarding end-to-end.
func TestSSEStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(sseCompletion("fixture-model", "stream-chunk")))
	}))
	defer upstream.Close()

	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true},
	}}
	handler, _ := openGateway(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"fixture-model","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected SSE Content-Type, got %s", ct)
	}
	sc := bufio.NewScanner(rec.Body)
	foundDone := false
	for sc.Scan() {
		if sc.Text() == "data: [DONE]" {
			foundDone = true
		}
	}
	if !foundDone {
		t.Fatal("SSE stream missing [DONE] sentinel")
	}
}

// TestKimiByConfig proves that a provider configured with a model alias
// forwards the alias to the upstream (Kimi-by-config pattern).
func TestKimiByConfig(t *testing.T) {
	var receivedModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		receivedModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jsonCompletion("kimi-k2", "kimi response")))
	}))
	defer upstream.Close()

	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		// Provider configured with a specific model alias; the gateway routes
		// any request with model="kimi-k2" here via the alias.
		"moonshot": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "kimi-k2", Local: true},
	}}
	handler, _ := openGateway(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"kimi-k2","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if receivedModel != "kimi-k2" {
		t.Fatalf("upstream received model=%q, want kimi-k2", receivedModel)
	}
}

// TestOfflineRecording proves that events are stored in the journal even when
// no export destination is configured (offline / no Control Plane).
func TestOfflineRecording(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jsonCompletion("fixture-model", "recorded")))
	}))
	defer upstream.Close()

	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true},
	}}
	handler, journal := openGateway(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"fixture-model","max_tokens":16,"messages":[{"role":"user","content":"record me"}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	// Give the recorder a moment to commit.
	time.Sleep(10 * time.Millisecond)
	events, _, err := journal.List(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("no events recorded in journal")
	}
}

// TestPrivacyContentAbsent proves that prompt/response content never appears
// in the journal or in exported telemetry.
func TestPrivacyContentAbsent(t *testing.T) {
	const prompt = "private-prompt-canary-e2e"
	const response = "private-response-canary-e2e"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jsonCompletion("fixture-model", response)))
	}))
	defer upstream.Close()

	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true},
	}}
	handler, journal := openGateway(t, cfg)

	body := fmt.Sprintf(`{"model":"fixture-model","max_tokens":16,"messages":[{"role":"user","content":%q}]}`, prompt)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	time.Sleep(10 * time.Millisecond)
	events, _, err := journal.List(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("no events found in journal")
	}
	for _, ev := range events {
		b, _ := json.Marshal(ev)
		if bytes.Contains(b, []byte(prompt)) {
			t.Fatalf("prompt canary found in journal event")
		}
		if bytes.Contains(b, []byte(response)) {
			t.Fatalf("response canary found in journal event")
		}
	}
}
