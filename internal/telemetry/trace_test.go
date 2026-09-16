package telemetry

import (
	"strings"
	"testing"
)

func TestIncomingTraceparentPreserved(t *testing.T) {
	trace, err := NewTrace("00-00000000000000000000000000000001-0000000000000002-01")
	if err != nil {
		t.Fatal(err)
	}
	if trace.TraceID != "00000000000000000000000000000001" || trace.ParentSpanID != "0000000000000002" {
		t.Fatalf("lost parent trace: %+v", trace)
	}
	if len(trace.SpanID) != 16 || trace.SpanID == trace.ParentSpanID {
		t.Fatalf("invalid child span: %+v", trace)
	}
}

func TestInvalidTraceparentRegenerated(t *testing.T) {
	for _, parent := range []string{
		"",
		"00-00000000000000000000000000000000-0000000000000001-01",
		"00-00000000000000000000000000000001-0000000000000000-01",
		"ff-00000000000000000000000000000001-0000000000000002-01",
		"not-a-trace",
	} {
		trace, err := NewTrace(parent)
		if err != nil {
			t.Fatal(err)
		}
		if len(trace.TraceID) != 32 || len(trace.SpanID) != 16 || strings.Trim(trace.TraceID, "0") == "" || strings.Trim(trace.SpanID, "0") == "" {
			t.Fatalf("bad generated trace: %+v", trace)
		}
		if trace.ParentSpanID != "" {
			t.Fatalf("invalid parent retained: %+v", trace)
		}
	}
}
