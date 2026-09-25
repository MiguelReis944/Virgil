package gateway

import (
	"bytes"
	"encoding/json"
	"io"
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

func TestResponsesStreamUsageAndUnsupportedState(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\"}\n\n"))
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"fixture-model\",\"output\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":5}}}\n\n"))
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
	e, err := policies.NewEngine(j, policies.Limits{MaxCallsPerRun: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"openai": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true}}}
	h, err := NewServer(cfg, Dependencies{DB: db, Recorder: j, Policy: e, InstallationID: j.InstallationID()})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"model":"fixture-model","input":"hello","stream":true,"store":false}`,
		`{"model":"fixture-model","input":"hello","previous_response_id":"resp_hidden"}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer synthetic-key")
		r.Header.Set("X-Virgil-Run-ID", "run_stream_test")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if strings.Contains(body, "stream") {
			if w.Code != 200 || !strings.Contains(w.Body.String(), "response.completed") {
				t.Fatalf("stream: %d %s", w.Code, w.Body.String())
			}
		} else if w.Code != 400 {
			t.Fatalf("stateful: %d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls=%d", calls.Load())
	}
	var in, out int64
	var data []byte
	if err := db.QueryRow("SELECT payload FROM events WHERE status='success' LIMIT 1").Scan(&data); err == nil {
		var event struct {
			InputTokens  *int64 `json:"input_tokens"`
			OutputTokens *int64 `json:"output_tokens"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if event.InputTokens != nil {
			in = *event.InputTokens
		}
		if event.OutputTokens != nil {
			out = *event.OutputTokens
		}
		if in != 4 || out != 5 {
			t.Fatalf("usage=%d/%d", in, out)
		}
	}
}

func TestResponsesCodexToolShapesAndMetadataRedaction(t *testing.T) {
	const request = `{"model":"fixture-model","input":[{"role":"user","content":"hello"}],"stream":false,"client_metadata":{"session_id":"private-session"},"tools":[{"type":"function","name":"exec_command"},{"type":"namespace","name":"multi_agent_v1","tools":[{"name":"send_message"}]},{"type":"web_search"}]}`
	input, names, err := parseResponsesRequest([]byte(request))
	if err != nil || input.Model != "fixture-model" {
		t.Fatalf("parse: %v", err)
	}
	if got := strings.Join(names, ","); got != "exec_command,multi_agent_v1.send_message,web_search" {
		t.Fatalf("tools=%s", got)
	}
	var forwarded string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		forwarded = string(body)
		_, _ = w.Write([]byte(`{"status":"completed","model":"fixture-model","output":[]}`))
	}))
	defer upstream.Close()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"openai": {Type: "openai", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true}}}
	h, err := NewServer(cfg, Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(request))
	r.Header.Set("Authorization", "Bearer synthetic-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(forwarded, "private-session") || strings.Contains(forwarded, "client_metadata") {
		t.Fatal("client metadata leaked upstream")
	}
	if !strings.Contains(forwarded, "web_search") {
		t.Fatal("tools lost upstream")
	}
	if !strings.Contains(forwarded, `"store":false`) {
		t.Fatal("stateless default not enforced")
	}
}

func TestResponsesEstimatedCostRemainsCharged(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"status":"completed","model":"fixture-model","output":[],"usage":{"input_tokens":2,"output_tokens":2}}`))
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
	e, err := policies.NewEngine(j, policies.Limits{MaxCostPerRunUSD: "0.15"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"openai": {Type: "openai", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true}}, Guardrails: config.GuardrailsConfig{EstimatedCostPerCallUSD: "0.10"}}
	h, err := NewServer(cfg, Dependencies{DB: db, Recorder: j, Policy: e, InstallationID: j.InstallationID()})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		r := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"fixture-model","input":"hello","store":false}`))
		r.Header.Set("Authorization", "Bearer synthetic-key")
		r.Header.Set("X-Virgil-Run-ID", "run_responses_cost")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if i == 0 && w.Code != 200 {
			t.Fatalf("first: %d %s", w.Code, w.Body.String())
		}
		if i == 1 && (w.Code != 403 || !strings.Contains(w.Body.String(), "cost_limit")) {
			t.Fatalf("second: %d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls=%d", calls.Load())
	}
}

func TestResponsesPolicyBlocksBeforeUpstream(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer provider-key" {
			t.Errorf("upstream path/auth = %s/%s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","model":"fixture-model","output":[],"usage":{"input_tokens":2,"output_tokens":3}}`))
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
	e, err := policies.NewEngine(j, policies.Limits{MaxCallsPerRun: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"openai": {Type: "openai", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true, APIKeyEnv: "PROVIDER_KEY"}}}
	h, err := NewServer(cfg, Dependencies{DB: db, Recorder: j, Policy: e, InstallationID: j.InstallationID(), Getenv: func(k string) string {
		if k == "PROVIDER_KEY" {
			return "provider-key"
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		r := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"fixture-model","input":"hello","store":false}`))
		r.Header.Set("Authorization", "Bearer local-token")
		r.Header.Set("X-Virgil-Run-ID", "run_responses_test")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if i == 0 && (w.Code != 200 || !strings.Contains(w.Body.String(), `"resp_1"`)) {
			t.Fatalf("first: %d %s", w.Code, w.Body.String())
		}
		if i == 1 && (w.Code != 403 || !strings.Contains(w.Body.String(), "call_limit")) {
			t.Fatalf("blocked: %d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls=%d", calls.Load())
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE status='policy_block'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("block events=%d", count)
	}
}
