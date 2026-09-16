package telemetry

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildMeasuredAttempt(t *testing.T) {
	start := time.Now()
	end := start.Add(125 * time.Millisecond)
	input := int64(7)
	output := int64(3)
	event, err := Build(Attempt{
		InstallationID: "install_fixture",
		RunID:          "run_fixture",
		TraceID:        "00000000000000000000000000000001",
		SpanID:         "0000000000000001",
		Provider:       "fixture",
		RequestedModel: "fixture-model",
		ResponseModel:  "fixture-model",
		InputTokens:    &input,
		OutputTokens:   &output,
		UsageSource:    "provider",
		Status:         "success",
		StartedAt:      start,
		EndedAt:        end,
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.SchemaVersion != "1.0" || event.LatencyMS != 125 || event.ContentCapture || event.InputTokens == nil || *event.InputTokens != 7 {
		t.Fatalf("event=%+v", event)
	}
	if len(event.EventID) != 32 || event.CreatedAt.Location() != time.UTC {
		t.Fatalf("invalid event identity: %+v", event)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"prompt", "response_body", "tool_arguments", "authorization", "chain_of_thought"} {
		if bytes.Contains(bytes.ToLower(encoded), []byte(forbidden)) {
			t.Fatalf("content field %q in %s", forbidden, encoded)
		}
	}
}

func TestBuildRejectsInvalidUsageAndStatus(t *testing.T) {
	now := time.Now()
	base := Attempt{
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
	}
	negative := int64(-1)
	tooManyCached := int64(3)
	twoInput := int64(2)
	cases := []Attempt{
		func() Attempt { a := base; a.InputTokens = &negative; return a }(),
		func() Attempt { a := base; a.InputTokens = &twoInput; a.CachedTokens = &tooManyCached; return a }(),
		func() Attempt { a := base; a.Status = "secret-text"; return a }(),
		func() Attempt { a := base; a.UsageSource = "provider"; return a }(),
		func() Attempt { a := base; a.Provider = strings.Repeat("x", 65); return a }(),
		func() Attempt { a := base; a.ResponseModel = "model\nprivate canary"; return a }(),
	}
	for _, attempt := range cases {
		if _, err := Build(attempt); err == nil {
			t.Fatalf("accepted invalid attempt: %+v", attempt)
		}
	}
}

func TestDecodeEventRejectsContentField(t *testing.T) {
	raw := `{"schema_version":"1.0","prompt":"synthetic private canary"}`
	if _, err := DecodeEvent(strings.NewReader(raw)); err == nil {
		t.Fatal("accepted prompt field")
	}
}

func TestDecodeEventRejectsContentCaptureTrue(t *testing.T) {
	raw := `{"schema_version":"1.0","content_capture":true}`
	if _, err := DecodeEvent(strings.NewReader(raw)); err == nil {
		t.Fatal("accepted content capture")
	}
}
