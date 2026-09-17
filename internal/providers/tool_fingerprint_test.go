package providers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONToolCallFingerprintOmitsArguments(t *testing.T) {
	a := NewOpenAICompatible("http://127.0.0.1:1/v1", nil)
	body := `{"model":"fixture-model","choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"secret\":\"SYNTHETIC_CANARY\"}"}}]}}]}`
	response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	result, err := a.Translate(response, httptest.NewRecorder())
	if err != nil || len(result.ToolCallFingerprints) != 1 || strings.Contains(result.ToolCallFingerprints[0], "SYNTHETIC_CANARY") {
		t.Fatalf("fingerprints=%v err=%v", result.ToolCallFingerprints, err)
	}
}

func TestSSEToolCallFingerprintAcrossDeltas(t *testing.T) {
	result := Result{Status: "success", UsageSource: "unknown"}
	frames := []string{
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"look\",\"arguments\":\"{\\\"secret\\\":\"}}]}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"up\",\"arguments\":\"\\\"SYNTHETIC_CANARY\\\"}\"}}]}}]}\n\n",
		"data: [DONE]\n\n",
	}
	for _, frame := range frames {
		if _, err := inspectSSEFrame([]byte(frame), &result); err != nil {
			t.Fatal(err)
		}
	}
	if len(result.ToolCallFingerprints) != 1 || strings.Contains(result.ToolCallFingerprints[0], "SYNTHETIC_CANARY") {
		t.Fatalf("fingerprints=%v", result.ToolCallFingerprints)
	}
}

func TestSSERejectsUnboundedToolIndex(t *testing.T) {
	result := Result{Status: "success", UsageSource: "unknown"}
	frame := []byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":256,\"function\":{\"name\":\"lookup\"}}]}}]}\n\n")
	if _, err := inspectSSEFrame(frame, &result); err == nil {
		t.Fatal("unbounded tool index accepted")
	}
}

func TestSSEKeepsToolCallsFromDifferentChoicesSeparate(t *testing.T) {
	result := Result{Status: "success", UsageSource: "unknown"}
	frame := []byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"first\",\"arguments\":\"{}\"}}]}},{\"index\":1,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"second\",\"arguments\":\"{}\"}}]}}]}\n\n")
	if _, err := inspectSSEFrame(frame, &result); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectSSEFrame([]byte("data: [DONE]\n\n"), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.ToolCallFingerprints) != 2 || result.ToolCallFingerprints[0] == result.ToolCallFingerprints[1] {
		t.Fatalf("fingerprints=%v", result.ToolCallFingerprints)
	}
}
