package gateway

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/pelletier/go-toml/v2"
)

// NewSetupHandler returns the /setup handler. Used by the setup-only server
// when the main config fails to load.
func NewSetupHandler(configPath string) http.HandlerFunc {
	return newSetupHandler(configPath)
}

func newSetupHandler(configPath string) http.HandlerFunc {
	tmpl := template.Must(template.New("setup").Funcs(template.FuncMap{
		"inc": func(i int) int { return i + 1 },
	}).Parse(setupHTML))
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			renderSetup(w, configPath, tmpl, "", "")
		case http.MethodPost:
			handleSetupPost(w, r, configPath, tmpl)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

type setupData struct {
	Providers  []providerRow
	Guardrails config.GuardrailsConfig
	Msg        string
	MsgClass   string
}

type providerRow struct {
	Name      string
	Type      string
	BaseURL   string
	APIKeyEnv string
	Model     string
}

func renderSetup(w http.ResponseWriter, configPath string, tmpl *template.Template, msg, msgClass string) {
	data := setupData{Msg: msg, MsgClass: msgClass}
	if loaded, err := config.Load(configPath, os.Getenv); err == nil {
		for name, p := range loaded.Providers {
			data.Providers = append(data.Providers, providerRow{
				Name: name, Type: p.Type, BaseURL: p.BaseURL,
				APIKeyEnv: p.APIKeyEnv, Model: p.Model,
			})
		}
		data.Guardrails = loaded.Guardrails
	}
	if len(data.Providers) == 0 {
		data.Providers = []providerRow{{}}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, data)
}

func handleSetupPost(w http.ResponseWriter, r *http.Request, configPath string, tmpl *template.Template) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	names := r.Form["provider_name"]
	types := r.Form["provider_type"]
	baseURLs := r.Form["provider_base_url"]
	apiKeyEnvs := r.Form["provider_api_key_env"]
	models := r.Form["provider_model"]

	providers := make(map[string]config.ProviderConfig)
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		p := config.ProviderConfig{}
		if i < len(types) {
			p.Type = strings.TrimSpace(types[i])
			p.Local = p.Type == "ollama"
		}
		if i < len(baseURLs) {
			p.BaseURL = strings.TrimSpace(baseURLs[i])
		}
		if i < len(models) {
			p.Model = strings.TrimSpace(models[i])
		}
		if i < len(apiKeyEnvs) {
			if env := strings.TrimSpace(apiKeyEnvs[i]); env != "" {
				p.APIKey = "${" + env + "}"
			}
		}
		providers[name] = p
	}

	g := config.GuardrailsConfig{}
	if v, err := strconv.ParseInt(r.FormValue("max_requests_per_run"), 10, 64); err == nil && v >= 0 {
		g.MaxRequestsPerRun = v
	}
	if cost := strings.TrimSpace(r.FormValue("max_cost_per_run_usd")); cost != "" {
		g.MaxCostPerRunUSD = cost
	}

	existing, err := config.Load(configPath, os.Getenv)
	if err != nil {
		existing = config.Config{
			Server:  config.ServerConfig{Listen: "127.0.0.1:8787", LogLevel: "info"},
			Storage: config.StorageConfig{Path: "./data/virgil.db", RetentionDays: 30},
		}
	}
	existing.Providers = providers
	existing.Guardrails = g

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(existing); err != nil {
		renderSetup(w, configPath, tmpl, fmt.Sprintf("Error encoding config: %v", err), "err")
		return
	}
	if err := os.WriteFile(configPath, buf.Bytes(), 0644); err != nil {
		renderSetup(w, configPath, tmpl, fmt.Sprintf("Error saving config: %v", err), "err")
		return
	}
	renderSetup(w, configPath, tmpl, "Saved! Restart Virgil to apply changes: Ctrl+C → go run ./cmd/virgil serve", "ok")
}

const setupHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Virgil Setup</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,sans-serif;background:#f5f5f5;color:#1a1a1a;padding:32px 20px}
.wrap{max-width:680px;margin:0 auto}
h1{font-size:1.4rem;font-weight:600;margin-bottom:4px}
.sub{color:#666;font-size:.875rem;margin-bottom:28px}
section{background:#fff;border:1px solid #e2e2e2;border-radius:10px;padding:24px;margin-bottom:18px}
h2{font-size:.95rem;font-weight:600;color:#333;margin-bottom:16px}
label{display:block;font-size:.8rem;color:#555;margin-top:12px;margin-bottom:4px;font-weight:500}
input,select{width:100%;padding:8px 10px;border:1px solid #ccc;border-radius:6px;font-size:.875rem;outline:none}
input:focus,select:focus{border-color:#0070f3;box-shadow:0 0 0 2px #dbeafe}
.provider-card{border:1px solid #ebebeb;border-radius:7px;padding:16px;margin-bottom:12px;position:relative}
.provider-card h3{font-size:.85rem;color:#555;margin-bottom:10px}
.rm{position:absolute;top:10px;right:10px;background:none;border:none;cursor:pointer;color:#bbb;font-size:1.1rem;line-height:1}
.rm:hover{color:#c00}
.add{width:100%;border:1px dashed #ccc;background:none;border-radius:6px;padding:9px;cursor:pointer;color:#666;font-size:.875rem;margin-top:4px}
.add:hover{border-color:#666;color:#333}
.save{background:#0070f3;color:#fff;border:none;border-radius:7px;padding:11px 30px;font-size:.95rem;cursor:pointer;margin-top:6px}
.save:hover{background:#005dd9}
.msg{padding:12px 16px;border-radius:7px;margin-bottom:20px;font-size:.875rem}
.ok{background:#ecfdf5;color:#166534;border:1px solid #bbf7d0}
.err{background:#fef2f2;color:#991b1b;border:1px solid #fecaca}
</style>
</head>
<body>
<div class="wrap">
<h1>Virgil Setup</h1>
<p class="sub">Configure providers and guardrails — restart Virgil after saving.</p>
{{if .Msg}}<div class="msg {{.MsgClass}}">{{.Msg}}</div>{{end}}
<form method="POST" action="/setup">
<section>
  <h2>Providers</h2>
  <div id="providers">
  {{range $i, $p := .Providers}}
  <div class="provider-card">
    <h3>Provider {{inc $i}}</h3>
    <button type="button" class="rm" onclick="this.parentElement.remove()" title="Remove">×</button>
    <label>Name</label>
    <input name="provider_name" value="{{$p.Name}}" placeholder="openai" required>
    <label>Type</label>
    <select name="provider_type" onchange="fillDefaults(this)">
      <option value="openai"{{if eq $p.Type "openai"}} selected{{end}}>OpenAI</option>
      <option value="anthropic"{{if eq $p.Type "anthropic"}} selected{{end}}>Anthropic</option>
      <option value="kimi"{{if eq $p.Type "kimi"}} selected{{end}}>Kimi</option>
      <option value="ollama"{{if eq $p.Type "ollama"}} selected{{end}}>Ollama (local)</option>
    </select>
    <label>Base URL</label>
    <input name="provider_base_url" value="{{$p.BaseURL}}" placeholder="https://api.openai.com/v1">
    <label>API key — environment variable name (e.g. OPENAI_API_KEY)</label>
    <input name="provider_api_key_env" value="{{$p.APIKeyEnv}}" placeholder="OPENAI_API_KEY">
    <label>Default model</label>
    <input name="provider_model" value="{{$p.Model}}" placeholder="gpt-4o">
  </div>
  {{end}}
  </div>
  <button type="button" class="add" onclick="addProvider()">+ Add provider</button>
</section>
<section>
  <h2>Guardrails</h2>
  <label>Max requests per run (0 = unlimited)</label>
  <input name="max_requests_per_run" type="number" min="0" value="{{.Guardrails.MaxRequestsPerRun}}">
  <label>Max cost per run USD (e.g. 0.50 — leave blank for no limit)</label>
  <input name="max_cost_per_run_usd" value="{{.Guardrails.MaxCostPerRunUSD}}" placeholder="0.50">
</section>
<button type="submit" class="save">Save</button>
</form>
</div>
<script>
const defaults = {
  openai:    {url:'https://api.openai.com/v1',   env:'OPENAI_API_KEY',   model:'gpt-4o'},
  anthropic: {url:'https://api.anthropic.com/v1',env:'ANTHROPIC_API_KEY',model:'claude-sonnet-4-6'},
  kimi:      {url:'https://api.moonshot.ai/v1',  env:'MOONSHOT_API_KEY', model:'moonshot-v1-8k'},
  ollama:    {url:'http://localhost:11434/v1',    env:'',                 model:'llama3'},
};
function fillDefaults(sel) {
  const card = sel.closest('.provider-card');
  const d = defaults[sel.value] || {};
  card.querySelector('[name=provider_base_url]').value = d.url || '';
  card.querySelector('[name=provider_api_key_env]').value = d.env || '';
  card.querySelector('[name=provider_model]').value = d.model || '';
}
let count = document.querySelectorAll('.provider-card').length;
function addProvider() {
  count++;
  const d = document.createElement('div');
  d.className = 'provider-card';
  d.innerHTML = '<h3>Provider '+count+'</h3>'
    +'<button type="button" class="rm" onclick="this.parentElement.remove()">×</button>'
    +'<label>Name</label><input name="provider_name" placeholder="openai" required>'
    +'<label>Type</label>'
    +'<select name="provider_type" onchange="fillDefaults(this)">'
    +'<option value="openai">OpenAI</option>'
    +'<option value="anthropic">Anthropic</option>'
    +'<option value="kimi">Kimi</option>'
    +'<option value="ollama">Ollama (local)</option>'
    +'</select>'
    +'<label>Base URL</label><input name="provider_base_url" placeholder="https://api.openai.com/v1">'
    +'<label>API key — environment variable name</label><input name="provider_api_key_env" placeholder="OPENAI_API_KEY">'
    +'<label>Default model</label><input name="provider_model" placeholder="gpt-4o">';
  document.getElementById('providers').appendChild(d);
}
</script>
</body>
</html>`
