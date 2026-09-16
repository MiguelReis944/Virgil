package gateway

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func chatFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "chat_request.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func chatServer(t *testing.T, upstreamURL string, getenv func(string) string) http.Handler {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"fixture": {Type: "openai-compatible", BaseURL: upstreamURL + "/v1", Model: "fixture-model", APIKeyEnv: "TEST_PROVIDER_KEY"},
		},
	}
	handler, err := NewServer(cfg, Dependencies{DB: db, Client: http.DefaultClient, Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestChatJSONForwarding(t *testing.T) {
	var seenBody []byte
	var seenKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		seenKey = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl_fixture","object":"chat.completion","model":"fixture-model","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_fixture","type":"function","function":{"name":"fixture_tool","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`))
	}))
	defer upstream.Close()
	handler := chatServer(t, upstream.URL, func(string) string { return "" })
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatFixture(t)))
	req.Header.Set("Authorization", "Bearer synthetic-pass-through")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(bytes.TrimSpace(seenBody), bytes.TrimSpace(chatFixture(t))) || seenKey != "Bearer synthetic-pass-through" {
		t.Fatalf("request not forwarded correctly: key=%q body=%s", seenKey, seenBody)
	}
	if !strings.Contains(rec.Body.String(), "call_fixture") {
		t.Fatalf("tool call missing: %s", rec.Body.String())
	}
}

func TestConfiguredKeyRequiresAppToken(t *testing.T) {
	var keys []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl_fixture","object":"chat.completion","model":"fixture-model","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0}}`))
	}))
	defer upstream.Close()
	getenv := func(name string) string {
		switch name {
		case "VIRGIL_LOCAL_APP_TOKEN":
			return "synthetic-local-token"
		case "TEST_PROVIDER_KEY":
			return "synthetic-configured-key"
		}
		return ""
	}
	handler := chatServer(t, upstream.URL, getenv)
	for _, tc := range []struct {
		bearer string
		status int
		key    string
	}{
		{"", 401, ""},
		{"synthetic-arbitrary-bearer", 200, "Bearer synthetic-arbitrary-bearer"},
		{"synthetic-local-token", 200, "Bearer synthetic-configured-key"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatFixture(t)))
		if tc.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("bearer %q: status=%d body=%s", tc.bearer, rec.Code, rec.Body.String())
		}
		if tc.key != "" && keys[len(keys)-1] != tc.key {
			t.Fatalf("bearer %q unlocked %q", tc.bearer, keys[len(keys)-1])
		}
	}
}

func TestUnsupportedFeatureDoesNotDispatch(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer upstream.Close()
	handler := chatServer(t, upstream.URL, func(string) string { return "" })
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}],"audio":{"format":"wav"}}`))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 400 || calls.Load() != 0 {
		t.Fatalf("status=%d provider calls=%d", rec.Code, calls.Load())
	}
}
