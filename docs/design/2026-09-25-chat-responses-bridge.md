# Chat Completions upstream for Responses clients

## Problem

Codex sends OpenAI Responses requests. NVIDIA's hosted Integrate API accepts
Chat Completions for the configured `mistralai/mistral-nemotron` model, but its
`/v1/responses` endpoint returns 404. Virgil currently forwards every Responses
request to that endpoint. The same NVIDIA key and model passed a direct Chat
Completions call and a supervised Virgil Chat Completions call.

## Contract

- A provider opts in with `responses_backend = "chat-completions"`; the default
  remains native Responses forwarding. Only `openai-compatible` providers may opt
  in. The panel exposes the setting.
- Virgil validates the Responses request and converts the supported text and
  function-tool subset to Chat Completions before policy reservation or provider
  dispatch. Unsupported built-in, multimodal, stateful, and background features
  fail locally. No request content is silently dropped.
- The bridge converts JSON and SSE Chat Completions results into Responses
  results, including function calls, usage, model, and completion status. The
  existing Responses outcome path records usage and applies policy postflight.
- Provider credentials stay in the core; supervised clients receive only a run
  token. Privacy defaults remain unchanged. Errors sent to clients do not contain
  provider response bodies or credentials.

## Limits

The bridge covers stateless foreground text and function calls needed for a
local coding pilot. Hosted tools such as web search, images, audio, and stored
Responses conversations require a different provider or future work. A
successful translation does not guarantee every model follows tool instructions.

## Acceptance

Synthetic tests prove request conversion, response and SSE conversion, tool
roundtrips, local rejection, policy blocking, and native pass-through unchanged.
A supervised request to the NVIDIA provider must produce a successful Usage
event before trying a real Codex project task.
