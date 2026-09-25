# NVIDIA hosted inference with Codex

NVIDIA's hosted `integrate.api.nvidia.com` endpoint supports Chat Completions for the configured Muse Glimmer model. Codex sends Responses requests. Virgil's **Translate Chat** option converts the supported text and function tool subset between those formats, while keeping policy checks and usage accounting in Virgil.

1. Put `NVIDIA_API_KEY` in Virgil's ignored `.env` file. Keep the value out of the panel and project repositories.
2. In **Providers**, set type `openai-compatible`, base URL `https://integrate.api.nvidia.com/v1`, model `meta/muse-glimmer-30b`, credential environment name `NVIDIA_API_KEY`, and **Responses → Translate Chat**. Save and restart Virgil.
3. From your project directory, follow the [Codex CLI setup](codex.md) using `meta/muse-glimmer-30b` as the model. Keep `-c 'web_search="disabled"'` in the command. Codex's native web search cannot be served by this Chat endpoint.
4. Send a short prompt and check **Executions** and **Usage** in the panel. If the provider returns a transient error, inspect the run's provider status and retry once.

The bridge supports text messages, function calls, and usage reported by the provider. It rejects unsupported multimodal or stateful requests rather than dropping those fields. A live Codex text request completed through Muse Glimmer. A tool call reached Codex, but this session's nested sandbox blocked the shell command, so file operations still need a pilot in your normal project environment. The previous Mistral Nemotron model returned provider errors on Codex's full tool list.
