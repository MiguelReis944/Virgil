# Virgil architecture: historical proposal

Status: historical design proposal, 2026-09-16. This document preserves an earlier architecture exploration and is not the current product specification.

The current product direction is a unified local circuit breaker: one loopback core serves the gateway and panel, and `virgil run` supervises agent executions so a policy block can stop the process tree. The panel at `/dashboard` is the primary interface. Local operation does not require a hosted service or LAN access.

## Historical proposal

The sections below retain the earlier proposal for context. Its optional hosted Control Plane, remote policy, team administration, and future remote access ideas are deferred concepts, not requirements or promises for the current product.

> Earlier vision: Virgil is an open source runtime control layer for LLM agents, with local guardrails, cost enforcement, and an optional team control plane.

## Scope and decisions

The public repository owns the MIT-licensed Edge Gateway, the canonical event schema, public policy schema, and export and Control Plane protocols. A hosted Control Plane may use a separate repository. The gateway remains useful with only a local SQLite database and no Virgil account. It does not contact a Virgil service or any telemetry destination by default. A configured provider request still requires access to that provider.

This design proposes Go for the gateway: its standard HTTP stack, single-binary distribution, and Windows/macOS/Linux builds fit the local-first requirement. SQLite must use a driver that does not require a C toolchain for those builds. The Control Plane implementation language is independent of this choice. This is a proposed implementation choice for review, not an existing codebase convention.

The first HTTP compatibility target is OpenAI Chat Completions, including server-sent event (SSE) streaming and tool-call output. The Responses API, provider-specific multimodal features, Runner mode, and SDK lifecycle control have separate milestones. Unsupported request features return a structured error before dispatch instead of being silently dropped.

## Components and trust boundaries

| Component | Responsibility | Trust boundary |
|---|---|---|
| Local HTTP server | Bind loopback, validate requests, preserve trace context, expose health and proxy routes | Application to gateway |
| Router and provider adapters | Select provider, translate supported requests, normalize responses, usage, and errors | Gateway to provider |
| Policy engine | Make deterministic allow/block decisions from local limits and optional remote policy | Untrusted request metadata to local decision |
| Event builder and redactor | Produce allowlisted metadata, remove secrets, assign provenance | In-memory request to durable record |
| SQLite journal and outbox | Persist events, budgets, retry state, and installation identity | Process memory to local disk |
| Exporters | Write JSONL or send OTLP/HTTP and optional Control Plane batches | Local disk to configured destination |
| Optional Control Plane | Authenticate installations, ingest and deduplicate events, aggregate, distribute policies | Installation to tenant service |

The request path is: validate → resolve provider and local credentials → evaluate preflight policy → forward/stream → normalize usage and error → evaluate postflight policy → redact metadata → persist locally → queue eligible exports. Export failure never blocks local event recording. A policy block is itself a local event and does not call the provider.

## Public interfaces

| Interface | Contract |
|---|---|
| `GET /health` | Loopback-only; returns process readiness and SQLite availability, with no secret or provider status. |
| `POST /v1/chat/completions` | Accepts the documented OpenAI-compatible subset. Supports non-streaming JSON and streaming SSE. Returns an OpenAI-shaped response or error. |
| `POST /v1/tool-results` | Optional structured feedback from the local application: `run_id`, `tool_call_id`, `tool_name`, `status`, and an error code. No tool arguments or output body. Enables repeated tool-error policy when the agent loop runs outside the gateway. |
| CLI `events export --format jsonl` | Writes redacted canonical events to an explicit local path. It does not upload. |
| Optional Control Plane API | Enrollment, event batch ingestion, policy retrieval, and installation revocation, as described below. |

The HTTP server binds `127.0.0.1:8787` by default. The first release rejects a non-loopback listen address. A future remote-listen mode requires its own authentication and TLS design. Request and header size limits, concurrency limits, and provider timeouts are configured locally and enforced before forwarding. The gateway never logs request bodies, response bodies, authorization headers, or raw provider errors.

The optional `/v1/tool-results` route is disabled until a local application token is supplied through an environment variable. The route requires that token in a dedicated header; the value is compared without logging or persistence. This prevents another local process from injecting false tool failures into a run. The regular proxy route does not use the Control Plane credential for local authentication.

The local application can pass a provider key in its authorization header, as shown in the README. Alternatively, when a local application token is configured, a request bearing that token uses the selected provider key from the gateway's environment. Any other bearer value is treated only as a pass-through provider key and never unlocks the configured key. The selected adapter uses the resolved key only for its outbound request. It is never stored in SQLite, included in an event, or sent to the Control Plane. An absent key produces a local authentication error. Control Plane enrollment credentials are separate from provider credentials.

The provider interface has four operations: validate supported request fields; construct an outbound request; transform response or SSE frames into the OpenAI-compatible surface; and extract model, usage, status, and normalized error code. OpenAI, Kimi, and arbitrary OpenAI-compatible endpoints share a protocol adapter with separate configuration. Anthropic uses a native Messages API adapter. Model names, prices, and capability flags come from configuration rather than a hardcoded core table.

Streaming passes frames through with bounded buffering. The gateway keeps only counters and the minimal parser state needed for tool-call and usage extraction. A client disconnect cancels the upstream request. A provider error after stream headers are sent is emitted as a final structured SSE error and recorded locally; it cannot be converted into a new HTTP status. Missing final usage is marked `estimated` or `unknown`, never reported as provider-measured usage. The gateway does not retry provider requests automatically because a retry could duplicate a paid completion.

## Canonical event contract

The versioned provider-neutral event has one record per provider attempt or local policy block. Required identifiers use W3C-compatible trace IDs (16 bytes/32 hex characters) and span IDs (8 bytes/16 hex characters). A valid incoming `traceparent` is preserved; otherwise the gateway generates identifiers. `run_id` groups calls in an agent execution and is generated if the client does not provide one. Local `installation_id` is generated and stored even without Control Plane enrollment; it is not a credential.

| Fields | Meaning and constraints |
|---|---|
| `event_id`, `schema_version`, `created_at` | Locally unique event ID, major/minor schema version, UTC timestamp. |
| `organization_id`, `installation_id`, `project_id`, `environment`, `agent_id`, `run_id`, `trace_id`, `span_id` | Correlation. Organization and project are absent in local mode. Client labels are length-limited and never treated as authorization claims. |
| `provider`, `requested_model`, `response_model` | Configured provider and requested/observed model names. No model name is fixed in the core. |
| `input_tokens`, `output_tokens`, `cached_tokens`, `usage_source` | Nonnegative counts or null; `usage_source` is `provider`, `estimated`, or `unknown`. Cached tokens are a subset of input tokens. |
| `latency_ms`, `status`, `error_code` | Monotonic elapsed time; `success`, `provider_error`, `transport_error`, `policy_block`, or `client_cancelled`; normalized code only, no raw message. |
| `tool_name`, `policy_decision` | Optional tool name and structured allow/block decision with policy ID, reason, attempt, and threshold. |
| `actual_cost`, `estimated_cost`, `pricing_version`, `cost_currency` | Decimal-string cost fields, versioned local price source, and ISO currency. Exactly one cost field is populated when pricing is known. |
| `content_capture` | Always false in the initial schema; no content fields are present. |

Provider-reported usage produces `actual_cost` by multiplying the measured token categories by the selected versioned price table. This is a calculated cost, not a provider invoice. Estimated usage produces `estimated_cost`. Unknown usage or missing pricing leaves both null and does not silently become zero. The price version and calculation inputs are retained so reports can be explained and recalculated. Budget checks use the best available estimate before a call and reconcile with measured usage afterward; a preflight estimate cannot guarantee the final invoice amount.

The canonical schema is independent of OpenTelemetry's evolving GenAI attributes. The OTLP adapter maps provider, requested/response model, usage, latency, and trace IDs to current documented conventions in one mapping module. It emits valid OTLP/HTTP trace payloads to `/v1/traces`, not raw canonical JSON disguised as OTLP. Optional Virgil attributes carry policy and cost provenance. Prompt, response, tool arguments, and reasoning attributes are never emitted. Cached input tokens must not be added twice to total input tokens.

## Persistence, retention, and delivery

SQLite is the source of truth for local events and budget counters. Migrations are numbered and transactional. WAL mode supports concurrent proxy reads and a single writer; a bounded write queue applies backpressure rather than dropping audit records silently. The database path is local and file permissions are restricted to the current user where the operating system supports that. Retention is configurable; deleting delivered events never deletes pending or retryable events merely because their age threshold passed.

An event is built, redacted, and inserted into SQLite before export. In the same transaction, the gateway assigns a stable idempotency key derived from `installation_id` and `event_id` and creates one outbox row per enabled destination. This preserves the required logical order while preventing a crash between persistence and key assignment. Export is disabled until the user configures both a destination and an explicit field allowlist.

Outbox states are `pending`, `sending`, `delivered`, `retryable`, and `dead_letter`. A worker leases pending/retryable rows, sends a bounded batch, marks acknowledged rows delivered, and returns expired `sending` leases to retryable after restart. Network errors and HTTP 429/502/503/504 use exponential backoff with jitter and honor `Retry-After` for OTLP. Permanent validation/authentication failures become dead letters visible locally. JSONL export writes to an explicit file with a stable event ID, while remote destinations use the same idempotency key across retries. A destination's acknowledgment does not mark another destination delivered.

## Privacy and authentication

The default persisted and exported event is metadata only. Prompts, responses, raw tool arguments, raw headers, provider keys, and Chain of Thought are not captured. The first release does not offer content capture; any later opt-in requires a separate schema and security review. Redaction runs before SQLite insertion and again against the destination allowlist before export. It removes known secret-bearing fields and rejects an event if an unrecognized free-text field could bypass the allowlist. A redaction failure is logged locally as a code without the offending value and prevents export.

Provider keys remain at the edge. The gateway does not need a Virgil account. Optional enrollment exchanges a short-lived one-time enrollment token for an installation-scoped credential stored in the local OS secret store or an explicitly configured ignored file. Control Plane APIs require TLS and that credential; provider credentials are never accepted there. Revocation prevents subsequent ingestion and policy downloads for that installation, while local gateway operation continues.

The Control Plane derives `organization_id` and installation identity from the authenticated credential, not from event fields. It rejects a mismatched tenant claim and deduplicates on `(organization_id, installation_id, event_id)`. Its public contract consists of `POST /v1/installations/enroll`, `POST /v1/events/batch`, and `GET /v1/policies/current`; revocation is an authenticated administrative action. Policies are versioned and constrained to the installed organization's project/environment. A remote policy may tighten local limits but cannot loosen a locally configured hard cap, enable content capture, change provider endpoints, or add export destinations. If policy download fails, the last valid policy remains active and local hard caps continue to apply.

The enrollment response contains a server installation ID and a scoped credential; the local ID remains the stable event origin. Batch ingestion returns accepted event IDs and per-event rejection codes so only acknowledged outbox rows are delivered. `GET /v1/policies/current` returns a version and cache validator. `POST /v1/installations/{id}/revoke` requires an organization administrator and immediately denies subsequent ingestion and policy access for that installation.

The service data model has organizations, users, teams, projects, environments, installations, events, policy versions, and administrative audit entries. Ingestion and aggregation are tenant-scoped. Dashboards group cost and errors by organization, project, user or agent, provider, and model; alerts evaluate budget and failure thresholds. RBAC gates administrative reads and writes; OIDC is the first SSO mechanism and SAML belongs to the enterprise phase. Retention and export are tenant settings. The public repository specifies these contracts without requiring the hosted implementation to live in this repository.

## Deterministic guardrails and proxy limits

The initial policy engine evaluates maximum cost per run, provider calls, total/input/output tokens, duration, tool calls, repeated identical tool calls, repeated error codes, and allowlists for providers, models, and tools. Each block returns a structured decision with `decision`, `reason`, `policy`, `attempt`, and `threshold`. Counters are scoped by `run_id`; invalid or missing client IDs are replaced with generated IDs. Concurrency-safe updates in SQLite prevent two requests from both passing a remaining single-call budget.

The proxy can observe provider errors and tool-call requests passing through it. It cannot know whether an external tool execution failed unless the application submits structured feedback to `/v1/tool-results` or a future SDK/Runner reports it. Three equivalent reported tool errors can block the fourth relevant provider request. Tool arguments are not persisted; repeated-call matching uses a bounded in-memory normalized fingerprint and stores only a non-reversible digest if restart continuity is enabled. A proxy can block only calls routed through it and cannot terminate an agent loop outside the gateway. Runner mode and the Python SDK are later milestones for full lifecycle control.

## Compatibility and tests

The first compatibility matrix covers OpenAI Chat Completions JSON and SSE, OpenAI tool calls, Kimi through an OpenAI-compatible base URL, Anthropic Messages translation, and configurable OpenAI-compatible endpoints. Each adapter has contract fixtures for success, streaming completion, usage absent/present, tool calls, provider errors, client cancellation, and unsupported features. The core never assumes a particular model name or price.

Tests use synthetic payloads and local fake provider and collector servers; no real provider credentials or network calls are required. Required suites cover: local startup without Docker or Control Plane; request and streaming forwarding; preservation/generation of trace IDs; provider and estimated token/cost provenance; SQLite migration and restart; redaction and forbidden-field scans of DB, JSONL, logs, and outbound requests; deterministic three-error block; concurrent budgets; outbox recovery, deduplication, and reconnect; OTLP payload decoding; installation revocation and remote-policy precedence; Windows/macOS/Linux builds. The end-to-end suite must prove that a disconnected Control Plane does not break local proxying or event recording.

## Threat model

| Threat | Boundary | Control and test |
|---|---|---|
| Another machine reaches the local proxy | Network to local HTTP | Loopback-only bind; reject non-loopback configuration in the first release. |
| Another local process fabricates tool feedback | Local application to tool-results route | Route disabled by default; separate locally supplied token when enabled. |
| Another local process spends a configured provider key | Local application to proxy route | Configured-key mode requires a separate locally supplied application token. |
| Secret appears in event, log, or export | Request/provider to disk or network | Allowlisted fields, header exclusion, pre-persistence redaction, outbound redaction, synthetic leak tests. |
| Custom provider/export URL reaches private infrastructure | Local config to outbound network | Endpoints are local-only configuration, never remote-policy controlled; reject userinfo and non-HTTP(S) schemes, require TLS for remote exports. |
| Duplicate or lost cost event after crash | SQLite to Control Plane | Transactional event/outbox insert, stable idempotency key, lease recovery, server unique constraint. |
| Tenant spoofing or installation replay | Gateway to Control Plane | Installation-scoped credential, server-derived tenant identity, TLS, revocation, deduplication. |
| Remote policy weakens a local hard cap | Control Plane to policy engine | Monotonic policy version, schema validation, local hard-cap precedence, last-valid-policy fallback. |
| Unbounded agent/tool loop | Application to gateway | Deterministic counters for intercepted calls; document that out-of-band loops need Runner/SDK supervision. |
| Expensive duplicate provider request | Gateway to provider | Never automatically retry an LLM request after uncertain dispatch. |

## Source references

- [OpenAI Chat Completions API](https://developers.openai.com/api/reference/cli/resources/chat) — request/response and streaming surface.
- [Claude Messages streaming](https://platform.claude.com/docs/en/build-with-claude/streaming) — native event and usage behavior for the Anthropic adapter.
- [OpenTelemetry GenAI semantic conventions](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/gen-ai-spans.md) — mapping reference; the canonical Virgil schema does not depend on its release cadence.
- [OTLP specification](https://opentelemetry.io/docs/specs/otlp/) — HTTP payloads, acknowledgments, and retry handling.
