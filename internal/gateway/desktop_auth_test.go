package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func TestDesktopAuthenticationIsOptInAndNotSupervised(t *testing.T) {
	r := &router{getenv: func(name string) string {
		if name == "VIRGIL_DESKTOP_TOKEN" {
			return "desktop-secret-at-least-32-bytes-long"
		}
		return ""
	}, executionAuth: testExecutionAuth{"run-secret": "run_real"}}
	var got requestIdentity
	var supervised bool
	h := r.authenticateExecution(func(w http.ResponseWriter, req *http.Request) {
		got, supervised = identityFromRequest(req)
		w.WriteHeader(http.StatusNoContent)
	}, "Authorization")
	for _, token := range []string{"", "wrong", "desktop-secret-at-least-32-bytes-long-extra"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("token %q status %d", token, w.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer desktop-secret-at-least-32-bytes-long")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusNoContent || supervised || !got.UseConfiguredKey || got.RunID != "desktop_codex" {
		t.Fatalf("desktop identity=%+v supervised=%v status=%d", got, supervised, w.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer run-secret")
	w = httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusNoContent || !supervised || got.RunID != "run_real" {
		t.Fatalf("run identity=%+v supervised=%v status=%d", got, supervised, w.Code)
	}
}

func TestDesktopAuthenticationAbsentKeepsRunTokenRequired(t *testing.T) {
	r := &router{getenv: func(string) string { return "" }, executionAuth: testExecutionAuth{"run-secret": "run_real"}}
	h := r.authenticateExecution(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }, "Authorization")
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer desktop-secret")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestShortDesktopSecretIsNeverAccepted(t *testing.T) {
	r := &router{getenv: func(name string) string {
		if name == "VIRGIL_DESKTOP_TOKEN" {
			return "short"
		}
		return ""
	}, executionAuth: testExecutionAuth{}}
	h := r.authenticateExecution(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }, "Authorization")
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer short")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestDesktopSessionRunIDIsBoundedAndOpaque(t *testing.T) {
	a := desktopSessionRunID("codex", "session-one")
	b := desktopSessionRunID("codex", "session-two")
	if a == b || a == "desktop_codex" || a != desktopSessionRunID("codex", "session-one") {
		t.Fatalf("unstable or shared IDs: %q %q", a, b)
	}
	if got := desktopSessionRunID("codex", "bad/session"); got != "desktop_codex" {
		t.Fatalf("invalid session ID %q", got)
	}
}

func TestDesktopCodexSessionsHaveIndependentPolicyCounters(t *testing.T) {
	const desktopToken = "desktop-secret-at-least-32-bytes-long"
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
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
	engine, err := policies.NewEngine(j, policies.Limits{MaxCallsPerRun: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true, APIKeyEnv: "PROVIDER_KEY"}}}
	h, err := NewServer(cfg, Dependencies{DB: db, Recorder: j, Policy: engine, InstallationID: j.InstallationID(), ExecutionAuth: testExecutionAuth{}, Getenv: func(k string) string {
		switch k {
		case "VIRGIL_DESKTOP_TOKEN":
			return desktopToken
		case "PROVIDER_KEY":
			return "provider-key"
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(session string) (int, string) {
		body := []byte(`{"model":"fixture-model","input":"hello","store":false,"client_metadata":{"session_id":"` + session + `"}}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+desktopToken)
		req.Header.Set("X-Virgil-Run-ID", "attacker_claim")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code, w.Header().Get("X-Virgil-Run-ID")
	}
	codeA, runA := call("session-a")
	codeBlocked, _ := call("session-a")
	codeB, runB := call("session-b")
	if codeA != 200 || codeBlocked != 403 || codeB != 200 || upstreamCalls != 2 || runA == runB || runA == "attacker_claim" || runB == "attacker_claim" {
		t.Fatalf("A=%d blocked=%d B=%d calls=%d runs=%q,%q", codeA, codeBlocked, codeB, upstreamCalls, runA, runB)
	}
	var blocks int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE status='policy_block'").Scan(&blocks); err != nil {
		t.Fatal(err)
	}
	if blocks != 1 {
		t.Fatalf("blocks=%d", blocks)
	}
}
