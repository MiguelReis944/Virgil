package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/controlauth"
	"github.com/MiguelReis944/Virgil/internal/executions"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func TestCoreBindsGatewayRequestsToRegisteredExecution(t *testing.T) {
	var upstreamCalls atomic.Int64
	var upstreamKey atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		upstreamKey.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"synthetic","model":"fixture-model","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "virgil.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	readDB, err := storage.OpenReadPool(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer readDB.Close()
	credential, err := controlauth.LoadOrCreate(filepath.Join(dir, "control.token"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORE_TEST_PROVIDER_KEY", "configured-provider-key")
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model", APIKeyEnv: "CORE_TEST_PROVIDER_KEY", Local: true},
	}}
	handler, journal, _, err := buildHandlerWithControl(cfg, filepath.Join(dir, "virgil.toml"), db, readDB, &credential)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()

	register := httptest.NewRequest(http.MethodPost, "/api/executions", strings.NewReader(`{"run_id":"bound_run"}`))
	register.Header.Set("Authorization", "Bearer "+credential.Bearer())
	registration := httptest.NewRecorder()
	handler.ServeHTTP(registration, register)
	if registration.Code != http.StatusCreated {
		t.Fatalf("registration status=%d body=%s", registration.Code, registration.Body.String())
	}
	var execution executions.Registration
	if err := json.Unmarshal(registration.Body.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}
	if execution.RunID != "bound_run" || execution.RunToken == "" {
		t.Fatalf("invalid registration: run=%q has_token=%t", execution.RunID, execution.RunToken != "")
	}

	chat := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}]}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("X-Virgil-Run-ID", "forged_run")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	for _, token := range []string{"", "invalid-run-token"} {
		if response := chat(token); response.Code != http.StatusUnauthorized || upstreamCalls.Load() != 0 {
			t.Fatalf("token %q: status=%d provider calls=%d", token, response.Code, upstreamCalls.Load())
		}
	}
	response := chat(execution.RunToken)
	if response.Code != http.StatusOK || response.Header().Get("X-Virgil-Run-ID") != execution.RunID || upstreamCalls.Load() != 1 {
		t.Fatalf("valid token: status=%d run=%q calls=%d body=%s", response.Code, response.Header().Get("X-Virgil-Run-ID"), upstreamCalls.Load(), response.Body.String())
	}
	if got := upstreamKey.Load(); got != "Bearer configured-provider-key" {
		t.Fatalf("upstream credential=%v", got)
	}

	toolResult := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/tool-results", strings.NewReader(`{"run_id":"forged_run","tool_call_id":"call_1","tool_name":"lookup","status":"success"}`))
		if token != "" {
			req.Header.Set("X-Virgil-App-Token", token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	for _, token := range []string{"", "invalid-run-token"} {
		if response := toolResult(token); response.Code != http.StatusUnauthorized {
			t.Fatalf("tool token %q: status=%d body=%s", token, response.Code, response.Body.String())
		}
	}
	if response := toolResult(execution.RunToken); response.Code != http.StatusNoContent {
		t.Fatalf("valid tool token: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCoreOptionsRejectsInvalidPanelURLs(t *testing.T) {
	for _, panelURL := range []string{
		"https://127.0.0.1:8787/dashboard",
		"http://example.com/dashboard",
		"http://127.0.0.1:8787/other",
		"http://127.0.0.1:8787/dashboard.evil",
		"http://user@127.0.0.1:8787/dashboard",
		"http://127.0.0.1:8787/dashboard?token=secret",
	} {
		t.Run(panelURL, func(t *testing.T) {
			if err := OpenBrowser(panelURL); err == nil {
				t.Fatal("expected unsafe panel URL to be rejected")
			}
		})
	}
}

func TestCoreOptionsAcceptsLoopbackPanelURLs(t *testing.T) {
	for _, panelURL := range []string{
		"http://127.0.0.1:8787/dashboard",
		"http://[::1]:8787/dashboard/executions/run_1",
	} {
		if err := validatePanelURL(panelURL); err != nil {
			t.Fatalf("%s: %v", panelURL, err)
		}
	}
}

func TestCoreOptionsRunCoreRejectsOccupiedListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	configPath := filepath.Join(t.TempDir(), "virgil.toml")
	dataPath := filepath.ToSlash(filepath.Join(t.TempDir(), "virgil.db"))
	configText := fmt.Sprintf("[server]\nlisten = %q\n[storage]\npath = %q\n", listener.Addr().String(), dataPath)
	if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	err = RunCore(context.Background(), CoreOptions{ConfigPath: configPath, OpenPanel: false})
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("error = %v, want listener failure", err)
	}
}

func TestRunCoreRejectsMalformedConfigInsteadOfStartingSetup(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "virgil.toml")
	if err := os.WriteFile(configPath, []byte("[server\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := RunCore(ctx, CoreOptions{ConfigPath: configPath})
	if err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("error = %v, want actionable parse error", err)
	}
}

func TestRunCoreRejectsUnreadableConfigInsteadOfStartingSetup(t *testing.T) {
	configPath := t.TempDir() // A directory cannot be read as a TOML file.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := RunCore(ctx, CoreOptions{ConfigPath: configPath})
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("error = %v, want actionable read error", err)
	}
}

func TestRunCoreRejectsInvalidConfigInsteadOfStartingSetup(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "virgil.toml")
	if err := os.WriteFile(configPath, []byte("[server]\nlisten = \"0.0.0.0:8787\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := RunCore(ctx, CoreOptions{ConfigPath: configPath})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error = %v, want loopback validation error", err)
	}
}

func TestCoreOptionsServesPanelUntilCancelled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	configPath := filepath.Join(t.TempDir(), "virgil.toml")
	dataPath := filepath.ToSlash(filepath.Join(t.TempDir(), "virgil.db"))
	configText := fmt.Sprintf("[server]\nlisten = %q\n[storage]\npath = %q\n", address, dataPath)
	if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- RunCore(ctx, CoreOptions{ConfigPath: configPath}) }()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	ready := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get("http://" + address + "/health")
		if err == nil {
			response.Body.Close()
			ready = response.StatusCode == http.StatusOK
			if ready {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("local core did not become ready")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dataPath), "control.token")); err != nil {
		t.Fatalf("control credential was not created beside SQLite: %v", err)
	}
	response, err := client.Get("http://" + address + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	client.CloseIdleConnections()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("panel status = %d", response.StatusCode)
	}
	controlURL := "http://" + address + "/api/executions"
	unauthorized, err := http.Post(controlURL, "application/json", strings.NewReader(`{"run_id":"core_run"}`))
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized control status = %d", unauthorized.StatusCode)
	}
	credential, err := os.ReadFile(filepath.Join(filepath.Dir(dataPath), "control.token"))
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, controlURL, strings.NewReader(`{"run_id":"core_run"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+string(credential))
	request.Header.Set("Content-Type", "application/json")
	registered, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	registered.Body.Close()
	client.CloseIdleConnections()
	if registered.StatusCode != http.StatusCreated {
		t.Fatalf("authenticated control status = %d", registered.StatusCode)
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("core did not stop after cancellation")
	}
}

func TestRunCoreRecoversAbandonedExecutionsBeforeServing(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "virgil.db")
	configPath := filepath.Join(dir, "virgil.toml")
	configText := fmt.Sprintf("[server]\nlisten = %q\n[storage]\npath = %q\n", address, filepath.ToSlash(dbPath))
	if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-time.Minute)
	for _, id := range []string{"starting", "running"} {
		if err := journal.StartExecution(context.Background(), id, started); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.MarkExecutionRunning(context.Background(), "running"); err != nil {
		t.Fatal(err)
	}
	journal.Close()
	db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- RunCore(ctx, CoreOptions{ConfigPath: configPath}) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Errorf("core shutdown: %v", err)
			}
		case <-time.After(7 * time.Second):
			t.Error("core did not stop")
		}
	})
	client := &http.Client{Timeout: 100 * time.Millisecond}
	defer client.CloseIdleConnections()
	ready := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get("http://" + address + "/health")
		if err == nil {
			response.Body.Close()
			ready = response.StatusCode == http.StatusOK
			if ready {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("core did not become ready")
	}

	checkDB, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer checkDB.Close()
	for _, id := range []string{"starting", "running"} {
		var state, reason string
		var endedAt int64
		err := checkDB.QueryRowContext(context.Background(),
			"SELECT state, COALESCE(stop_reason, ''), COALESCE(ended_at_unix_ns, 0) FROM executions WHERE run_id=?", id,
		).Scan(&state, &reason, &endedAt)
		if err != nil || state != string(executions.StateInterrupted) || reason != "virgil_restart" || endedAt == 0 {
			t.Fatalf("execution %s: state=%q reason=%q ended=%d err=%v", id, state, reason, endedAt, err)
		}
	}
}

func TestServePanelWaitsForHTTPHandlerBeforeOpening(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	panelURL := "http://" + listener.Addr().String() + "/dashboard"
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened := make(chan string, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- servePanel(ctx, listener, handler, panelURL, func(url string) error {
			opened <- url
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness probe did not reach handler")
	}
	select {
	case <-opened:
		t.Fatal("browser opened before handler answered")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case got := <-opened:
		if got != panelURL {
			t.Fatalf("opened %q, want %q", got, panelURL)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("browser did not open after handler answered")
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestServePanelDoesNotOpenAfterCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	panelURL := "http://" + listener.Addr().String() + "/dashboard"
	started := make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened := make(chan struct{}, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- servePanel(ctx, listener, handler, panelURL, func(string) error {
			opened <- struct{}{}
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness probe did not reach handler")
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
	select {
	case <-opened:
		t.Fatal("browser opened after cancellation")
	default:
	}
}

func TestServePanelDoesNotOpenMissingPanelPage(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	panelURL := "http://" + listener.Addr().String() + "/dashboard"
	served := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		select {
		case served <- struct{}{}:
		default:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened := make(chan struct{}, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- servePanel(ctx, listener, handler, panelURL, func(string) error {
			opened <- struct{}{}
			return nil
		})
	}()
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("panel probe did not reach handler")
	}
	select {
	case <-opened:
		t.Fatal("browser opened a missing panel page")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}
