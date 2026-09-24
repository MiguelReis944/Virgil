package e2e

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/app"
	"github.com/MiguelReis944/Virgil/internal/executions"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func TestInstalledStyleCircuitBreak(t *testing.T) {
	const runID = "run_e2e_circuit_break"
	const providerKey = "provider-key-canary-supervised"
	const responseCanary = "response-canary-supervised"
	t.Setenv("VIRGIL_PROVIDER_KEY_E2E", providerKey)
	var forwarded atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jsonCompletion("fixture-model", responseCanary)))
	}))
	defer provider.Close()

	dir := t.TempDir()
	marker := filepath.Join(dir, "child")
	dbPath := filepath.Join(dir, "virgil.db")
	configPath := filepath.Join(dir, "virgil.toml")
	address := unusedLoopbackAddress(t)
	configText := fmt.Sprintf("[server]\nlisten = %q\nlog_level = \"info\"\n[storage]\npath = %q\nretention_days = 30\n[guardrails]\nmax_requests_per_run = 1\n[providers.fixture]\ntype = \"openai-compatible\"\nbase_url = %q\nmodel = \"fixture-model\"\nlocal = true\napi_key = \"${VIRGIL_PROVIDER_KEY_E2E}\"\n", address, dbPath, provider.URL+"/v1")
	if err := os.WriteFile(configPath, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	coreCtx, stopCore := context.WithCancel(context.Background())
	coreDone := make(chan error, 1)
	coreStopped := false
	go func() {
		coreDone <- app.RunCore(coreCtx, app.CoreOptions{ConfigPath: configPath, EnvPath: filepath.Join(dir, "absent.env")})
	}()
	t.Cleanup(func() {
		if coreStopped {
			return
		}
		stopCore()
		select {
		case err := <-coreDone:
			if err != nil {
				t.Errorf("core shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("core failed to stop")
		}
	})
	baseURL := "http://" + address
	waitForHealth(t, baseURL)
	previousLogger := slog.Default()
	var coreLogs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&coreLogs, nil)))
	defer slog.SetDefault(previousLogger)
	root := filepath.Join("..", "..")
	virgil := buildTestBinary(t, root, "./cmd/virgil", dir, "virgil")
	fixture := buildTestBinary(t, root, "./tests/fixtures/supervised-child", dir, "supervised-child")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, virgil, "run", "--config", configPath, "--run-id", runID, "--env", "VIRGIL_TEST_MARKER="+marker+",VIRGIL_TEST_EXPECT_RUN_ID="+runID, "--", fixture)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("supervised run timed out: %v\n%s", ctx.Err(), output)
	}
	if err == nil {
		t.Fatalf("blocked run exited successfully: %s", output)
	}
	if !strings.Contains(string(output), "state=blocked") {
		t.Fatalf("missing blocked summary: %s", output)
	}
	if got := forwarded.Load(); got != 1 {
		t.Fatalf("provider requests = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(marker, "environment.valid")); err != nil {
		t.Fatalf("fixture did not validate injected environment: %v", err)
	}
	if _, err := os.Stat(filepath.Join(marker, "environment.invalid")); !os.IsNotExist(err) {
		t.Fatalf("fixture rejected injected environment: %v", err)
	}
	for name, want := range map[string]string{"request-1.status": "200"} {
		data, readErr := os.ReadFile(filepath.Join(marker, name))
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if string(data) != want {
			t.Fatalf("%s = %q, want %s", name, data, want)
		}
	}
	// The SSE signal may kill the child before it writes the second status.
	// When that write wins the race, the actual HTTP response must be forbidden.
	if second, err := os.ReadFile(filepath.Join(marker, "request-2.status")); err == nil && len(second) > 0 && string(second) != "403" {
		t.Fatalf("blocked request status = %q, want 403", second)
	}
	db, err := storage.OpenReadPool(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	row, err := storage.Execution(context.Background(), db, runID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != executions.StateBlocked || row.TerminationStatus != executions.TerminationSucceeded || row.StopReason != "policy_block" {
		t.Fatalf("execution outcome = %+v", row)
	}
	if row.Policy == "" || row.PolicyAttempt == nil || *row.PolicyAttempt != 2 {
		t.Fatalf("missing first policy block details: %+v", row)
	}
	var blocks int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE status='policy_block' AND json_extract(payload, '$.run_id')=?", runID).Scan(&blocks); err != nil {
		t.Fatal(err)
	}
	if blocks != 1 {
		t.Fatalf("block events = %d, want 1", blocks)
	}
	heartbeat := filepath.Join(marker, "heartbeat")
	before, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatalf("descendant never ran: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	after, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("descendant kept running after circuit break")
	}
	controlCredential, err := os.ReadFile(filepath.Join(dir, "control.token"))
	if err != nil {
		t.Fatal(err)
	}
	panel, err := http.Get(baseURL + "/dashboard/executions/" + runID)
	if err != nil {
		t.Fatal(err)
	}
	if panel.StatusCode != http.StatusOK {
		_ = panel.Body.Close()
		t.Fatalf("execution panel status = %d", panel.StatusCode)
	}
	panelBody := new(bytes.Buffer)
	if _, err := panelBody.ReadFrom(panel.Body); err != nil {
		_ = panel.Body.Close()
		t.Fatal(err)
	}
	_ = panel.Body.Close()
	stopCore()
	select {
	case err := <-coreDone:
		coreStopped = true
		if err != nil {
			t.Fatalf("core shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("core failed to stop")
	}
	databaseBytes, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	walBytes, err := os.ReadFile(dbPath + "-wal")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, canary := range []string{providerKey, "prompt-canary-supervised", responseCanary, string(controlCredential)} {
		for name, artifact := range map[string][]byte{"sqlite": databaseBytes, "sqlite WAL": walBytes, "cli": output, "panel": panelBody.Bytes(), "core logs": coreLogs.Bytes()} {
			if bytes.Contains(artifact, []byte(canary)) {
				t.Errorf("%s leaked into %s", canary, name)
			}
		}
	}
}

func unusedLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func waitForHealth(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(baseURL + "/health")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("local core did not become healthy")
}

func buildTestBinary(t *testing.T, root, pkg, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", path, pkg)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, output)
	}
	return path
}
