# Codex CLI through Virgil (local pilot)

This pilot sends Codex model requests through Virgil's `/v1/responses` gateway. Virgil applies its local call, token, cost, provider, model, and tool limits before forwarding each request. A policy block ends a supervised CLI process tree and appears in **Executions**. The provider credential stays in the Virgil core process; Codex receives only a short lived execution token. For Codex desktop, use the separate [desktop access setup](desktop.md).

## Set up

1. In **Providers**, add a provider. For NVIDIA hosted inference, use type `openai-compatible`, base URL `https://integrate.api.nvidia.com/v1`, model `meta/muse-glimmer-30b`, credential environment name `NVIDIA_API_KEY`, and **Responses → Translate Chat**. Set the credential in Virgil's ignored `.env`, save, and restart the core. For an OpenAI Responses provider, keep **Responses → Native**.
2. Set a small call or cost limit in **Protections** and restart the core again. For a hard cost cap, also set `estimated_cost_per_call_usd` in the local configuration until model pricing is configured.
3. From the project directory, start Codex as a child of Virgil. Use the model ID configured in **Providers** and the loopback address shown there. In PowerShell:

   ```powershell
   virgil run --deadline 30m -- codex `
     -c 'model_provider="virgil"' `
     -c 'model_providers.virgil.name="Virgil"' `
     -c 'model_providers.virgil.base_url="http://127.0.0.1:8787/v1"' `
     -c 'model_providers.virgil.env_key="VIRGIL_RUN_TOKEN"' `
     -c 'model_providers.virgil.wire_api="responses"' `
     -c 'model_providers.virgil.requires_openai_auth=false' `
     -c 'web_search="disabled"' `
     -m 'YOUR_MODEL_ID'
   ```

   For a single task, add `exec 'your prompt'` after the model ID. Use `virgil.exe` on Windows if the command is not on `PATH`.

4. Make a small test request. Confirm that **Executions** shows its run and **Usage** shows the provider's response and token count. Then deliberately set `max_requests_per_run = 1`, restart Virgil, and send a second request in one run to verify a block. Restore the intended limit afterwards.

The loopback address is local to the machine running Codex and Virgil. If the core runs on a different LAN host, this CLI setup needs a secure network design and an execution token flow for that host; do not expose the local core as an unauthenticated LAN service.

## Scope and limits

- Codex uses a custom `responses` provider. The gateway accepts stateless foreground calls. **Native** forwards to `/v1/responses`; **Translate Chat** maps supported text and function calls to `/v1/chat/completions`. It rejects stored/background calls and `previous_response_id` so model context does not bypass local inspection. It removes Codex's `client_metadata` before forwarding.
- With **Translate Chat**, disable Codex web search as shown above. Chat Completions has no equivalent for the native `web_search` tool. Image, audio, and other unsupported options are rejected rather than silently omitted. NVIDIA may have different tool behavior from a native Responses model; test the intended workflows before relying on them.
- This setup supervises the **Codex CLI process**. The [desktop setup](desktop.md) routes model calls through Virgil with a separate token, but its process tree cannot be stopped by this pilot. Do not infer desktop process supervision from CLI data.
- The gateway sees model traffic sent to its configured provider. Local shell commands, files, and network calls made by Codex tools are governed by Codex's own controls; Virgil records and limits tool declarations and returned tool calls but is not a general OS firewall.
- A real provider account and live Codex run are needed to validate model support, billing, and every Codex feature. The automated tests use a synthetic provider.

Codex custom provider settings are documented by [OpenAI](https://developers.openai.com/codex/config-file/config-reference). OmniRoute's [Codex guide](https://github.com/diegosouzapw/OmniRoute/blob/main/docs/guides/CODEX-CLI-CONFIGURATION.md) uses the same custom provider mechanism.

To use OmniRoute as the upstream model router, see [OmniRoute upstream](omniroute.md). Its HTTP Responses endpoint can be configured as an `openai-compatible` provider. Its separate WebSocket bridge for ChatGPT-backed Codex sessions is outside this pilot.
