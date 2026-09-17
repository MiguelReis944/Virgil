package redaction

import (
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

func fixtureEvent(t *testing.T) telemetry.Event {
	t.Helper()
	now := time.Now()
	event, err := telemetry.Build(telemetry.Attempt{
		InstallationID: "install_fixture",
		RunID:          "run_fixture",
		TraceID:        "00000000000000000000000000000001",
		SpanID:         "0000000000000001",
		Provider:       "fixture",
		RequestedModel: "fixture-model",
		UsageSource:    "unknown",
		Status:         "success",
		StartedAt:      now,
		EndedAt:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestPrepareRejectsFreeTextMetadata(t *testing.T) {
	event := fixtureEvent(t)
	event.ResponseModel = "private canary in response model"
	if _, err := Prepare(event); err == nil {
		t.Fatal("accepted free text response model")
	}
	event = fixtureEvent(t)
	event.PolicyDecision = &telemetry.PolicyDecision{
		Decision: "block", Reason: "private canary", Policy: "budget", Attempt: 4, Threshold: 3,
	}
	if _, err := Prepare(event); err == nil {
		t.Fatal("accepted free text policy reason")
	}
}

func TestPrepareRejectsContentCapture(t *testing.T) {
	event := fixtureEvent(t)
	event.ContentCapture = true
	if _, err := Prepare(event); err == nil {
		t.Fatal("accepted content capture")
	}
}

func TestForExportRequiresExplicitSafeAllowlist(t *testing.T) {
	event := fixtureEvent(t)
	if _, err := ForExport(event, nil); err == nil {
		t.Fatal("export accepted without allowlist")
	}
	if _, err := ForExport(event, []string{"prompt"}); err == nil {
		t.Fatal("export accepted forbidden field")
	}
	fields, err := ForExport(event, []string{"event_id", "provider", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || fields["provider"] != "fixture" || fields["status"] != "success" {
		t.Fatalf("exported fields=%v", fields)
	}
}
