# Claude Code through Virgil (local pilot)

Virgil accepts Anthropic Messages on `/v1/messages` and applies local policy before forwarding to an Anthropic-format provider. Configure a provider of type `anthropic` in **Providers**, with the model ID Claude Code will request, a base URL ending in `/v1`, and an API key environment variable for the Virgil core. Start the core after saving the provider and a limit in **Protections**.

## Supervised CLI

Start Claude Code as a child of Virgil from the project directory:

```powershell
virgil run --deadline 30m -- claude
```

The runner supplies `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN` to the child. Run `/status` inside Claude Code and check that its Anthropic base URL points to Virgil. Send a small prompt, then inspect **Executions** and **Usage** in Virgil. If a policy blocks a request, Virgil also stops the supervised process tree. To choose a different configured model, pass `--env ANTHROPIC_MODEL=YOUR_MODEL_ID` before `--`.

Claude Code may send requests to `/v1/messages?beta=true`; Virgil routes the path and forwards compatible Anthropic protocol headers. It does not implement every optional Anthropic endpoint. Verify the exact Claude Code version and features you intend to use with a live provider.

## Desktop app

For the Claude desktop app, use **Help → Troubleshooting → Enable Developer Mode**, then **Developer → Configure Third-Party Inference**. Enter the Virgil root URL, such as `http://127.0.0.1:8787`, and the separate Virgil desktop token described in [Desktop access](desktop.md). Claude's desktop app does not use the CLI's `ANTHROPIC_BASE_URL` or `settings.json` for this setting. A desktop session is not a child of `virgil run`: Virgil can refuse model requests and record them, but cannot terminate the app's process tree.

Using a gateway credential changes Claude Code's authentication path. Do not expect a claude.ai subscription to pay for requests forwarded with Virgil's provider API key. See [Claude's gateway connection guide](https://code.claude.com/docs/en/llm-gateway-connect) and [protocol compatibility guide](https://code.claude.com/docs/en/llm-gateway-protocol).
