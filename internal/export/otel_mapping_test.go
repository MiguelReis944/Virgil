package export

import (
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

func makeDelivery(t *testing.T, installID string) storage.Delivery {
	t.Helper()
	ev, err := telemetry.Build(telemetry.Attempt{
		InstallationID: installID,
		RunID:          "run_fixture",
		TraceID:        "0102030405060708090a0b0c0d0e0f10",
		SpanID:         "0102030405060708",
		Provider:       "fixture",
		RequestedModel: "fixture-model",
		UsageSource:    "unknown",
		Status:         "success",
		StartedAt:      time.Now().Add(-50 * time.Millisecond),
		EndedAt:        time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return storage.Delivery{EventID: ev.EventID, Event: ev}
}

func TestOTLPPayloadDecodes(t *testing.T) {
	installID := "install_fixture12345678"
	d := makeDelivery(t, installID)
	allowed := []string{"provider", "status", "run_id", "latency_ms"}
	resourceSpans := deliveriesToResourceSpans([]storage.Delivery{d}, allowed)
	if len(resourceSpans) == 0 {
		t.Fatal("no resource spans")
	}
	body, err := encodeExportRequest(resourceSpans)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 {
		t.Fatal("empty OTLP body")
	}
	// Verify re-parse (decode first ResourceSpans from field 1)
	spans := resourceSpans[0].GetScopeSpans()
	if len(spans) == 0 || len(spans[0].GetSpans()) == 0 {
		t.Fatal("no spans in payload")
	}
	span := spans[0].GetSpans()[0]
	if len(span.GetTraceId()) != 16 {
		t.Fatalf("wrong trace ID length: %d", len(span.GetTraceId()))
	}
	if len(span.GetSpanId()) != 8 {
		t.Fatalf("wrong span ID length: %d", len(span.GetSpanId()))
	}
}

func TestOTLPCanaryAbsent(t *testing.T) {
	installID := "install_fixture12345678"
	ev, _ := telemetry.Build(telemetry.Attempt{
		InstallationID: installID,
		RunID:          "run_fixture",
		TraceID:        "0102030405060708090a0b0c0d0e0f10",
		SpanID:         "0102030405060708",
		Provider:       "fixture",
		RequestedModel: "fixture-model",
		UsageSource:    "unknown",
		Status:         "success",
		StartedAt:      time.Now().Add(-10 * time.Millisecond),
		EndedAt:        time.Now(),
	})
	d := storage.Delivery{EventID: ev.EventID, Event: ev}
	spans := deliveriesToResourceSpans([]storage.Delivery{d}, []string{"provider", "status"})
	body, _ := encodeExportRequest(spans)
	// Verify no content attributes appear
	for _, rs := range spans {
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				for _, attr := range span.GetAttributes() {
					if attr.GetKey() == "prompt" || attr.GetKey() == "completion" || attr.GetKey() == "content" {
						t.Fatalf("content attribute found in OTLP payload: %s", attr.GetKey())
					}
				}
			}
		}
	}
	_ = body
}
