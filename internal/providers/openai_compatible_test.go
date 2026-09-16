package providers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOpenAICompatibleRejectsUnsupportedField(t *testing.T) {
	adapter := NewOpenAICompatible("https://example.invalid/v1", http.DefaultClient)
	body := []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}],"audio":{"format":"wav"}}`)
	if err := adapter.Validate(body); err == nil {
		t.Fatal("accepted unsupported audio field")
	}
}

func TestOpenAICompatibleBuildUsesOnlyResolvedKey(t *testing.T) {
	adapter := NewOpenAICompatible("https://example.invalid/v1", http.DefaultClient)
	body := fixture(t, "chat_request.json")
	req, err := adapter.Build(t.Context(), body, "synthetic-provider-key")
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://example.invalid/v1/chat/completions" {
		t.Fatalf("upstream URL = %q", req.URL)
	}
	if req.Header.Get("Authorization") != "Bearer synthetic-provider-key" {
		t.Fatal("missing resolved provider key")
	}
	for name := range req.Header {
		if name != "Authorization" && name != "Content-Type" {
			t.Fatalf("unexpected forwarded header %q", name)
		}
	}
}

func TestOpenAICompatibleTranslatePreservesToolCallsAndUsage(t *testing.T) {
	adapter := NewOpenAICompatible("https://example.invalid/v1", http.DefaultClient)
	raw := fixture(t, "chat_response.json")
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: http.Header{"Content-Type": []string{"application/json"}}}
	rec := httptest.NewRecorder()
	result, err := adapter.Translate(resp, rec)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	choices := got["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if len(message["tool_calls"].([]any)) != 1 {
		t.Fatal("tool call lost")
	}
	if result.ResponseModel != "fixture-model" || result.InputTokens == nil || *result.InputTokens != 5 || result.OutputTokens == nil || *result.OutputTokens != 3 {
		t.Fatalf("result = %+v", result)
	}
}

func TestOpenAICompatibleNormalizesProviderError(t *testing.T) {
	adapter := NewOpenAICompatible("https://example.invalid/v1", http.DefaultClient)
	resp := &http.Response{StatusCode: 429, Body: io.NopCloser(bytes.NewReader([]byte(`{"error":{"message":"synthetic secret canary"}}`)))}
	rec := httptest.NewRecorder()
	result, err := adapter.Translate(resp, rec)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != 429 || bytes.Contains(rec.Body.Bytes(), []byte("canary")) {
		t.Fatalf("unsafe response: %d %s", rec.Code, rec.Body.String())
	}
	if result.ErrorCode != "provider_http_429" {
		t.Fatalf("code = %q", result.ErrorCode)
	}
}

func TestOpenAICompatibleExtractsCachedInputTokens(t *testing.T) {
	adapter := NewOpenAICompatible("https://example.invalid/v1", http.DefaultClient)
	raw := []byte(`{"id":"chatcmpl_fixture","object":"chat.completion","model":"fixture-model","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":4}}}`)
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}
	result, err := adapter.Translate(resp, httptest.NewRecorder())
	if err != nil {
		t.Fatal(err)
	}
	if result.CachedTokens == nil || *result.CachedTokens != 4 || result.InputTokens == nil || *result.InputTokens != 12 {
		t.Fatalf("cached input usage lost: %+v", result)
	}
}

func TestOpenAICompatibleDoesNotForwardKeyAcrossRedirect(t *testing.T) {
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected = true
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	adapter := NewOpenAICompatible(source.URL+"/v1", http.DefaultClient)
	req, err := adapter.Build(t.Context(), fixture(t, "chat_request.json"), "synthetic-provider-key")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := adapter.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if redirected || resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("provider redirect followed: redirected=%t status=%d", redirected, resp.StatusCode)
	}
}

func TestOpenAICompatibleRejectsURLUserinfo(t *testing.T) {
	adapter := NewOpenAICompatible("https://user:synthetic-secret@example.invalid/v1", http.DefaultClient)
	if _, err := adapter.Build(t.Context(), fixture(t, "chat_request.json"), "synthetic-provider-key"); err == nil {
		t.Fatal("accepted provider URL with userinfo")
	}
}
