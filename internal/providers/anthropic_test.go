package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicBuildTranslatesMessagesAndTools(t *testing.T) {
	a := NewAnthropic("https://example.invalid/v1", nil)
	body := []byte(`{"model":"fixture-model","messages":[{"role":"system","content":"Be concise"},{"role":"user","content":"Find it"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"synthetic\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"found"}],"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"lookup"}},"max_tokens":64}`)
	req, err := a.Build(t.Context(), body, "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://example.invalid/v1/messages" || req.Header.Get("x-api-key") != "synthetic-key" || req.Header.Get("anthropic-version") == "" || req.Header.Get("Authorization") != "" {
		t.Fatalf("incorrect upstream request: %s %#v", req.URL, req.Header)
	}
	raw, _ := io.ReadAll(req.Body)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["system"] != "Be concise" || got["max_tokens"] != float64(64) {
		t.Fatalf("request = %s", raw)
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %s", raw)
	}
	call := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if call["type"] != "tool_use" || call["id"] != "call_1" || call["name"] != "lookup" {
		t.Fatalf("tool use = %v", call)
	}
	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "call_1" {
		t.Fatalf("tool result = %v", result)
	}
	if got["tool_choice"].(map[string]any)["type"] != "tool" {
		t.Fatalf("choice = %v", got["tool_choice"])
	}
}

func TestAnthropicRejectsUnsupportedBeforeDispatch(t *testing.T) {
	a := NewAnthropic("https://example.invalid/v1", nil)
	cases := []string{
		`{"model":"m","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`,
		`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.invalid/a"}}]}]}`,
		`{"model":"m","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"x","type":"function","function":{"name":"f","arguments":"not-json"}}]}]}`,
		`{"model":"m","messages":[{"role":"tool","tool_call_id":"x","content":"ok"}]}`,
		`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"missing"}}}`,
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","max_tokens":8,"messages":[{"role":"system","content":"only instructions"}]}`,
		`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","strict":true,"parameters":{"type":"object"}}}]}`,
	}
	for _, body := range cases {
		if err := a.Validate([]byte(body)); err == nil {
			t.Fatalf("accepted unsupported request: %s", body)
		}
	}
}

func TestAnthropicTranslateToolUseAndUsage(t *testing.T) {
	a := NewAnthropic("https://example.invalid/v1", nil)
	raw := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"fixture-model","content":[{"type":"text","text":"Done"},{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"synthetic"}}],"stop_reason":"tool_use","usage":{"input_tokens":12,"output_tokens":4,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}`)
	rec := httptest.NewRecorder()
	result, err := a.Translate(&http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, rec)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct{ Name, Arguments string } `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Choices[0].FinishReason != "tool_calls" || got.Choices[0].Message.ToolCalls[0].Function.Arguments != `{"q":"synthetic"}` {
		t.Fatalf("response = %s", rec.Body.String())
	}
	if result.InputTokens == nil || *result.InputTokens != 17 || result.OutputTokens == nil || *result.OutputTokens != 4 || result.CachedTokens == nil || *result.CachedTokens != 3 || len(result.ToolCallFingerprints) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestAnthropicTranslateMissingUsageAndSanitizedError(t *testing.T) {
	a := NewAnthropic("https://example.invalid/v1", nil)
	rec := httptest.NewRecorder()
	result, err := a.Translate(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"m","model":"fixture-model","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`))}, rec)
	if err != nil || result.UsageSource != "unknown" || result.InputTokens != nil {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	rec = httptest.NewRecorder()
	result, err = a.Translate(&http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"synthetic-key canary"}}`))}, rec)
	if err != nil || rec.Code != 429 || strings.Contains(rec.Body.String(), "canary") || result.ErrorCode != "provider_http_429" {
		t.Fatalf("error = %+v %s %v", result, rec.Body.String(), err)
	}
}

func TestAnthropicStreamTranslatesTextToolAndUsage(t *testing.T) {
	a := NewAnthropic("https://example.invalid/v1", nil)
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"fixture-model\",\"usage\":{\"input_tokens\":7,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"lookup\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"q\\\":\\\"synthetic\\\"}\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":4}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	rec := httptest.NewRecorder()
	result, err := a.Stream(context.Background(), &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sse))}, rec)
	if err != nil {
		t.Fatal(err)
	}
	out := rec.Body.String()
	for _, part := range []string{`"content":"Hello"`, `"name":"lookup"`, `"arguments":"{\"q\":\"synthetic\"}"`, `"finish_reason":"tool_calls"`, `data: [DONE]`} {
		if !strings.Contains(out, part) {
			t.Fatalf("missing %s in %s", part, out)
		}
	}
	if result.InputTokens == nil || *result.InputTokens != 7 || result.OutputTokens == nil || *result.OutputTokens != 4 || len(result.ToolCallFingerprints) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestAnthropicDoesNotForwardKeyAcrossRedirect(t *testing.T) {
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "synthetic-key" {
			t.Error("missing provider key")
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	a := NewAnthropic(source.URL+"/v1", nil)
	req, err := a.Build(t.Context(), []byte(`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hello"}]}`), "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if redirected || resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("redirected=%t status=%d", redirected, resp.StatusCode)
	}
}

func TestAnthropicDoHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer server.Close()
	a := NewAnthropic(server.URL+"/v1", nil)
	ctx, cancel := context.WithCancel(t.Context())
	req, err := a.Build(ctx, []byte(`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hello"}]}`), "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		resp, err := a.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		finished <- err
	}()
	<-started
	cancel()
	defer close(release)
	if err := <-finished; err == nil {
		t.Fatal("cancelled request completed")
	}
}

func TestAnthropicUsageRejectsOverflow(t *testing.T) {
	result := Result{UsageSource: "unknown"}
	large := int64(9223372036854775807)
	if err := applyAnthropicUsage(&result, &anthropicUsage{InputTokens: &large, CacheReadInputTokens: &large}); err == nil {
		t.Fatal("accepted overflowing token sum")
	}
}

func TestAnthropicStreamEmitsNormalizedErrorAfterHeaders(t *testing.T) {
	a := NewAnthropic("https://example.invalid/v1", nil)
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"m\",\"usage\":{\"input_tokens\":1}}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":-1}}\n\n"
	rec := httptest.NewRecorder()
	result, err := a.Stream(t.Context(), &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sse))}, rec)
	if err == nil || result.Status != "provider_error" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !strings.Contains(rec.Body.String(), "event: error") || strings.Contains(rec.Body.String(), "-1") {
		t.Fatalf("unsafe stream: %s", rec.Body.String())
	}
}

func TestAnthropicStreamUsageOption(t *testing.T) {
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"m\",\"usage\":{\"input_tokens\":2}}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Content-Type", "text/event-stream"); io.WriteString(w, sse) }))
	defer server.Close()
	for _, tc := range []struct { name, options string; wantUsage bool }{{"included", `,"stream_options":{"include_usage":true}`, true}, {"omitted", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAnthropic(server.URL+"/v1", nil)
			body := []byte(`{"model":"m","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"hi"}]`+tc.options+`}`)
			req, err := a.Build(t.Context(), body, "synthetic-key"); if err != nil { t.Fatal(err) }
			resp, err := a.Do(req); if err != nil { t.Fatal(err) }
			rec := httptest.NewRecorder()
			if _, err := a.Stream(t.Context(), resp, rec); err != nil { t.Fatal(err) }
			out := rec.Body.String()
			hasFinalUsage := strings.Contains(out, `"choices":[],"id":"msg_1"`) && strings.Contains(out, `"prompt_tokens":2`)
			if hasFinalUsage != tc.wantUsage { t.Fatalf("final usage=%t, stream=%s", hasFinalUsage, out) }
			if !tc.wantUsage && strings.Contains(out, `"usage":`) { t.Fatalf("unexpected usage: %s", out) }
		})
	}
}

func TestAnthropicRejectsTotalUsageOverflow(t *testing.T) {
	a := NewAnthropic("https://example.invalid/v1", nil)
	raw := `{"id":"m","model":"m","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":9223372036854775807,"output_tokens":1}}`
	rec := httptest.NewRecorder()
	if _, err := a.Translate(&http.Response{StatusCode:200,Body:io.NopCloser(strings.NewReader(raw))}, rec); err == nil { t.Fatalf("accepted overflow: %s", rec.Body.String()) }
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"m\",\"usage\":{\"input_tokens\":9223372036854775807}}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"
	rec = httptest.NewRecorder()
	result, err := a.Stream(t.Context(), &http.Response{StatusCode:200,Body:io.NopCloser(strings.NewReader(sse))}, rec)
	if err == nil || result.Status != "provider_error" || !strings.Contains(rec.Body.String(), "event: error") { t.Fatalf("accepted SSE overflow: %+v %v %s", result, err, rec.Body.String()) }
}

func TestAnthropicStreamEmptyToolInputIsObject(t *testing.T) {
	a := NewAnthropic("https://example.invalid/v1", nil)
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"m\"}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"lookup\",\"input\":{}}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	rec := httptest.NewRecorder()
	if _, err := a.Stream(t.Context(), &http.Response{StatusCode:200,Body:io.NopCloser(strings.NewReader(sse))}, rec); err != nil { t.Fatal(err) }
	if !strings.Contains(rec.Body.String(), `"arguments":"{}"`) { t.Fatalf("invalid empty arguments: %s", rec.Body.String()) }
}
