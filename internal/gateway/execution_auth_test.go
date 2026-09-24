package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/executions"
	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

type testExecutionAuth map[string]string

func (a testExecutionAuth) ResolveRunToken(token string) (string, bool) {
	id, ok := a[token]
	return id, ok
}

func TestSupervisedPolicyBlockPersistsBeforeCircuitBreakSignal(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"fixture-model","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
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
	registry := executions.NewRegistry(journal)
	registration, err := registry.Register(context.Background(), "run_policy_block")
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.MarkExecutionRunning(context.Background(), registration.RunID); err != nil {
		t.Fatal(err)
	}
	signals, release, err := registry.Subscribe(registration.RunID, registration.SignalToken)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	engine, err := policies.NewEngine(journal, policies.Limits{MaxCallsPerRun: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model", APIKeyEnv: "TEST_PROVIDER_KEY", Local: true},
	}}
	h, err := NewServer(cfg, Dependencies{
		DB: db, Recorder: journal, Policy: engine, InstallationID: journal.InstallationID(),
		ExecutionAuth: registry, PolicyBlocks: journal, PolicyNotifier: registry,
		Getenv: func(string) string { return "provider-key" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := supervisedChat(h, registration.RunToken, ""); got.Code != http.StatusOK {
		t.Fatalf("first: %d %s", got.Code, got.Body.String())
	}
	const blockedRequests = 8
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, blockedRequests)
	var requests sync.WaitGroup
	for range blockedRequests {
		requests.Add(1)
		go func() {
			defer requests.Done()
			<-start
			responses <- supervisedChat(h, registration.RunToken, "")
		}()
	}
	close(start)
	requests.Wait()
	close(responses)
	for got := range responses {
		if got.Code != http.StatusForbidden {
			t.Fatalf("blocked request: %d %s", got.Code, got.Body.String())
		}
	}
	select {
	case signal := <-signals:
		if signal.Kind != executions.SignalCircuitBreak || signal.PolicyBlock.PersistenceFailed {
			t.Fatalf("signal=%+v", signal)
		}
		execution, err := journal.Execution(context.Background(), registration.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if execution.State != executions.StateBlocked {
			t.Fatalf("state=%s", execution.State)
		}
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE status='policy_block'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("block events=%d", count)
		}
	case <-time.After(time.Second):
		t.Fatal("circuit break signal not delivered")
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls=%d", calls.Load())
	}
}

type failingPolicyBlockRecorder struct{}

func (failingPolicyBlockRecorder) RecordPolicyBlock(context.Context, telemetry.Event, executions.PolicyBlockNotice) error {
	return errors.New("disk unavailable")
}

type capturePolicyNotifier struct {
	notices chan executions.PolicyBlockNotice
}

func (n capturePolicyNotifier) NotifyPolicyBlock(notice executions.PolicyBlockNotice) {
	n.notices <- notice
}

func TestPolicyBlockPersistenceFailureStillSignalsSupervisor(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"fixture-model","choices":[]}`))
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
	e, err := policies.NewEngine(j, policies.Limits{MaxCostPerRunUSD: "1"})
	if err != nil {
		t.Fatal(err)
	}
	notifier := capturePolicyNotifier{notices: make(chan executions.PolicyBlockNotice, 1)}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model", APIKeyEnv: "KEY", Local: true}}}
	server, err := NewServer(cfg, Dependencies{DB: db, Recorder: j, Policy: e, InstallationID: j.InstallationID(), ExecutionAuth: testExecutionAuth{"token": "run_failure"}, PolicyBlocks: failingPolicyBlockRecorder{}, PolicyNotifier: notifier, Getenv: func(string) string { return "key" }})
	if err != nil {
		t.Fatal(err)
	}
	resp := supervisedChat(server, "token", "")
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	select {
	case notice := <-notifier.notices:
		if !notice.PersistenceFailed {
			t.Fatalf("notice=%+v", notice)
		}
	case <-time.After(time.Second):
		t.Fatal("missing failure signal")
	}
}

func supervisedGateway(t *testing.T, upstreamURL string, key string, auth ExecutionAuthenticator) (http.Handler, *sql.DB) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(journal.Close)
	engine, err := policies.NewEngine(journal, policies.Limits{MaxCallsPerRun: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstreamURL + "/v1", Model: "fixture-model", APIKeyEnv: "TEST_PROVIDER_KEY", Local: true},
	}}
	h, err := NewServer(cfg, Dependencies{
		DB: db, Recorder: journal, Policy: engine, InstallationID: journal.InstallationID(), ExecutionAuth: auth,
		Getenv: func(name string) string {
			if name == "TEST_PROVIDER_KEY" {
				return key
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, db
}

func supervisedChat(h http.Handler, token, claimedRunID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}]}`)))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if claimedRunID != "" {
		req.Header.Set("X-Virgil-Run-ID", claimedRunID)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestSupervisedChatBindsTokenAndUsesConfiguredProviderKey(t *testing.T) {
	var calls atomic.Int64
	var upstreamKey atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		upstreamKey.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"synthetic","model":"fixture-model","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()
	h, db := supervisedGateway(t, upstream.URL, "configured-provider-key", testExecutionAuth{"token-a": "run_a", "token-b": "run_b"})
	for _, token := range []string{"", "invalid-token"} {
		resp := supervisedChat(h, token, "run_b")
		if resp.Code != http.StatusUnauthorized || calls.Load() != 0 {
			t.Fatalf("invalid token %q: status=%d calls=%d", token, resp.Code, calls.Load())
		}
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid requests recorded %d events", count)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM policy_reservations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid requests reserved %d policy calls", count)
	}
	first := supervisedChat(h, "token-a", "run_b")
	if first.Code != http.StatusOK || first.Header().Get("X-Virgil-Run-ID") != "run_a" {
		t.Fatalf("bound request: status=%d run=%q body=%s", first.Code, first.Header().Get("X-Virgil-Run-ID"), first.Body.String())
	}
	if got := upstreamKey.Load(); got != "Bearer configured-provider-key" {
		t.Fatalf("upstream credential=%v", got)
	}
	if resp := supervisedChat(h, "token-a", "run_b"); resp.Code != http.StatusForbidden {
		t.Fatalf("run a second request: status=%d body=%s", resp.Code, resp.Body.String())
	}
	other := supervisedChat(h, "token-b", "run_a")
	if other.Code != http.StatusOK || other.Header().Get("X-Virgil-Run-ID") != "run_b" || calls.Load() != 2 {
		t.Fatalf("run b: status=%d run=%q calls=%d", other.Code, other.Header().Get("X-Virgil-Run-ID"), calls.Load())
	}
}

func TestSupervisedChatRequiresConfiguredProviderKey(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	h, _ := supervisedGateway(t, upstream.URL, "", testExecutionAuth{"run-token": "run_a"})
	resp := supervisedChat(h, "run-token", "run_a")
	if resp.Code != http.StatusUnauthorized || !strings.Contains(resp.Body.String(), "provider_key_required") || calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d body=%s", resp.Code, calls.Load(), resp.Body.String())
	}
}

func TestSupervisedToolResultUsesBoundRun(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"synthetic","model":"fixture-model","choices":[]}`))
	}))
	defer upstream.Close()
	h, _ := supervisedGateway(t, upstream.URL, "configured-key", testExecutionAuth{"token-a": "run_a", "token-b": "run_b"})
	body := `{"run_id":"run_b","tool_call_id":"call_1","tool_name":"lookup","status":"error","error_code":"timeout"}`
	for _, token := range []string{"", "wrong"} {
		resp := postToolResult(h, token, body)
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: status=%d", token, resp.Code)
		}
	}
	for i := range 3 {
		item := strings.Replace(body, "call_1", "call_"+string(rune('a'+i)), 1)
		if resp := postToolResult(h, "token-a", item); resp.Code != http.StatusNoContent {
			t.Fatalf("result %d: status=%d body=%s", i, resp.Code, resp.Body.String())
		}
	}
	if resp := supervisedChat(h, "token-a", "run_b"); resp.Code != http.StatusForbidden {
		t.Fatalf("run a: status=%d body=%s", resp.Code, resp.Body.String())
	}
	if resp := supervisedChat(h, "token-b", "run_a"); resp.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("run b: status=%d calls=%d body=%s", resp.Code, calls.Load(), resp.Body.String())
	}
}
