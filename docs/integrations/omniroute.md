# OmniRoute as an upstream provider

The supported local chain for this pilot is **Codex or Claude Code → Virgil → OmniRoute → model provider**. Virgil owns the protection decision and panel records. OmniRoute selects the upstream model. Configure OmniRoute as a provider in Virgil's **Providers** page:

| Client traffic | Virgil provider type | OmniRoute base URL | OmniRoute endpoint |
| --- | --- | --- | --- |
| Codex Responses | `openai-compatible` | `http://127.0.0.1:20128/v1` | `POST /v1/responses` |
| Claude Messages | `anthropic` | `http://127.0.0.1:20128/v1` | `POST /v1/messages` |

Set the Virgil provider's credential environment variable name to `OMNIROUTE_API_KEY`, set that key only in the Virgil core environment, and use an OmniRoute model ID that its HTTP endpoint accepts. The Codex or Claude client authenticates to Virgil with a Virgil token; it never receives the OmniRoute key. Keep both services on the same machine's loopback addresses during this beta.

Do not place OmniRoute in front of Virgil for this pilot: its current client launchers do not supply a Virgil execution token or supervision signal. A request that skips Virgil has no Virgil protection record.

OmniRoute also documents a **WebSocket** Responses bridge for Codex OAuth/ChatGPT accounts. Virgil currently handles HTTP Responses requests and does not proxy that WebSocket path. Therefore this chain has not been shown to protect Codex traffic using OmniRoute's OAuth bridge. Verify an HTTP model and credential path before expecting this setup to work with a particular OmniRoute connection or subscription.

See OmniRoute's [API reference](https://github.com/diegosouzapw/OmniRoute/blob/main/docs/reference/API_REFERENCE.md), [Codex guide](https://github.com/diegosouzapw/OmniRoute/blob/main/docs/guides/CODEX-CLI-CONFIGURATION.md), and [Claude Code guide](https://github.com/diegosouzapw/OmniRoute/blob/main/docs/guides/CLAUDE-CODE-CONFIGURATION.md). These describe OmniRoute behavior; a live OmniRoute installation is still required to verify the full chain.
