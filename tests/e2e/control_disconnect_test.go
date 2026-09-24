package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/app"
	"github.com/MiguelReis944/Virgil/internal/executions"
	"github.com/MiguelReis944/Virgil/internal/runner"
)

func TestRegistrationFailureDoesNotStartChild(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := runner.NewControlClient(server.URL, "local-test-credential")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	fixture := buildTestBinary(t, filepath.Join("..", ".."), "./tests/fixtures/supervised-child", t.TempDir(), "supervised-child")
	_, err = runner.Supervise(context.Background(), runner.SupervisionSpec{Control: client, Run: runner.RunSpec{Command: []string{fixture, "idle"}, GatewayURL: server.URL, Env: map[string]string{"VIRGIL_TEST_MARKER": marker}}})
	if err == nil || !strings.Contains(err.Error(), "register execution") {
		t.Fatalf("registration failure = %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("child marker unexpectedly exists: %v", err)
	}
}

func TestCoreShutdownStopsSupervisedTree(t *testing.T) {
	dir := t.TempDir()
	address := unusedLoopbackAddress(t)
	configPath := filepath.Join(dir, "virgil.toml")
	configText := fmt.Sprintf("[server]\nlisten = %q\nlog_level = \"info\"\n[storage]\npath = %q\nretention_days = 30\n", address, filepath.Join(dir, "virgil.db"))
	if err := os.WriteFile(configPath, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	coreCtx, stopCore := context.WithCancel(context.Background())
	coreDone := make(chan error, 1)
	go func() {
		coreDone <- app.RunCore(coreCtx, app.CoreOptions{ConfigPath: configPath, EnvPath: filepath.Join(dir, "absent.env")})
	}()
	t.Cleanup(stopCore)
	waitForHealth(t, "http://"+address)
	credential, err := os.ReadFile(filepath.Join(dir, "control.token"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := runner.NewControlClient("http://"+address, string(credential))
	if err != nil {
		t.Fatal(err)
	}
	fixture := buildTestBinary(t, filepath.Join("..", ".."), "./tests/fixtures/supervised-child", dir, "supervised-child")
	marker := filepath.Join(dir, "child")
	runDone := make(chan error, 1)
	go func() {
		_, err := runner.Supervise(context.Background(), runner.SupervisionSpec{Control: client, Run: runner.RunSpec{RunID: "run_core_shutdown", Command: []string{fixture, "idle"}, GatewayURL: "http://" + address, Env: map[string]string{"VIRGIL_TEST_MARKER": marker}}})
		runDone <- err
	}()
	waitForFile(t, filepath.Join(marker, "heartbeat"))
	stopCore()
	select {
	case <-runDone:
	case <-time.After(8 * time.Second):
		t.Fatal("supervised child survived core shutdown")
	}
	select {
	case <-coreDone:
	case <-time.After(8 * time.Second):
		t.Fatal("core failed to stop")
	}
	heartbeat := filepath.Join(marker, "heartbeat")
	before, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	after, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("descendant survived core shutdown")
	}
}

func TestSignalLossAndCrossRunSignalStopProcessTree(t *testing.T) {
	for _, tc := range []struct{ name, signal string }{
		{"closed-stream", ""},
		{"different-run", "event: circuit_break\ndata: {\"run_id\":\"run_other\",\"policy\":\"max_requests\",\"reason\":\"call_limit\",\"attempt\":2,\"threshold\":1}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const runID = "run_control_disconnect"
			marker := filepath.Join(t.TempDir(), "child")
			streamStop := make(chan struct{})
			runningReady := make(chan struct{})
			finishRequests := make(chan executions.FinishRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/executions":
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{"run_id": runID, "run_token": "run-token", "signal_token": "signal-token"})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/signals"):
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "event: ready\n\n")
					w.(http.Flusher).Flush()
					select {
					case <-streamStop:
					case <-r.Context().Done():
						return
					}
					_, _ = fmt.Fprint(w, tc.signal)
					w.(http.Flusher).Flush()
					if tc.signal != "" {
						<-r.Context().Done()
					}
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/running"):
					w.WriteHeader(http.StatusNoContent)
					close(runningReady)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/finish"):
					var finish executions.FinishRequest
					if err := json.NewDecoder(r.Body).Decode(&finish); err != nil {
						http.Error(w, "invalid finish", http.StatusBadRequest)
						return
					}
					finishRequests <- finish
					_ = json.NewEncoder(w).Encode(map[string]any{"run_id": runID, "state": "gateway_failed", "exit_code": -1, "stop_reason": "signal_lost", "termination_status": "succeeded"})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := runner.NewControlClient(server.URL, "local-test-credential")
			if err != nil {
				t.Fatal(err)
			}
			fixture := buildTestBinary(t, filepath.Join("..", ".."), "./tests/fixtures/supervised-child", t.TempDir(), "supervised-child")
			result := make(chan error, 1)
			go func() {
				_, err := runner.Supervise(context.Background(), runner.SupervisionSpec{Control: client, Run: runner.RunSpec{RunID: runID, Command: []string{fixture, "idle"}, GatewayURL: server.URL, Env: map[string]string{"VIRGIL_TEST_MARKER": marker}}})
				result <- err
			}()
			waitForFile(t, filepath.Join(marker, "heartbeat"))
			select {
			case <-runningReady:
			case <-time.After(5 * time.Second):
				t.Fatal("run was not marked running")
			}
			close(streamStop)
			select {
			case err := <-result:
				if err != nil {
					t.Fatalf("supervision: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("child survived lost control stream")
			}
			select {
			case finish := <-finishRequests:
				if finish.State != executions.StateGatewayFailed || finish.StopReason != "signal_lost" || finish.TerminationStatus != executions.TerminationSucceeded {
					t.Fatalf("incorrect fail-closed finish for %s: %+v", tc.name, finish)
				}
			default:
				t.Fatal("supervisor did not report its finish decision")
			}
			heartbeat := filepath.Join(marker, "heartbeat")
			before, err := os.ReadFile(heartbeat)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(150 * time.Millisecond)
			after, err := os.ReadFile(heartbeat)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("descendant survived lost control stream")
			}
		})
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("marker %s was not created", filepath.Base(path))
}
