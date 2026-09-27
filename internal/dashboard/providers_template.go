package dashboard

const providersBody = `{{define "content"}}
<div class="provider-intro">
  <h2>Connect a model</h2>
  <p>Choose a service, enter its model name, and paste your API key. Virgil keeps the key on this computer. Leave the key field empty to keep a saved key.</p>
  {{if .Onboarding}}<p class="provider-hint">Start with one provider. You can add more later.</p>{{end}}
  {{if .Error}}<div class="form-err" role="alert">{{.Error}}</div>{{end}}
</div>
<form method="post" action="/dashboard/providers" autocomplete="off">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  <div class="provider-stack">
    {{range .Providers}}
    <section class="provider-card" data-provider-card>
      <div class="provider-card-head"><div><h3>{{.Name}}</h3><p>Model provider</p></div>{{if .NoKeyRequired}}<span class="badge badge-success">No key needed</span>{{else if .CredentialConfigured}}<span class="badge badge-success">Key saved</span>{{else}}<span class="badge badge-err">No key saved</span>{{end}}</div>
      <div class="provider-fields">
        <label>Service<select name="provider_preset" data-provider-preset><option value="nvidia" {{if eq .Preset "nvidia"}}selected{{end}}>NVIDIA</option><option value="openai" {{if eq .Preset "openai"}}selected{{end}}>OpenAI</option><option value="anthropic" {{if eq .Preset "anthropic"}}selected{{end}}>Anthropic</option><option value="kimi" {{if eq .Preset "kimi"}}selected{{end}}>Kimi</option><option value="ollama" {{if eq .Preset "ollama"}}selected{{end}}>Ollama (local)</option><option value="custom" {{if eq .Preset "custom"}}selected{{end}}>Other compatible service</option></select></label>
        <label>Name in Virgil<input name="provider_name" value="{{.Name}}" maxlength="64" required></label>
        <label>Model ID<input name="provider_model" value="{{.Model}}" placeholder="Model name from your provider" required></label>
        <label>API key<input type="password" name="provider_api_key" value="" placeholder="{{if .CredentialConfigured}}Leave empty to keep saved key{{else}}Paste API key{{end}}" autocomplete="new-password" spellcheck="false"></label>
      </div>
      <details class="provider-advanced"><summary>Advanced connection settings</summary><div class="provider-fields">
        <label>Protocol<input name="provider_type" value="{{.Type}}" placeholder="openai-compatible"></label>
        <label>Provider address<input name="provider_base_url" value="{{.BaseURL}}" placeholder="https://api.example.com/v1"></label>
        <label>Location<select name="provider_local"><option value="false" {{if eq .Location "Remote"}}selected{{end}}>Hosted service</option><option value="true" {{if eq .Location "Local"}}selected{{end}}>This computer</option></select></label>
        <label>Codex request format<select name="provider_responses_backend"><option value="" {{if eq .ResponsesBackend ""}}selected{{end}}>Native Responses</option><option value="chat-completions" {{if eq .ResponsesBackend "chat-completions"}}selected{{end}}>Translate Chat</option></select></label>
        <label>Credential reference<input name="provider_api_key_env" value="{{.APIKeyEnv}}" placeholder="Managed automatically"></label>
        <label>Remove provider<select name="provider_remove"><option value="false">Keep provider</option><option value="true">Remove on save</option></select></label>
      </div></details>
    </section>
    {{end}}
    <section class="provider-card" data-provider-card data-new-provider>
      <div class="provider-card-head"><div><h3>Add a provider</h3><p>A second service or your first model</p></div></div>
      <div class="provider-fields">
        <label>Service<select name="provider_preset" data-provider-preset><option value="" selected>Choose a service</option><option value="nvidia">NVIDIA</option><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="kimi">Kimi</option><option value="ollama">Ollama (local)</option><option value="custom">Other compatible service</option></select></label>
        <label>Name in Virgil<input name="provider_name" placeholder="e.g. my-model" maxlength="64"></label>
        <label>Model ID<input name="provider_model" placeholder="Model name from your provider"></label>
        <label>API key<input type="password" name="provider_api_key" value="" placeholder="Paste API key, if required" autocomplete="new-password" spellcheck="false"></label>
      </div>
      <details class="provider-advanced"><summary>Advanced connection settings</summary><div class="provider-fields">
        <label>Protocol<input name="provider_type" value="openai-compatible"></label>
        <label>Provider address<input name="provider_base_url" placeholder="https://api.example.com/v1"></label>
        <label>Location<select name="provider_local"><option value="false">Hosted service</option><option value="true">This computer</option></select></label>
        <label>Codex request format<select name="provider_responses_backend"><option value="">Native Responses</option><option value="chat-completions">Translate Chat</option></select></label>
        <label>Credential reference<input name="provider_api_key_env" placeholder="Managed automatically"></label>
        <input type="hidden" name="provider_remove" value="false">
      </div></details>
    </section>
  </div>
  <button class="btn-primary provider-save" type="submit">Save providers</button>
  <p class="provider-hint">Restart Virgil after saving to activate provider changes and new keys.</p>
</form>
<details class="provider-help"><summary>How to connect Codex, Claude Code, or another app</summary>
  <div class="provider-help-grid">
    <div class="card"><div class="card-label">Use Virgil in any project</div><p>Keep one Virgil installation outside your projects. Set <code>VIRGIL_HOME</code> to its absolute directory in the environment that starts Virgil and the agent. Open a terminal at any project root and run Codex or Claude Code there. Virgil keeps <code>virgil.toml</code>, <code>.env</code>, database, and control token in its installation; the agent keeps the project as its working directory. Desktop settings are user-level and apply across local projects.</p></div>
    <div class="card"><div class="card-label">Run a protected agent</div><p>1. Save a provider here. 2. Set a limit in <a href="/dashboard/protections">Protections</a>. 3. Restart Virgil, then launch your agent with <code>virgil run -- &lt;command&gt;</code>. The result appears under <a href="/dashboard/executions">Executions</a>.</p><p>For an OpenAI-compatible client, use <code>OPENAI_BASE_URL={{.GatewayURL}}</code>, <code>OPENAI_API_KEY=$VIRGIL_RUN_TOKEN</code>, and the model ID shown above. <code>virgil run</code> supplies these environment variables to the child. Only requests sent through Virgil are protected.</p></div>
    <div class="card"><div class="card-label">Codex CLI pilot</div><p>Codex uses the Responses API. For a provider that offers only Chat Completions, choose Translate Chat. Keep web search disabled with Translate Chat.</p><pre><code>virgil run -- codex -c 'model_provider="virgil"' -c 'model_providers.virgil.name="Virgil"' -c 'model_providers.virgil.base_url="{{.GatewayURL}}"' -c 'model_providers.virgil.env_key="VIRGIL_RUN_TOKEN"' -c 'model_providers.virgil.wire_api="responses"' -c 'model_providers.virgil.requires_openai_auth=false' -c 'web_search="disabled"' -m 'YOUR_MODEL_ID'</code></pre><p>Inspect the run in <a href="/dashboard/executions">Executions</a> and costs in <a href="/dashboard/usage">Usage</a>.</p></div>
    <div class="card"><div class="card-label">Codex desktop</div><p>Set a separate random <code>VIRGIL_DESKTOP_TOKEN</code> (at least 32 characters) in Virgil's ignored <code>.env</code> and restart the core. Configure a custom Responses provider in your user-level <code>~/.codex/config.toml</code> with <code>base_url = "{{.GatewayURL}}"</code>, <code>env_key = "VIRGIL_DESKTOP_TOKEN"</code>, <code>wire_api = "responses"</code>, and your model ID. Put the same token in <code>~/.codex/.env</code> if the app does not inherit shell variables. Restart the app and verify a new local task in <a href="/dashboard/usage">Usage</a>. Desktop calls can be blocked; Virgil cannot stop the app process.</p></div>
    <div class="card"><div class="card-label">Claude Code</div><p>Configure an <code>anthropic</code> provider above. The supervised CLI command is <code>virgil run -- claude</code>; Virgil supplies <code>ANTHROPIC_BASE_URL</code> and <code>ANTHROPIC_AUTH_TOKEN</code>. For Claude desktop, enable Developer Mode and use <strong>Developer → Configure Third-Party Inference</strong> with root URL <code>{{.GatewayRoot}}</code> and the separate desktop token. Check <a href="/dashboard/usage">Usage</a> for desktop requests. An Anthropic API key or another compatible upstream credential is required; a claude.ai subscription is not forwarded by Virgil.</p></div>
    <div class="card"><div class="card-label">OmniRoute upstream</div><p>To route through OmniRoute after Virgil, use <code>http://127.0.0.1:20128/v1</code> as an <code>openai-compatible</code> Responses provider or an <code>anthropic</code> Messages provider, with <code>OMNIROUTE_API_KEY</code> set only for the Virgil core. Verify the chosen model works on OmniRoute's HTTP endpoint. Its separate Codex OAuth WebSocket bridge is not handled by Virgil.</p></div>
  </div>
</details>
<script>
(()=>{const presets={nvidia:{type:'openai-compatible',base:'https://integrate.api.nvidia.com/v1',backend:'chat-completions',local:'false',model:'meta/muse-glimmer-30b'},openai:{type:'openai',base:'https://api.openai.com/v1',backend:'',local:'false'},anthropic:{type:'anthropic',base:'https://api.anthropic.com/v1',backend:'',local:'false'},kimi:{type:'kimi',base:'https://api.moonshot.ai/v1',backend:'',local:'false'},ollama:{type:'ollama',base:'http://127.0.0.1:11434/v1',backend:'',local:'true'}};document.querySelectorAll('[data-provider-card]').forEach(card=>{const select=card.querySelector('[data-provider-preset]');select.addEventListener('change',()=>{const p=presets[select.value];if(!p){card.querySelector('.provider-advanced').open=true;return}card.querySelector('[name=provider_type]').value=p.type;card.querySelector('[name=provider_base_url]').value=p.base;card.querySelector('[name=provider_responses_backend]').value=p.backend;card.querySelector('[name=provider_local]').value=p.local;if(p.model&&(!card.querySelector('[name=provider_model]').value||card.hasAttribute('data-new-provider')))card.querySelector('[name=provider_model]').value=p.model})})})();
</script>{{end}}`
