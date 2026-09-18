package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

func TestLocalGatewayWiresDurableJournal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl_fixture","object":"chat.completion","model":"fixture-model","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "virgil.db")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model"},
	}}
	handler, journal, err := buildHandler(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}]}`))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted events=%d", count)
	}
}

func TestControlPlaneModeQueuesEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"fixture-model","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{ControlPlane: config.ControlPlaneConfig{Enabled: true}, Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model"},
	}}
	handler, journal, err := buildHandler(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}]}`))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	stats, err := journal.OutboxStats(context.Background(), "controlplane")
	if err != nil {
		t.Fatal(err)
	}
	if stats["pending"] != 1 {
		t.Fatalf("outbox=%v", stats)
	}
}

func TestConfiguredControlPlaneDrainsQueuedEvent(t *testing.T) {
	var sawCredential, sawProvider bool
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawCredential = r.Header.Get("Authorization") == "Bearer synthetic-credential"
		var request struct {
			Events []struct {
				EventID string         `json:"event_id"`
				Payload map[string]any `json:"payload"`
			} `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if len(request.Events) != 1 {
			t.Errorf("events=%d", len(request.Events))
			return
		}
		sawProvider = len(request.Events[0].Payload) == 1 && request.Events[0].Payload["provider"] == "fixture"
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted_ids": []string{request.Events[0].EventID}})
	}))
	defer cp.Close()
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("synthetic-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ControlPlaneConfig{Enabled: true, Endpoint: cp.URL, CredentialPath: path, AllowedFields: []string{"provider"}}
	client, err := configuredControlPlaneClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
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
	now := time.Now()
	ev, err := telemetry.Build(telemetry.Attempt{InstallationID: j.InstallationID(), RunID: "run_synthetic", TraceID: "00000000000000000000000000000001", SpanID: "0000000000000001", Provider: "fixture", RequestedModel: "fixture-model", UsageSource: "unknown", Status: "success", StartedAt: now, EndedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Append(context.Background(), ev, []string{"controlplane"}); err != nil {
		t.Fatal(err)
	}
	if err := drainControlPlaneOnce(context.Background(), j, client, cfg.AllowedFields); err != nil {
		t.Fatal(err)
	}
	stats, err := j.OutboxStats(context.Background(), "controlplane")
	if err != nil {
		t.Fatal(err)
	}
	if !sawCredential || !sawProvider || stats["delivered"] != 1 {
		t.Fatalf("credential=%v provider=%v stats=%v", sawCredential, sawProvider, stats)
	}
}
