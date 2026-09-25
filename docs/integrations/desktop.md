# Desktop access to Virgil (local beta)

Desktop apps are not supervised child processes. Virgil can authenticate their model requests, apply limits, return a policy block, and record the result in the panel. It cannot kill a desktop process or inspect tool activity that does not pass through its gateway.

Enable a separate desktop credential by generating a random token and setting `VIRGIL_DESKTOP_TOKEN` in the Virgil core's ignored `.env` file. On PowerShell, generate one with:

```powershell
$bytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
[Convert]::ToBase64String($bytes)
```

Copy the resulting value into `VIRGIL_DESKTOP_TOKEN=...` in Virgil's `.env`; restart Virgil. Keep that file outside version control and restrict access to your Windows account. Use the same value only in the desktop client's credential configuration. Rotating the value and restarting Virgil revokes the old one.

## Codex desktop

Set a custom provider in your **user-level** `~/.codex/config.toml`, replacing the model ID and Virgil loopback port as needed:

```toml
model_provider = "virgil"
model = "YOUR_MODEL_ID"

[model_providers.virgil]
name = "Virgil"
base_url = "http://127.0.0.1:8787/v1"
wire_api = "responses"
env_key = "VIRGIL_DESKTOP_TOKEN"
requires_openai_auth = false
```

The desktop app may not inherit shell environment variables. Put `VIRGIL_DESKTOP_TOKEN=...` in your user-level `~/.codex/.env`, restart the app, and start a new local task. Verify a fresh request in Virgil's **Usage** panel before assuming routing works. Project `.codex/config.toml` cannot change a provider or its authentication. You can still use the supervised CLI command in [Codex CLI](codex.md); its `-c` overrides select the short-lived run token for that process.

Changing your user-level default provider affects local Codex sessions. Keep a copy of your existing configuration so you can restore it if the provider or model is unavailable. This pilot does not configure Codex cloud tasks or turn ChatGPT sign-in into a Platform API key.

## Claude desktop

See [Claude Code](claude-code.md) for the desktop app's Third-Party Inference settings. It uses the Virgil root URL and desktop token, not the Codex TOML file.

The [official Codex configuration guide](https://learn.chatgpt.com/docs/config-file/config-advanced) documents user-level custom providers, and the [desktop configuration guide](https://learn.chatgpt.com/docs/amazon-bedrock) confirms that desktop and CLI read the same local configuration layers and that desktop may need `~/.codex/.env` for credentials.
