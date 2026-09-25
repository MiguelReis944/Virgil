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
	Name, Type, BaseURL, Model, APIKeyEnv, Location string
	CredentialConfigured                            bool
}
type providersPage struct {
	Providers   []providerView
	CSRF, Error string
	Onboarding  bool
	GatewayURL  string
	GatewayRoot string
}

const providersBody = `{{define "content"}}{{if .Onboarding}}<div class="flash">Add your first provider to route supervised AI requests through Virgil.</div>{{end}}{{if .Error}}<div class="form-err">{{.Error}}</div>{{end}}
<div class="card"><div class="card-label">Use Virgil in any project</div><p>Keep one Virgil installation outside your projects. Set <code>VIRGIL_HOME</code> to its absolute directory in the environment that starts Virgil and the agent. Open a terminal at any project root and run Codex or Claude Code there. Virgil keeps <code>virgil.toml</code>, <code>.env</code>, database, and control token in its installation; the agent keeps the project as its working directory. Desktop settings are user-level and apply across local projects.</p></div>
<div class="card"><div class="card-label">Run a protected agent</div><p>1. Save a provider here and set its credential environment variable for the Virgil process, if required. 2. Set a limit in <a href="/dashboard/protections">Protections</a>. 3. Restart Virgil, then launch your agent with <code>virgil run -- &lt;command&gt;</code>. The result appears under <a href="/dashboard/executions">Executions</a>.</p><p>For an OpenAI-compatible client, use <code>OPENAI_BASE_URL={{.GatewayURL}}</code>, <code>OPENAI_API_KEY=$VIRGIL_RUN_TOKEN</code>, and the model ID shown below. <code>virgil run</code> supplies these environment variables to the child. Only requests sent through Virgil are protected.</p></div>
<div class="card"><div class="card-label">Codex CLI pilot</div><p>Codex uses the Responses API. Configure an <code>openai</code> or Responses-capable <code>openai-compatible</code> provider above with a model and provider credential, then restart Virgil. From a project directory, launch Codex below. Replace <code>YOUR_MODEL_ID</code> with the model ID shown above.</p><pre><code>virgil run -- codex -c 'model_provider="virgil"' -c 'model_providers.virgil.name="Virgil"' -c 'model_providers.virgil.base_url="{{.GatewayURL}}"' -c 'model_providers.virgil.env_key="VIRGIL_RUN_TOKEN"' -c 'model_providers.virgil.wire_api="responses"' -c 'model_providers.virgil.requires_openai_auth=false' -m 'YOUR_MODEL_ID'</code></pre><p>Inspect the run in <a href="/dashboard/executions">Executions</a> and costs in <a href="/dashboard/usage">Usage</a>.</p></div>
<div class="card"><div class="card-label">Codex desktop</div><p>Set a separate random <code>VIRGIL_DESKTOP_TOKEN</code> (at least 32 characters) in Virgil's ignored <code>.env</code> and restart the core. Configure a custom Responses provider in your user-level <code>~/.codex/config.toml</code> with <code>base_url = "{{.GatewayURL}}"</code>, <code>env_key = "VIRGIL_DESKTOP_TOKEN"</code>, <code>wire_api = "responses"</code>, and your model ID. Put the same token in <code>~/.codex/.env</code> if the app does not inherit shell variables. Restart the app and verify a new local task in <a href="/dashboard/usage">Usage</a>. Desktop calls can be blocked; Virgil cannot stop the app process.</p></div>
<div class="card"><div class="card-label">Claude Code</div><p>Configure an <code>anthropic</code> provider above. The supervised CLI command is <code>virgil run -- claude</code>; Virgil supplies <code>ANTHROPIC_BASE_URL</code> and <code>ANTHROPIC_AUTH_TOKEN</code>. For Claude desktop, enable Developer Mode and use <strong>Developer → Configure Third-Party Inference</strong> with root URL <code>{{.GatewayRoot}}</code> and the separate desktop token. Check <a href="/dashboard/usage">Usage</a> for desktop requests. An Anthropic API key or another compatible upstream credential is required; a claude.ai subscription is not forwarded by Virgil.</p></div>
<div class="card"><div class="card-label">OmniRoute upstream</div><p>To route through OmniRoute after Virgil, use <code>http://127.0.0.1:20128/v1</code> as an <code>openai-compatible</code> Responses provider or an <code>anthropic</code> Messages provider, with <code>OMNIROUTE_API_KEY</code> set only for the Virgil core. Verify the chosen model works on OmniRoute's HTTP endpoint. Its separate Codex OAuth WebSocket bridge is not handled by Virgil.</p></div>
<form method="post" action="/dashboard/providers"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><div class="table-wrap"><table><thead><tr><th>Name</th><th>Type</th><th>Model ID</th><th>Base URL</th><th>Location</th><th>Credential env name</th></tr></thead><tbody>{{range .Providers}}<tr><td><input name="provider_name" value="{{.Name}}"></td><td><input name="provider_type" value="{{.Type}}"></td><td><input name="provider_model" value="{{.Model}}"></td><td><input name="provider_base_url" value="{{.BaseURL}}"></td><td>{{.Location}}<input type="hidden" name="provider_local" value="{{if eq .Location "Local"}}true{{else}}false{{end}}"></td><td><input name="provider_api_key_env" value="{{.APIKeyEnv}}" placeholder="OPENAI_API_KEY">{{if .CredentialConfigured}}<span class="badge badge-success">configured</span>{{end}}</td></tr>{{else}}<tr><td colspan="6">No providers configured.</td></tr>{{end}}<tr><td><input name="provider_name"></td><td><input name="provider_type" value="openai-compatible"></td><td><input name="provider_model"></td><td><input name="provider_base_url"></td><td><select name="provider_local"><option value="false">Remote</option><option value="true">Local</option></select></td><td><input name="provider_api_key_env" placeholder="OPENAI_API_KEY"></td></tr></tbody></table></div><button class="btn-primary" type="submit">Save providers</button></form>{{end}}`

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
				err = store.Update(r.Context(), func(cfg *config.Config) error { cfg.Providers = providers; return nil })
				if err != nil {
					slog.Error("save provider settings failed", "error", err)
					page.Error = "Could not save configuration. Check the Virgil logs."
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
		page.Providers = append(page.Providers, providerView{Name: n, Type: p.Type, BaseURL: p.BaseURL, Model: p.Model, APIKeyEnv: p.APIKeyEnv, Location: location, CredentialConfigured: p.APIKeyEnv != ""})
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
		if name == "" {
			continue
		}
		if !providerName.MatchString(name) {
			return nil, fmt.Errorf("invalid provider name")
		}
		env := value("provider_api_key_env", i)
		if env != "" && !settingName.MatchString(env) {
			return nil, fmt.Errorf("credential must be an environment variable name, not a secret value")
		}
		typ := value("provider_type", i)
		switch typ {
		case "openai-compatible", "openai", "kimi", "anthropic", "ollama":
		default:
			return nil, fmt.Errorf("unsupported provider type")
		}
		base := value("provider_base_url", i)
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return nil, fmt.Errorf("invalid provider base URL")
		}
		model := value("provider_model", i)
		if model == "" {
			return nil, fmt.Errorf("provider model is required")
		}
		local := value("provider_local", i) == "true"
		provider := existing[name]
		provider.Type, provider.BaseURL, provider.Model, provider.APIKeyEnv, provider.APIKey, provider.Local = typ, base, model, env, "", local
		out[name] = provider
	}
	if err := providers.ValidateConfig(config.Config{Providers: out}); err != nil {
		return nil, fmt.Errorf("provider settings are invalid: %v", err)
	}
	return out, nil
}
