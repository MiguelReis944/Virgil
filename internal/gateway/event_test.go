package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

type captureEvents struct {
	events []telemetry.Event
}

func (c *captureEvents) Record(_ context.Context, event telemetry.Event) error {
	c.events = append(c.events, event)
	return nil
}

func TestGatewayRecordsOneEventAndPreservesTrace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl_fixture","object":"chat.completion","model":"fixture-model","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recorder := &captureEvents{}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model"},
	}}
	handler, err := NewServer(cfg, Dependencies{DB: db, Client: http.DefaultClient, Getenv: func(string) string { return "" }, Recorder: recorder, InstallationID: "install_fixture"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatFixture(t)))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	req.Header.Set("traceparent", "00-00000000000000000000000000000001-0000000000000002-01")
	req.Header.Set("X-Virgil-Run-ID", "run_fixture")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != 200 || len(recorder.events) != 1 {
		t.Fatalf("status=%d events=%d", resp.Code, len(recorder.events))
	}
	event := recorder.events[0]
	if event.TraceID != "00000000000000000000000000000001" || event.RunID != "run_fixture" || event.Provider != "fixture" || event.Status != "success" || event.UsageSource != "provider" {
		t.Fatalf("event=%+v", event)
	}
	if event.InputTokens == nil || *event.InputTokens != 5 {
		t.Fatalf("usage=%+v", event)
	}
}
