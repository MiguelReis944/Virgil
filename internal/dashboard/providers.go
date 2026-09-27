package dashboard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/providers"
	"github.com/MiguelReis944/Virgil/internal/settings"
)

var settingName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var providerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

type csrfGuard struct{ secret [32]byte }

func newCSRFGuard() *csrfGuard {
	g := &csrfGuard{}
	if _, err := rand.Read(g.secret[:]); err != nil {
		panic("dashboard csrf entropy unavailable")
	}
	return g
}
func (g *csrfGuard) identity(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value
	}
	return "open-loopback-session"
}
func (g *csrfGuard) token(r *http.Request) string {
	mac := hmac.New(sha256.New, g.secret[:])
	_, _ = mac.Write([]byte(g.identity(r)))
	return hex.EncodeToString(mac.Sum(nil))
}
func (g *csrfGuard) valid(r *http.Request, candidate string) bool {
	expected, err := hex.DecodeString(g.token(r))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(candidate)
	return err == nil && hmac.Equal(expected, got)
}

type providerView struct {
	Name, Type, BaseURL, Model, APIKeyEnv, Location, ResponsesBackend, Preset string
	CredentialConfigured, NoKeyRequired                                       bool
}

type providerPreset struct {
	Type, BaseURL, ResponsesBackend, Model string
	Local                                  bool
}

var providerPresets = map[string]providerPreset{
	"nvidia":    {Type: "openai-compatible", BaseURL: "https://integrate.api.nvidia.com/v1", ResponsesBackend: "chat-completions", Model: "meta/muse-glimmer-30b"},
	"openai":    {Type: "openai", BaseURL: "https://api.openai.com/v1"},
	"anthropic": {Type: "anthropic", BaseURL: "https://api.anthropic.com/v1"},
	"kimi":      {Type: "kimi", BaseURL: "https://api.moonshot.ai/v1"},
	"ollama":    {Type: "ollama", BaseURL: "http://127.0.0.1:11434/v1", Local: true},
}

func presetForProvider(p config.ProviderConfig) string {
	for name, preset := range providerPresets {
		if p.Type == preset.Type && p.BaseURL == preset.BaseURL && p.ResponsesBackend == preset.ResponsesBackend && p.Local == preset.Local {
			return name
		}
	}
	return "custom"
}

func credentialEnvName(name string) string {
	sum := sha256.Sum256([]byte(name))
	clean := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
	return "VIRGIL_PROVIDER_" + clean + "_" + hex.EncodeToString(sum[:4]) + "_API_KEY"
}

type providersPage struct {
	Providers   []providerView
	CSRF, Error string
	Onboarding  bool
	GatewayURL  string
	GatewayRoot string
}

func ProvidersHandler(store *settings.Store, password string) http.Handler {
	csrf := newCSRFGuard()
	return requirePanelAuth(password, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := providersPage{CSRF: csrf.token(r), Onboarding: r.URL.Query().Get("onboarding") != ""}
		current, err := store.Load()
		if err != nil {
			slog.Error("load provider settings failed", "error", err)
			http.Error(w, "Could not load configuration.", http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodPost {
			w.Header().Set("Cache-Control", "no-store")
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			if !csrf.valid(r, r.FormValue("csrf_token")) {
				http.Error(w, "invalid CSRF token", http.StatusForbidden)
				return
			}
			providers, err := parseProviders(r.Form, current.Providers)
			if err == nil {
				for i, rawName := range r.Form["provider_name"] {
					name := strings.TrimSpace(rawName)
					if name == "" || i >= len(r.Form["provider_api_key"]) || r.Form["provider_api_key"][i] == "" {
						continue
					}
					provider, exists := providers[name]
					if !exists {
						continue
					}
					err = store.SaveCredential(r.Context(), provider.APIKeyEnv, r.Form["provider_api_key"][i])
					if err != nil {
						page.Error = "Could not save the API key. Remove spaces around it and line breaks."
						break
					}
				}
				if err == nil {
					err = store.Update(r.Context(), func(cfg *config.Config) error { cfg.Providers = providers; return nil })
				}
				if err != nil {
					slog.Error("save provider settings failed", "error", err)
					if page.Error == "" {
						page.Error = "Could not save the provider. Check the Virgil logs."
					}
				}
			}
			if err != nil {
				if page.Error == "" {
					page.Error = err.Error()
				}
				setPanelHeaders(w.Header())
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusUnprocessableEntity)
				renderProviders(w, password, page, store)
				return
			}
			page.Onboarding = false
			if err := renderProvidersWithFlash(w, password, page, store, "Configuration saved. Restart Virgil to apply changes."); err != nil {
				http.Error(w, "render error", 500)
			}
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		renderProviders(w, password, page, store)
	}))
}

func renderProviders(w http.ResponseWriter, password string, page providersPage, store *settings.Store) {
	if err := renderProvidersWithFlash(w, password, page, store, ""); err != nil {
		http.Error(w, "render error", 500)
	}
}
func renderProvidersWithFlash(w http.ResponseWriter, password string, page providersPage, store *settings.Store, flash string) error {
	cfg, err := store.Load()
	if err != nil {
		return err
	}
	page.GatewayURL = "http://" + cfg.Server.Listen + "/v1"
	page.GatewayRoot = "http://" + cfg.Server.Listen
	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := cfg.Providers[n]
		location := "Remote"
		if p.Local {
			location = "Local"
		}
		page.Providers = append(page.Providers, providerView{Name: n, Type: p.Type, BaseURL: p.BaseURL, Model: p.Model, APIKeyEnv: p.APIKeyEnv, Location: location, ResponsesBackend: p.ResponsesBackend, Preset: presetForProvider(p), CredentialConfigured: store.HasCredential(p.APIKeyEnv), NoKeyRequired: p.Local && p.APIKeyEnv == ""})
	}
	if len(page.Providers) == 0 {
		page.Onboarding = true
	}
	return renderPage(w, pageData{Title: "Providers", ActiveSection: sectionProviders, HasAuth: password != "", Flash: flash}, providersBody, page)
}

func parseProviders(form url.Values, existing map[string]config.ProviderConfig) (map[string]config.ProviderConfig, error) {
	names := form["provider_name"]
	out := make(map[string]config.ProviderConfig)
	value := func(key string, i int) string {
		values := form[key]
		if i >= len(values) {
			return ""
		}
		return strings.TrimSpace(values[i])
	}
	for i, raw := range names {
		name := strings.TrimSpace(raw)
		if value("provider_remove", i) == "true" {
			continue
		}
		if name == "" {
			continue
		}
		if !providerName.MatchString(name) {
			return nil, fmt.Errorf("invalid provider name")
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("provider name is already in use")
		}
		env := value("provider_api_key_env", i)
		if env != "" && !settingName.MatchString(env) {
			return nil, fmt.Errorf("credential must be an environment variable name, not a secret value")
		}
		typ := value("provider_type", i)
		base := value("provider_base_url", i)
		local := value("provider_local", i) == "true"
		responsesBackend := value("provider_responses_backend", i)
		presetName := value("provider_preset", i)
		if presetName != "" && presetName != "custom" {
			preset, found := providerPresets[presetName]
			if !found {
				return nil, fmt.Errorf("unsupported provider service")
			}
			typ, base, local, responsesBackend = preset.Type, preset.BaseURL, preset.Local, preset.ResponsesBackend
		}
		switch typ {
		case "openai-compatible", "openai", "kimi", "anthropic", "ollama":
		default:
			return nil, fmt.Errorf("unsupported provider type")
		}
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return nil, fmt.Errorf("invalid provider base URL")
		}
		model := value("provider_model", i)
		if model == "" {
			model = providerPresets[presetName].Model
		}
		if model == "" {
			return nil, fmt.Errorf("provider model is required")
		}
		provider := existing[name]
		if env == "" && i < len(form["provider_api_key"]) && form["provider_api_key"][i] != "" {
			env = credentialEnvName(name)
		}
		provider.Type, provider.BaseURL, provider.Model, provider.APIKeyEnv, provider.APIKey, provider.Local = typ, base, model, env, "", local
		provider.ResponsesBackend = responsesBackend
		out[name] = provider
	}
	if err := providers.ValidateConfig(config.Config{Providers: out}); err != nil {
		return nil, fmt.Errorf("provider settings are invalid: %v", err)
	}
	return out, nil
}
