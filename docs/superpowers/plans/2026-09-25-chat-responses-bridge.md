# Chat Responses Bridge Implementation Plan

> **For agentic workers:** Use test-driven development for each task and verify the full Go suite before committing.

**Goal:** Let a supervised Codex Responses client use a Chat Completions-only provider such as hosted NVIDIA through Virgil.

**Architecture:** An explicit provider setting selects the translation path. The gateway keeps its existing auth, policy, and telemetry boundaries; a focused translator maps the supported text and function-tool protocol subset.

**Tech Stack:** Go standard library, existing SQLite/policy packages, synthetic `httptest` upstreams.

**Spec:** `docs/design/2026-09-25-chat-responses-bridge.md`

## Global constraints

- Native Responses forwarding remains the default.
- No provider key or user content is logged by the bridge.
- Reject features that cannot be represented in Chat Completions.

### Task 1: Provider selection

**Files:** `internal/config/config.go`, `internal/providers/registry.go`, `internal/gateway/chat.go`, `internal/dashboard/providers.go` and their tests.

- [ ] Add a failing test for explicit opt-in and invalid backend values.
- [ ] Add `responses_backend = "chat-completions"` to the provider config and dashboard form.
- [ ] Pass the backend setting into the gateway route; keep the default native.
- [ ] Run focused tests and commit this checkpoint.

### Task 2: Protocol translation

**Files:** `internal/gateway/responses_chat.go`, `internal/gateway/responses_chat_test.go`.

- [ ] Test text, function-tool, function result, usage, and SSE shapes with synthetic payloads.
- [ ] Implement bounded, stateless conversion in both directions.
- [ ] Reject unsupported input and tool types before provider dispatch.
- [ ] Run focused tests and commit this checkpoint.

### Task 3: Gateway integration and documentation

**Files:** `internal/gateway/responses.go`, `internal/gateway/responses_test.go`, `docs/integrations/nvidia.md`.

- [ ] Test the Chat path with a fake upstream, policy block, and native path regression.
- [ ] Select `/chat/completions` only for opted-in providers and convert results.
- [ ] Document a local NVIDIA setup with the required Codex model ID.
- [ ] Run `go test ./... -count=1`, build, and a live supervised small call.
- [ ] Commit and update the harness submodule pointer.
