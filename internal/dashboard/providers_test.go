package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/settings"
)

func TestProvidersHandlerRendersSafeIntegrationValues(t *testing.T) {
	store := testSettingsStore(t)
	h := ProvidersHandler(store, "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/providers?onboarding=1", nil))
	body := rec.Body.String()
	for _, want := range []string{"Providers", "http://127.0.0.1:11434/v1", "qwen", "OPENAI_BASE_URL", "VIRGIL_RUN_TOKEN", "Add your first provider", "Codex CLI pilot", "wire_api=\"responses\"", "desktop app session is not supervised"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "secret-value") {
		t.Fatal("credential value rendered")
	}
}

func TestProvidersHandlerUsesConfiguredGatewayAddress(t *testing.T) {
	store := testSettingsStore(t)
	if err := store.Update(context.Background(), func(cfg *config.Config) error {
		cfg.Server.Listen = "127.0.0.1:9999"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	ProvidersHandler(store, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/providers", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "OPENAI_BASE_URL=http://127.0.0.1:9999/v1") || strings.Contains(body, "127.0.0.1:8787/v1") {
		t.Fatal("integration instructions use wrong gateway address")
	}
}

func TestProvidersHandlerRejectsSecretValuesAndRequiresCSRF(t *testing.T) {
	store := testSettingsStore(t)
	h := ProvidersHandler(store, "")
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/dashboard/providers", nil))
	token := extractCSRF(t, get.Body.String())
	for _, tc := range []struct {
		name   string
		form   url.Values
		status int
	}{
		{"missing csrf", url.Values{"provider_name": {"openai"}}, http.StatusForbidden},
		{"credential value", url.Values{"csrf_token": {token}, "provider_name": {"openai"}, "provider_type": {"openai-compatible"}, "provider_model": {"gpt"}, "provider_base_url": {"https://api.example.com/v1"}, "provider_api_key_env": {"sk-secret-value"}}, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/dashboard/providers", strings.NewReader(tc.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			h.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tc.name == "credential value" && !strings.Contains(rec.Body.String(), "environment variable name") {
				t.Fatal("secret-shaped value was not rejected")
			}
		})
	}
}

func TestProvidersHandlerSavesEnvironmentReferenceWithoutSecret(t *testing.T) {
	store := testSettingsStore(t)
	h := ProvidersHandler(store, "")
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/dashboard/providers", nil))
	token := extractCSRF(t, get.Body.String())
	form := url.Values{"csrf_token": {token}, "provider_name": {"remote"}, "provider_type": {"openai-compatible"}, "provider_model": {"model-x"}, "provider_base_url": {"https://api.example.com/v1"}, "provider_local": {"false"}, "provider_api_key_env": {"REMOTE_API_KEY"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/dashboard/providers", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Configuration saved. Restart Virgil to apply changes.") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers["remote"].APIKeyEnv != "REMOTE_API_KEY" {
		t.Fatalf("provider=%+v", cfg.Providers["remote"])
	}
}

func TestProvidersHandlerPreservesCapabilitiesForEditedProvider(t *testing.T) {
	store := testSettingsStore(t)
	if err := store.Update(context.Background(), func(cfg *config.Config) error {
		p := cfg.Providers["local"]
		p.Capabilities = []string{"stream", "tools"}
		cfg.Providers["local"] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := ProvidersHandler(store, "")
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/dashboard/providers", nil))
	token := extractCSRF(t, get.Body.String())
	form := url.Values{"csrf_token": {token}, "provider_name": {"local"}, "provider_type": {"openai-compatible"}, "provider_model": {"qwen-new"}, "provider_base_url": {"http://127.0.0.1:11434/v1"}, "provider_local": {"true"}, "provider_api_key_env": {"LOCAL_KEY"}}
	rec := postForm(h, "/dashboard/providers", form, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	cfg, _ := store.Load()
	if len(cfg.Providers["local"].Capabilities) != 2 {
		t.Fatalf("capabilities=%v", cfg.Providers["local"].Capabilities)
	}
}

func TestProvidersHandlerEnforcesLocalAndRemoteNetworkBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, base, local string }{{"local rejects remote", "https://example.com/v1", "true"}, {"remote rejects loopback", "http://127.0.0.1:11434/v1", "false"}} {
		t.Run(tc.name, func(t *testing.T) {
			store := testSettingsStore(t)
			h := ProvidersHandler(store, "")
			get := httptest.NewRecorder()
			h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/dashboard/providers", nil))
			form := url.Values{"csrf_token": {extractCSRF(t, get.Body.String())}, "provider_name": {"test"}, "provider_type": {"openai-compatible"}, "provider_model": {"m"}, "provider_base_url": {tc.base}, "provider_local": {tc.local}}
			rec := postForm(h, "/dashboard/providers", form, nil)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestProvidersCSRFIsBoundToAuthenticatedSession(t *testing.T) {
	store := testSettingsStore(t)
	h := ProvidersHandler(store, "password")
	cookieA := loginCookie(t, "password")
	cookieB := loginCookie(t, "password")
	get := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/providers", nil)
	req.AddCookie(cookieA)
	h.ServeHTTP(get, req)
	token := extractCSRF(t, get.Body.String())
	form := url.Values{"csrf_token": {token}, "provider_name": {"local"}, "provider_type": {"openai-compatible"}, "provider_model": {"qwen"}, "provider_base_url": {"http://127.0.0.1:11434/v1"}, "provider_local": {"true"}, "provider_api_key_env": {"LOCAL_KEY"}}
	if rec := postForm(h, "/dashboard/providers", form, cookieB); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-session status=%d", rec.Code)
	}
	if rec := postForm(h, "/dashboard/providers", form, cookieA); rec.Code != http.StatusOK {
		t.Fatalf("same-session status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func loginCookie(t *testing.T, password string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	ok, err := Login(rec, password, password)
	if err != nil || !ok {
		t.Fatalf("login: ok=%v err=%v", ok, err)
	}
	return rec.Result().Cookies()[0]
}
func postForm(h http.Handler, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	h.ServeHTTP(rec, req)
	return rec
}

func testSettingsStore(t *testing.T) *settings.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "virgil.toml")
	raw := "[server]\nlisten='127.0.0.1:8787'\nlog_level='info'\n[storage]\npath='./data/test.db'\n[providers.local]\ntype='openai-compatible'\nbase_url='http://127.0.0.1:11434/v1'\nmodel='qwen'\napi_key='${LOCAL_KEY}'\nlocal=true\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path, os.Getenv); err != nil {
		t.Fatal(err)
	}
	return settings.New(path)
}
