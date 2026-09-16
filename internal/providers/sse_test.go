package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSSEForwardsToolDeltaAndUsage(t *testing.T) {
	adapter := NewOpenAICompatible("https://example.invalid/v1", http.DefaultClient)
	body := strings.Join([]string{
		`data: {"id":"chunk_fixture","model":"fixture-model","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_fixture","function":{"name":"fixture_tool","arguments":"{"}}]}}]}`,
		"",
		`data: {"id":"chunk_fixture","model":"fixture-model","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]}`,
		"",
		`data: {"id":"chunk_fixture","model":"fixture-model","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
		"",
		"data: [DONE]",
		"",
		"",
	}, "\n")
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	rec := httptest.NewRecorder()
	result, err := adapter.Stream(context.Background(), resp, rec)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" || rec.Body.String() != body {
		t.Fatalf("SSE changed: headers=%v body=%q", rec.Header(), rec.Body.String())
	}
	if result.ResponseModel != "fixture-model" || result.UsageSource != "provider" || result.InputTokens == nil || *result.InputTokens != 5 || result.OutputTokens == nil || *result.OutputTokens != 3 {
		t.Fatalf("result=%+v", result)
	}
}

type failingReader struct {
	data []byte
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.data == nil {
		return 0, errors.New("synthetic upstream interruption with private canary")
	}
	n := copy(p, r.data)
	r.data = nil
	return n, nil
}

func (r *failingReader) Close() error { return nil }

func TestLateProviderErrorIsSSE(t *testing.T) {
	adapter := NewOpenAICompatible("https://example.invalid/v1", http.DefaultClient)
	resp := &http.Response{StatusCode: 200, Body: &failingReader{data: []byte("data: {\"model\":\"fixture-model\",\"choices\":[]}\n\n")}}
	rec := httptest.NewRecorder()
	result, err := adapter.Stream(context.Background(), resp, rec)
	if err == nil {
		t.Fatal("missing stream error")
	}
	if !strings.Contains(rec.Body.String(), "event: error") || strings.Contains(rec.Body.String(), "private canary") {
		t.Fatalf("unsafe error frame: %q", rec.Body.String())
	}
	if result.Status != "transport_error" {
		t.Fatalf("status=%q", result.Status)
	}
}

func TestMissingUsageIsUnknown(t *testing.T) {
	adapter := NewOpenAICompatible("https://example.invalid/v1", http.DefaultClient)
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data: {\"model\":\"fixture-model\",\"choices\":[]}\n\ndata: [DONE]\n\n"))}
	rec := httptest.NewRecorder()
	result, err := adapter.Stream(context.Background(), resp, rec)
	if err != nil {
		t.Fatal(err)
	}
	if result.UsageSource != "unknown" || result.InputTokens != nil || result.OutputTokens != nil {
		t.Fatalf("invented usage: %+v", result)
	}
}

func TestProviderErrorEventIsSanitized(t *testing.T) {
	adapter := NewOpenAICompatible("https://example.invalid/v1", http.DefaultClient)
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("event: error\ndata: {\"message\":\"private canary\"}\n\n"))}
	rec := httptest.NewRecorder()
	result, err := adapter.Stream(context.Background(), resp, rec)
	if err == nil || result.Status != "provider_error" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if strings.Contains(rec.Body.String(), "private canary") || !strings.Contains(rec.Body.String(), "event: error") {
		t.Fatalf("unsafe provider error: %q", rec.Body.String())
	}
}
