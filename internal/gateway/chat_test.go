package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func chatFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "chat_request.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPolicyBlockDoesNotCallProvider(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_fixture","object":"chat.completion","model":"fixture-model","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	engine, err := policies.NewEngine(journal, policies.Limits{MaxCallsPerRun: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model"},
	}}
	handler, err := NewServer(cfg, Dependencies{DB: db, Client: http.DefaultClient, Recorder: journal, InstallationID: journal.InstallationID(), Policy: engine})
	if err != nil {
		t.Fatal(err)
	}
	var runID string
	for i := range 2 {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatFixture(t)))
		req.Header.Set("Authorization", "Bearer synthetic-key")
		req.Header.Set("X-Virgil-Run-ID", "invalid run id")
		if i == 1 {
			req.Header.Set("X-Virgil-Run-ID", runID)
		}
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if i == 0 {
			if resp.Code != 200 {
				t.Fatalf("first status=%d: %s", resp.Code, resp.Body.String())
			}
			// The generated run ID is returned for subsequent calls.
			runID = resp.Header().Get("X-Virgil-Run-ID")
			if !strings.HasPrefix(runID, "run_") {
				t.Fatalf("run ID=%q", runID)
			}
		} else {
			if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), `"decision":"block"`) || !strings.Contains(resp.Body.String(), `"reason":"call_limit"`) {
				t.Fatalf("block status=%d body=%s", resp.Code, resp.Body.String())
			}
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls=%d", calls.Load())
	}
}

func TestCostUnavailableBlocksAndJournals(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	engine, err := policies.NewEngine(journal, policies.Limits{MaxCostPerRunUSD: "1.00"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model"},
	}}
	handler, err := NewServer(cfg, Dependencies{DB: db, Recorder: journal, InstallationID: journal.InstallationID(), Policy: engine})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatFixture(t)))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), `"reason":"cost_unavailable"`) || calls.Load() != 0 {
		t.Fatalf("status=%d body=%s calls=%d", resp.Code, resp.Body.String(), calls.Load())
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE status='policy_block'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("block events=%d", count)
	}
}

func TestActualCostReconcilesBeforeNextRequest(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"fixture-model","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":0}}`))
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
	e, err := policies.NewEngine(j, policies.Limits{MaxCostPerRunUSD: "0.25"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Providers:  map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model"}},
		Guardrails: config.GuardrailsConfig{EstimatedCostPerCallUSD: "0.10"},
		Pricing:    config.PricingConfig{Version: "test", Currency: "USD", Models: map[string]config.ModelPriceEntry{"fixture-model": {InputPerToken: "0.10", OutputPerToken: "0"}}},
	}
	h, err := NewServer(cfg, Dependencies{DB: db, Policy: e})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatFixture(t)))
		req.Header.Set("Authorization", "Bearer synthetic-key")
		req.Header.Set("X-Virgil-Run-ID", "run_cost_actual")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if i == 0 && w.Code != http.StatusOK {
			t.Fatalf("first: %d %s", w.Code, w.Body.String())
		}
		if i == 1 && (w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "cost_limit")) {
			t.Fatalf("second: %d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls=%d", calls.Load())
	}
}

func chatServer(t *testing.T, upstreamURL string, getenv func(string) string) http.Handler {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"fixture": {Type: "openai-compatible", BaseURL: upstreamURL + "/v1", Model: "fixture-model", APIKeyEnv: "TEST_PROVIDER_KEY"},
		},
	}
	handler, err := NewServer(cfg, Dependencies{DB: db, Client: http.DefaultClient, Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestChatJSONForwarding(t *testing.T) {
	var seenBody []byte
	var seenKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		seenKey = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl_fixture","object":"chat.completion","model":"fixture-model","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_fixture","type":"function","function":{"name":"fixture_tool","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`))
	}))
	defer upstream.Close()
	handler := chatServer(t, upstream.URL, func(string) string { return "" })
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatFixture(t)))
	req.Header.Set("Authorization", "Bearer synthetic-pass-through")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(bytes.TrimSpace(seenBody), bytes.TrimSpace(chatFixture(t))) || seenKey != "Bearer synthetic-pass-through" {
		t.Fatalf("request not forwarded correctly: key=%q body=%s", seenKey, seenBody)
	}
	if !strings.Contains(rec.Body.String(), "call_fixture") {
		t.Fatalf("tool call missing: %s", rec.Body.String())
	}
}

func TestConfiguredKeyRequiresAppToken(t *testing.T) {
	var keys []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl_fixture","object":"chat.completion","model":"fixture-model","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0}}`))
	}))
	defer upstream.Close()
	getenv := func(name string) string {
		switch name {
		case "VIRGIL_LOCAL_APP_TOKEN":
			return "synthetic-local-token"
		case "TEST_PROVIDER_KEY":
			return "synthetic-configured-key"
		}
		return ""
	}
	handler := chatServer(t, upstream.URL, getenv)
	for _, tc := range []struct {
		bearer string
		status int
		key    string
	}{
		{"", 401, ""},
		{"synthetic-arbitrary-bearer", 200, "Bearer synthetic-arbitrary-bearer"},
		{"synthetic-local-token", 200, "Bearer synthetic-configured-key"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatFixture(t)))
		if tc.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("bearer %q: status=%d body=%s", tc.bearer, rec.Code, rec.Body.String())
		}
		if tc.key != "" && keys[len(keys)-1] != tc.key {
			t.Fatalf("bearer %q unlocked %q", tc.bearer, keys[len(keys)-1])
		}
	}
}

func TestUnsupportedFeatureDoesNotDispatch(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer upstream.Close()
	handler := chatServer(t, upstream.URL, func(string) string { return "" })
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}],"audio":{"format":"wav"}}`))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 400 || calls.Load() != 0 {
		t.Fatalf("status=%d provider calls=%d", rec.Code, calls.Load())
	}
}

func TestConfiguredCapabilityRejectsBeforeDispatch(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer upstream.Close()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"kimi": {Type: "kimi", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Capabilities: []string{}},
	}}
	handler, err := NewServer(cfg, Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}],"stream":true}`))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest || calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d", resp.Code, calls.Load())
	}
}

func TestKimiConfiguredEndpointAndModel(t *testing.T) {
	type seenRequest struct{ path, model string }
	seen := make(chan seenRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		seen <- seenRequest{path: r.URL.Path, model: request.Model}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"kimi-test-model","choices":[]}`))
	}))
	defer upstream.Close()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"kimi": {Type: "kimi", BaseURL: upstream.URL + "/v1", Model: "kimi-test-model"},
	}}
	handler, err := NewServer(cfg, Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"kimi-test-model","messages":[{"role":"user","content":"synthetic"}]}`))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	select {
	case got := <-seen:
		if got.path != "/v1/chat/completions" || got.model != "kimi-test-model" {
			t.Fatalf("path=%q model=%q", got.path, got.model)
		}
	default:
		t.Fatal("provider was not called")
	}
}

func TestAnthropicConfiguredRouteJSON(t *testing.T) {
	type seenRequest struct{ path, key, model string }
	seen := make(chan seenRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		seen <- seenRequest{path: r.URL.Path, key: r.Header.Get("x-api-key"), model: request.Model}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_synthetic","model":"fixture-model","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`))
	}))
	defer upstream.Close()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"anthropic": {Type: "anthropic", BaseURL: upstream.URL + "/v1", Model: "fixture-model"},
	}}
	handler, err := NewServer(cfg, Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}],"max_tokens":64}`))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"object":"chat.completion"`) {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	select {
	case got := <-seen:
		if got.path != "/v1/messages" || got.key != "synthetic-key" || got.model != "fixture-model" {
			t.Fatalf("request=%+v", got)
		}
	default:
		t.Fatal("provider was not called")
	}
}
