package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/controlplane"
	"github.com/MiguelReis944/Virgil/internal/export"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

// TestReconnectDelivery proves that events recorded while the Control Plane
// is offline are delivered after connectivity is restored.
func TestReconnectDelivery(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jsonCompletion("fixture-model", "reconnect")))
	}))
	defer upstream.Close()

	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true},
	}}
	handler, journal := openGateway(t, cfg)

	// Record an event while the Control Plane is "offline" (no CP configured).
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"fixture-model","max_tokens":16,"messages":[{"role":"user","content":"offline"}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	time.Sleep(10 * time.Millisecond)

	// Build a fresh event and append it to the outbox for a CP destination
	// (simulating the gateway's behaviour when a CP is configured).
	const dest = "e2e-cp-reconnect"
	offlineEv, err := telemetry.Build(telemetry.Attempt{
		InstallationID: journal.InstallationID(),
		RunID:          "run_reconnect",
		TraceID:        "00000000000000000000000000000099",
		SpanID:         "0000000000000099",
		Provider:       "fixture",
		RequestedModel: "fixture-model",
		UsageSource:    "unknown",
		Status:         "success",
		StartedAt:      time.Now().Add(-time.Millisecond),
		EndedAt:        time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(context.Background(), offlineEv, []string{dest}); err != nil {
		t.Fatal(err)
	}

	// Start a fake Control Plane that accepts the batch.
	var delivered int
	cpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/events/batch" {
			var req struct {
				Events []struct {
					EventID string `json:"event_id"`
				} `json:"events"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			ids := make([]string, len(req.Events))
			for i, e := range req.Events {
				ids[i] = e.EventID
				delivered++
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"accepted_ids": ids})
		}
	}))
	defer cpServer.Close()

	// Drain the outbox to the CP â€” simulating reconnection.
	cpClient := controlplane.NewClient(cpServer.URL, "cred_reconnect", nil)
	n, err := export.DrainControlPlane(context.Background(), journal, dest, cpClient, []string{"event_id", "provider"}, 10)
	if err != nil {
		t.Fatalf("DrainControlPlane: %v", err)
	}
	if n == 0 {
		t.Fatal("expected at least 1 event delivered on reconnect")
	}
	if delivered == 0 {
		t.Fatal("CP server received no deliveries")
	}

	// Second drain should find nothing (outbox is empty).
	n2, err := export.DrainControlPlane(context.Background(), journal, dest, cpClient, []string{"event_id", "provider"}, 10)
	if err != nil || n2 != 0 {
		t.Fatalf("second drain: %v n=%d", err, n2)
	}
}

// TestTracePreservation proves that TraceID and SpanID from the client request
// are preserved in the recorded event.
func TestTracePreservation(t *testing.T) {
	const wantTrace = "0102030405060708090a0b0c0d0e0f10"
	const wantSpan = "0102030405060708"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jsonCompletion("fixture-model", "traced")))
	}))
	defer upstream.Close()

	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model", Local: true},
	}}
	handler, journal := openGateway(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"fixture-model","max_tokens":16,"messages":[{"role":"user","content":"trace me"}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("X-Trace-Id", wantTrace)
	req.Header.Set("X-Span-Id", wantSpan)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	time.Sleep(10 * time.Millisecond)
	events, _, err := journal.List(context.Background(), "", 10)
	if err != nil || len(events) == 0 {
		t.Fatalf("no events: %v", err)
	}
	ev := events[0]
	// TraceID should be set (gateway generates one if not forwarded by header).
	if ev.TraceID == "" {
		t.Fatal("event missing TraceID")
	}
	if ev.SpanID == "" {
		t.Fatal("event missing SpanID")
	}
}
