package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/executions"
	"github.com/MiguelReis944/Virgil/internal/runner"
)

func TestRunChildEnvHelper(t *testing.T) {
	if os.Getenv("VIRGIL_TEST_CHILD") != "1" {
		return
	}
	if os.Getenv("VIRGIL_RUN_ID") != "run_cli" || os.Getenv("OPENAI_API_KEY") != "run-secret" || os.Getenv("VIRGIL_RUN_TOKEN") != "run-secret" || os.Getenv("PROVIDER_API_KEY") != "" {
		os.Exit(3)
	}
	if err := os.WriteFile(os.Getenv("VIRGIL_TEST_OUTPUT"), []byte("ready"), 0600); err != nil {
		os.Exit(4)
	}
}

func TestRunCommandUsesConfiguredCoreAndRunToken(t *testing.T) {
	credential := base64.RawURLEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+credential {
			t.Errorf("missing control bearer: %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/api/executions":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"run_id":"run_cli","run_token":"run-secret","signal_token":"signal-secret"}`)
		case "/api/executions/run_cli/signals":
			if r.Header.Get("X-Virgil-Signal-Token") != "signal-secret" {
				t.Error("missing signal token")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: ready\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/api/executions/run_cli/running":
			w.WriteHeader(http.StatusNoContent)
		case "/api/executions/run_cli/finish":
			var request executions.FinishRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request.State != executions.StateCompleted {
				t.Errorf("finish=%+v", request)
			}
			fmt.Fprint(w, `{"run_id":"run_cli","state":"completed","exit_code":0}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "control.token"), []byte(credential), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "virgil.toml")
	if err := os.WriteFile(configPath, []byte("[server]\nlisten = \"127.0.0.1:8787\"\n[storage]\npath = \""+filepath.ToSlash(filepath.Join(dir, "virgil.db"))+"\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "child.txt")
	t.Setenv("PROVIDER_API_KEY", "provider-secret")
	err := runRun([]string{"--config", configPath, "--address", strings.TrimPrefix(server.URL, "http://"), "--run-id", "run_cli", "--env", "VIRGIL_TEST_CHILD=1,VIRGIL_TEST_OUTPUT=" + output, "--", os.Args[0], "-test.run=^TestRunChildEnvHelper$"})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "ready" {
		t.Fatalf("child output=%q err=%v", data, err)
	}
}

func TestRunCommandRejectsRemoteAddressBeforeChild(t *testing.T) {
	err := runRun([]string{"--address", "example.com:8787", "--", "missing-child"})
	if err == nil {
		t.Fatal("accepted remote core")
	}
}

func TestRunExitCodeFailsClosedAfterStoppedChild(t *testing.T) {
	zero := 0
	for _, state := range []executions.State{executions.StateBlocked, executions.StateGatewayFailed, executions.StateDeadline, executions.StateInterrupted} {
		if code := runExitCode(runner.ExecutionSummary{State: state, ExitCode: &zero}); code == 0 {
			t.Errorf("state %s returned success", state)
		}
	}
}
