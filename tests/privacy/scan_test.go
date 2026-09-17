package privacy

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/gateway"
	"github.com/MiguelReis944/Virgil/internal/redaction"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func TestScanRejectsSecretShapes(t *testing.T) {
	for _, value := range []string{
		"sk-abcdefghijklmnopqrstuvwxyz123456",
		"ghp_abcdefghijklmnopqrstuvwxyz123456",
		"-----BEGIN PRIVATE KEY-----",
		"https://user:password@example.invalid/path",
	} {
		if ScanBytes([]byte(value)) == "" {
			t.Fatalf("secret shape accepted: %q", value[:3])
		}
	}
	if finding := ScanBytes([]byte("api_key = \"\u0024{PROVIDER_KEY}\"")); finding != "" {
		t.Fatalf("environment reference rejected: %s", finding)
	}
}

func TestTrackedPublicFilesHaveNoSecrets(t *testing.T) {
	if err := ScanTrackedRepository(filepath.Join("..", "..")); err != nil {
		t.Fatal(err)
	}
}

func TestCanariesStayOutOfSQLiteLogsAndExport(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl_fixture","object":"chat.completion","model":"fixture-model","choices":[{"index":0,"message":{"role":"assistant","content":"response-canary","tool_calls":[{"id":"call_fixture","type":"function","function":{"name":"fixture_tool","arguments":"tool-canary"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
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
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model"},
	}}
	handler, err := gateway.NewServer(cfg, gateway.Dependencies{
		DB: db, Client: http.DefaultClient, Recorder: journal,
		InstallationID: journal.InstallationID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"prompt-canary"}]}`))
	req.Header.Set("Authorization", "Bearer key-canary")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	var eventID string
	var stored []byte
	if err := db.QueryRow("SELECT event_id, payload FROM events LIMIT 1").Scan(&eventID, &stored); err != nil {
		t.Fatal(err)
	}
	event, err := journal.Get(context.Background(), eventID)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := redaction.ForExport(event, []string{"event_id", "provider", "status"})
	if err != nil {
		t.Fatal(err)
	}
	exportBytes, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"prompt-canary", "response-canary", "tool-canary", "key-canary"} {
		if bytes.Contains(stored, []byte(canary)) || bytes.Contains(logs.Bytes(), []byte(canary)) || bytes.Contains(exportBytes, []byte(canary)) {
			t.Fatalf("canary %q escaped privacy boundary", canary)
		}
	}
}
