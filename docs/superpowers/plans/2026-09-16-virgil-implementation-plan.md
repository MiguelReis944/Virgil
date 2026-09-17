# Virgil Implementation Plan

> **For agentic workers:** Use `executing-plans` task by task. After verifying a cohesive checkpoint, an agent may make a local commit containing only that checkpoint. Push and publication require an explicit user request. Independent, bounded subtasks may be delegated when available.

**Goal:** Deliver a useful offline Edge Gateway, then an optional tenant-scoped Control Plane and supervised agent integrations.

**Architecture:** The public repository contains the local Go gateway, canonical schemas, and Control Plane wire contract. Events are redacted before a transactional SQLite journal and independently acknowledged outbox destinations. A separately owned Control Plane consumes the public contract; its absence never prevents local proxying.

**Tech Stack:** Go 1.26 or newer, `net/http`, `modernc.org/sqlite`, TOML configuration, SQLite WAL, OTLP/HTTP protobuf, PostgreSQL for the optional service, browser UI served by that service, Python 3.11 or newer for the later SDK. Select and record dependency versions in `go.mod`/`go.sum` when each component starts; do not use floating dependencies.

**Spec:** [Virgil architecture](../../design/2026-09-16-virgil-architecture.md).

## Quality hardening checkpoint (2026-09-17)

The implemented Edge Gateway now bounds complete HTTP request reads to fifteen seconds while retaining the 1 MiB chat body limit. Slow bodies return a generic 408, and SSE still flushes incrementally without a global write deadline. Process cancellation propagates to active requests and upstream streams; graceful shutdown has a five-second deadline and closes remaining connections on expiry. Task 7 has an initial local policy implementation with transactional call reservations, counter reconciliation, allowlists, and policy-block events. Its acceptance is being audited before Task 8. See [quality review](../../quality/2026-09-16-quality-review.md) for validation results and tool limitations.

The **local Edge MVP** closes when Tasks 1–14 demonstrate offline proxying, private events, local policies including repeated-error blocking, JSONL/OTLP export, contract compatibility, and reproducible end-to-end tests without a service. The **integrated MVP** also requires Tasks 15–18 in a separate Control Plane repository, with tenant-scoped ingestion, aggregation, revocation, and remote policy demonstrated end to end. Tasks 19–20 (Runner and SDK) follow the stable Edge.

Task 8 now accepts bounded metadata-only tool feedback behind the dedicated local application token, detects repeated normalized tool errors and provider errors, and fingerprints provider tool calls in JSON/SSE without persisting arguments. Repetition state is bounded in memory and resets on gateway restart; the SQLite call and budget counters remain durable. The proxy blocks only intercepted requests and does not supervise the external agent process. Task 9 is next.

## Global constraints

- Preserve the MIT license and keep all examples and test payloads synthetic.
- The gateway starts without Docker, an account, a Control Plane, or a Virgil network request. It binds `127.0.0.1:8787` by default and rejects non-loopback listeners.
- The supported first proxy surface is `POST /v1/chat/completions` JSON and SSE. Unsupported fields fail before provider dispatch.
- Never persist or export prompts, responses, tool arguments, raw headers, keys, or Chain of Thought. Content capture stays disabled in the initial schema.
- Provider credentials stay local. Configured-key mode requires the distinct local application token; the tool-results route is disabled without that token.
- Every provider attempt and local policy block yields one canonical event. Unknown usage or price leaves cost null, not zero.
- Export requires both a destination and an explicit field allowlist; a Control Plane is never an implicit destination.
- No provider request is retried automatically. Only telemetry delivery uses retry.
- Each code task uses a red → green test cycle. At a checkpoint boundary, run the focused and shared gates, inspect the complete diff and privacy scan, then make a local commit containing only the verified checkpoint. No push or publication without explicit authorization.

## Repository boundaries and checkpoints

Paths in Tasks 1–14 and 19–20 are relative to this public repository. Paths in Tasks 15–18 are relative to a **separate Control Plane repository root**; they must not be created in Virgil. The Control Plane repository must be selected before Task 15, with its own access control, license, Git history, and deployment decisions. The public protocol in Task 14 is sufficient for an independent implementation.

Each numbered task is a local commit checkpoint after its tests pass. Tasks 1–14 form the Edge and protocol sequence; Tasks 15–18 form the optional team service; Tasks 19–20 follow a stable proxy. The agent commits only its verified files in the repository that owns them. Push remains a separate user decision. The harness submodule pointer is updated separately after a Virgil commit.

For each code task: (1) write the named failing test, (2) run its focused command and observe failure for the missing behavior, (3) add the listed implementation, (4) run the focused command and the shared gate, (5) review the diff and commit the verified checkpoint locally. Test assertions below are the minimum required cases, not permission to omit adjacent failure cases.

### Task 1: Local server, configuration, and health

**Depends on:** architecture specification.

**Files:** Create `go.mod`, `cmd/virgil/main.go`, `internal/config/config.go`, `internal/config/config_test.go`, `internal/storage/connection.go`, `internal/storage/connection_test.go`, `internal/gateway/server.go`, `internal/gateway/server_test.go`, `configs/virgil.example.toml`, `.gitignore`; update `README.md` startup section.

**Interfaces:** `config.Load(path string, getenv func(string) string) (Config, error)` returns typed server, storage, provider, privacy, and policy settings; `storage.Open(path string) (*sql.DB, error)` opens a local SQLite file without creating application tables; `gateway.NewServer(cfg config.Config, deps Dependencies) (http.Handler, error)` registers `GET /health` and checks the database connection; `Dependencies` holds the database and later journal, router, and policy interfaces. `main` supports `virgil serve --config <path>`.

**Test:** `TestLoadRejectsNonLoopback` checks `0.0.0.0` and a public IP; `TestHealth` expects HTTP 200 with readiness and no configuration/secrets; `TestOfflineStartup` uses a temporary database path and no provider or Control Plane. Invalid TOML and a missing config path return a clear error code.

**Focused validation:** `go test ./internal/config ./internal/storage ./internal/gateway`. **Acceptance:** a local process starts with the example configuration and `/health` reports ready with SQLite reachable; no outbound HTTP occurs.

### Task 2: OpenAI-compatible JSON proxy

**Depends on:** 1.

**Files:** Create `internal/providers/adapter.go`, `internal/providers/openai_compatible.go`, `internal/providers/openai_compatible_test.go`, `internal/gateway/chat.go`, `internal/gateway/chat_test.go`, `tests/fixtures/chat_request.json`, `tests/fixtures/chat_response.json`; update `internal/gateway/server.go` and `configs/virgil.example.toml`.

**Interfaces:** `providers.Adapter` exposes `Validate(json.RawMessage) error`, `Build(ctx context.Context, body json.RawMessage, key string) (*http.Request, error)`, `Translate(*http.Response, http.ResponseWriter) (providers.Result, error)`. `providers.Result` contains response model, optional usage counts, normalized error code, and status. `gateway.Router` selects one configured adapter from a model/provider mapping. The local auth resolver accepts a pass-through provider bearer; it uses an environment-configured key only for the exact local application token.

**Test:** `TestChatJSONForwarding` uses `httptest.Server` and asserts body, model, tool calls, upstream status, and returned OpenAI shape. `TestConfiguredKeyRequiresAppToken` ensures arbitrary bearer values cannot unlock an environment key. `TestUnsupportedFeatureDoesNotDispatch` asserts HTTP 400 and zero fake-provider calls. Provider errors expose normalized codes, never raw upstream text.

**Focused validation:** `go test ./internal/providers ./internal/gateway -run 'TestChatJSON|TestConfiguredKey|TestUnsupportedFeature'`. **Acceptance:** one JSON completion crosses the fake provider; no provider credentials appear in logs or response errors.

### Task 3: Streaming proxy and cancellation

**Depends on:** 2.

**Files:** Create `internal/providers/sse.go`, `internal/providers/sse_test.go`, `internal/gateway/stream_test.go`; update `internal/providers/openai_compatible.go` and `internal/gateway/chat.go`.

**Interfaces:** Add `Stream(ctx context.Context, upstream *http.Response, downstream http.ResponseWriter) (providers.Result, error)` to the Task 2 `Adapter` interface. It forwards bounded SSE frames and extracts final usage. `providers.Result` includes `UsageSource` (`provider`, `estimated`, `unknown`).

**Test:** `TestSSEForwardsToolDeltaAndUsage` verifies ordered tool-call deltas, `[DONE]`, and usage; `TestClientCancelCancelsUpstream` observes request-context cancellation; `TestLateProviderErrorIsSSE` expects a structured final error frame; `TestMissingUsageIsUnknown` forbids fabricated provider usage.

**Focused validation:** `go test ./internal/providers ./internal/gateway -run 'TestSSE|TestClientCancel|TestLateProvider|TestMissingUsage'`. **Acceptance:** streaming reaches the client incrementally and a disconnect stops upstream work.

### Task 4: Canonical events and trace context

**Depends on:** 3.

**Files:** Create `schemas/event.v1.schema.json`, `internal/telemetry/event.go`, `internal/telemetry/event_test.go`, `internal/telemetry/trace.go`, `internal/telemetry/trace_test.go`; update `internal/gateway/chat.go`.

**Interfaces:** `telemetry.Event` has exactly the documented canonical fields; `telemetry.NewTrace(traceparent string) (TraceContext, error)` preserves a valid W3C trace ID and span ID or creates them; `telemetry.Build(input Attempt) (Event, error)` uses a monotonic elapsed duration and one status from the spec. `Attempt` carries only metadata and usage, never request or response bodies.

**Test:** `TestIncomingTraceparentPreserved`, `TestInvalidTraceparentRegenerated`, `TestEventSchemaRejectsContent`, and table cases for success, provider error, transport error, cancellation, and policy block. Trace IDs are 32 lowercase hex digits; span IDs are 16.

**Focused validation:** `go test ./internal/telemetry ./internal/gateway`. **Acceptance:** one event per attempt/block with valid IDs and explicit usage provenance.

### Task 5: SQLite journal, migrations, and restart

**Depends on:** 4.

**Files:** Create `internal/storage/sqlite.go`, `internal/storage/sqlite_test.go`, `internal/storage/migrations/001_init.sql`, `internal/storage/migrations/002_outbox.sql`, `internal/storage/retention.go`, `internal/storage/retention_test.go`; update `internal/gateway/server.go` and `cmd/virgil/main.go`.

**Interfaces:** `storage.Journal` exposes `Append(ctx context.Context, event telemetry.Event, destinations []string) error`, `Get(ctx context.Context, eventID string) (telemetry.Event, error)`, and `Prune(ctx context.Context, before time.Time) (int64, error)`. `Append` creates stable `installation_id:event_id` idempotency keys and destination outbox rows in one transaction. A bounded writer queue returns a local overload error rather than silently dropping events. The journal owns a local installation ID independent of enrollment.

**Test:** `TestMigrateEmptyAndExistingDatabase`, `TestRestartPreservesEventAndInstallationID`, `TestAppendRollsBackEventAndOutboxTogether`, `TestPruneKeepsUndeliveredRows`, and concurrent append. SQLite uses WAL, busy timeout, and current-user file permissions where supported.

**Focused validation:** `go test ./internal/storage ./internal/gateway`. **Acceptance:** a crash/restart preserves committed events and pending deliveries; migrations are repeatable.

### Task 6: Privacy boundary and leak tests

**Depends on:** 5.

**Files:** Create `internal/redaction/event.go`, `internal/redaction/event_test.go`, `internal/gateway/privacy_test.go`, `tests/privacy/scan.go`, `tests/privacy/scan_test.go`; update `internal/storage/sqlite.go` and `internal/gateway/chat.go`.

**Interfaces:** `redaction.Prepare(event telemetry.Event) (telemetry.Event, error)` accepts only schema fields; `redaction.ForExport(event telemetry.Event, allowed []string) (map[string]any, error)` intersects an explicit allowlist with safe fields. Gateway calls `Prepare` before `Journal.Append`; exporters call `ForExport` again. Redaction failures emit a code without the rejected value.

**Test:** synthetic canaries in prompts, response text, tool arguments, authorization headers, provider error text, and malformed metadata do not occur in database bytes, logs, JSONL output, or fake outbound bodies. `TestUnknownFreeTextFailsClosed` rejects fields outside the schema.

**Focused validation:** `go test ./internal/redaction ./internal/gateway ./tests/privacy`. **Acceptance:** default event contains metadata only, `content_capture=false`, and scan finds no synthetic canary outside transient fake-provider traffic.

### Task 7: Deterministic local policy engine

**Depends on:** 6.

**Files:** Create `schemas/policy.v1.schema.json`, `internal/policies/policy.go`, `internal/policies/policy_test.go`, `internal/policies/counters.go`, `internal/policies/counters_test.go`, `internal/storage/migrations/003_policy_counters.sql`; update `internal/storage/sqlite.go` and `internal/gateway/chat.go`.

**Interfaces:** `policies.Engine.Preflight(ctx, RequestFacts) (Decision, error)` atomically reserves a call budget; `Postflight(ctx, OutcomeFacts) error` reconciles tokens and cost; `Decision` has `decision`, `reason`, `policy`, `attempt`, `threshold`. `RequestFacts` is limited to run ID, provider, model, tool names, start time, and configured estimates. Hard local limits cover cost, call count, input/output/total tokens, duration, tool count, and allowlists. If a hard cost cap is set but no cost estimate is available before Task 10 supplies pricing, preflight blocks with `cost_unavailable` rather than bypassing the cap.

**Test:** `TestConcurrentLastCallBudgetAllowsOne`, `TestAllowedProviderModelTool`, `TestDurationAndTokenLimits`, `TestPolicyBlockDoesNotCallProvider`. A missing/invalid run ID is replaced with a generated one. A local policy block is journaled as an event.

**Focused validation:** `go test -race ./internal/policies ./internal/gateway`. **Acceptance:** concurrent requests cannot both spend the last available call; every block returns the structured decision.

### Task 8: Tool feedback and repeated failures

**Depends on:** 7.

**Files:** Create `internal/policies/repetition.go`, `internal/policies/repetition_test.go`, `internal/gateway/tool_results.go`, `internal/gateway/tool_results_test.go`; update `internal/gateway/server.go` and `internal/gateway/chat.go`.

**Interfaces:** `policies.RecordToolResult(ctx, ToolResult) (Decision, error)` consumes `run_id`, `tool_call_id`, `tool_name`, `status`, normalized error code, and an in-memory bounded fingerprint. `POST /v1/tool-results` requires the local application token in a dedicated header and never accepts tool output or arguments. After three equivalent errors, `Preflight` blocks attempt four with `repeated_tool_error`. A separately counted repeated identical tool call is also blockable.

**Test:** `TestThreeErrorsBlockFourth`, `TestDifferentErrorResetsSequence`, `TestToolResultsDisabledWithoutToken`, `TestToolResultsRejectsWrongToken`, `TestNoToolArgumentsPersisted`. Include provider error repetition visible directly to the proxy.

**Focused validation:** `go test ./internal/policies ./internal/gateway -run 'TestThreeErrors|TestDifferentError|TestToolResults|TestNoToolArguments'`. **Acceptance:** the fourth matching request is blocked locally and no outside agent process is claimed to have been terminated.

### Task 9: Provider adapters and configuration

**Depends on:** 8.

**Files:** Create `internal/providers/openai.go`, `internal/providers/anthropic.go`, `internal/providers/anthropic_test.go`, `internal/providers/registry_test.go`, `tests/fixtures/anthropic_message.json`, `tests/fixtures/anthropic_stream.txt`; update `internal/providers/openai_compatible.go`, `internal/config/config.go`, and `configs/virgil.example.toml`.

**Interfaces:** `providers.NewRegistry(cfg config.Config, client *http.Client) (Registry, error)` supplies the Task 2 `Adapter` interface. OpenAI, Kimi, and arbitrary OpenAI-compatible endpoints share the protocol implementation but have independent configured URL, model mapping, capability flags, and key environment variable. Anthropic translates the supported Chat Completions subset to Messages and back, including tool use, usage, errors, and SSE. Reject unsupported features before network dispatch.

**Test:** table cases for OpenAI, Kimi at `https://api.moonshot.ai/v1`, and a local generic endpoint; Anthropic JSON/SSE/tool fixtures; missing usage; upstream errors; cancellation; and unsupported features. Fake HTTP servers only.

**Focused validation:** `go test ./internal/providers ./internal/gateway`. **Acceptance:** no model, price, or capability is hardcoded into core routing; all four configurations pass contract fixtures.

### Task 10: Usage, price version, and cost provenance

**Depends on:** 9.

**Files:** Create `internal/telemetry/usage.go`, `internal/telemetry/usage_test.go`, `internal/pricing/pricing.go`, `internal/pricing/pricing_test.go`, `configs/pricing.example.toml`; update `internal/telemetry/event.go` and `internal/gateway/chat.go`.

**Interfaces:** `pricing.Calculate(usage telemetry.Usage, table pricing.Table) (pricing.Cost, error)` uses decimal arithmetic and a configured version/currency; `telemetry.Usage` distinguishes provider, estimated, and unknown counts and keeps cached tokens as a subset of input. `Cost` populates `actual_cost` only for provider usage, `estimated_cost` only for estimated usage, and neither if usage or price is unknown.

**Test:** `TestMeasuredUsageActualCost`, `TestEstimatedUsageEstimatedCost`, `TestUnknownUsageHasNullCost`, `TestCachedTokensNotDoubleCounted`, and decimal precision. The tests label calculated cost as an estimate of billing, not a provider invoice.

**Focused validation:** `go test ./internal/telemetry ./internal/pricing ./internal/gateway`. **Acceptance:** events retain price version and calculation inputs and never fabricate a zero price.

### Task 11: Outbox recovery and local JSONL

**Depends on:** 10.

**Files:** Create `internal/export/outbox.go`, `internal/export/outbox_test.go`, `internal/export/jsonl.go`, `internal/export/jsonl_test.go`, `internal/storage/outbox.go`, `internal/storage/outbox_test.go`; update `cmd/virgil/main.go`.

**Interfaces:** `storage.Lease(ctx, destination string, limit int, now time.Time) ([]Delivery, error)` moves pending/retryable rows to sending; `Ack` marks only acknowledged IDs delivered; `Fail` schedules bounded exponential backoff with jitter or dead letter. Add `List(ctx context.Context, cursor string, limit int) ([]telemetry.Event, string, error)` to the journal for bounded pagination. `export.JSONL(ctx, journal storage.Journal, path string, allowed []string) error` writes explicit-path redacted canonical lines with event IDs. CLI: `virgil events export --format jsonl --output <path>`.

**Test:** `TestLeaseExpiresAfterRestart`, `TestDestinationADoesNotAckB`, `TestPermanentErrorDeadLetters`, `TestJSONLRequiresPathAndAllowlist`, `TestJSONLStableEventIDs`. A failed destination cannot delete its event or block local requests.

**Focused validation:** `go test ./internal/storage ./internal/export`. **Acceptance:** the five named outbox states transition correctly and local JSONL does not contact a network service.

### Task 12: OTLP/HTTP exporter

**Depends on:** 11.

**Files:** Create `internal/export/otlp.go`, `internal/export/otlp_test.go`, `internal/export/otel_mapping.go`, `internal/export/otel_mapping_test.go`; update `internal/config/config.go` and `configs/virgil.example.toml`.

**Interfaces:** `export.OTLP.Send(ctx context.Context, batch []storage.Delivery, allowed []string) (Ack, error)` sends valid OTLP trace protobuf to `/v1/traces`; `Ack` contains accepted event IDs. Mapping preserves trace/span IDs, token provenance, provider/model, latency, status, and optional Virgil policy/cost attributes. It emits no content attributes. The outbox retries HTTP 429/502/503/504 with jitter and honors `Retry-After`; permanent 4xx becomes dead letter.

**Test:** `TestOTLPPayloadDecodes`, `TestOTLPCanaryAbsent`, `TestRetryAfter429`, `TestPermanent400DeadLetter`, `TestCollectorReconnectDeliversOnce`. Use a local fake collector; test explicit destination and allowlist gates.

**Focused validation:** `go test ./internal/export ./internal/storage`. **Acceptance:** a collector can decode the payload and an offline collector does not interrupt local proxying.

### Task 13: Optional Control Plane client and remote-policy precedence

**Depends on:** 12.

**Files:** Create `internal/controlplane/client.go`, `internal/controlplane/client_test.go`, `internal/controlplane/policy.go`, `internal/controlplane/policy_test.go`, `internal/export/controlplane.go`, `internal/export/controlplane_test.go`; update `internal/config/config.go` and `internal/policies/policy.go`.

**Interfaces:** `controlplane.Client.Enroll(ctx, oneTimeToken string) (Enrollment, error)`, `SendBatch(ctx, []storage.Delivery) (BatchAck, error)`, `CurrentPolicy(ctx, etag string) (PolicyEnvelope, error)`. Enrollment stores a scoped credential in an OS secret store or explicitly configured ignored local file. `policies.Merge(local Policy, remote PolicyEnvelope) (Policy, error)` takes the stricter limit, rejects endpoint/export/content changes, and retains the last valid monotonic version.

**Test:** `TestBatchAcksOnlyAcceptedEvents`, `TestCredentialNeverInEvent`, `TestRemotePolicyCannotRaiseHardCap`, `TestStalePolicyIgnored`, `TestOfflineUsesLastValidPolicy`, `TestRevokedCredentialStopsRemoteOnly`. All remote tests use fake TLS endpoints and synthetic credentials.

**Focused validation:** `go test ./internal/controlplane ./internal/export ./internal/policies`. **Acceptance:** revocation stops batch/policy access while the gateway continues local requests, policy enforcement, and recording.

### Task 14: Public wire contract and compatibility suite

**Depends on:** 13.

**Files:** Create `schemas/controlplane.v1.openapi.yaml`, `schemas/policy.v1.example.json`, `docs/protocol/control-plane.md`, `tests/contract/controlplane_test.go`, `tests/e2e/local_gateway_test.go`, `tests/e2e/reconnect_test.go`; update `README.md`.

**Interfaces:** Document `POST /v1/installations/enroll`, `POST /v1/events/batch`, `GET /v1/policies/current`, and admin `POST /v1/installations/{id}/revoke`. Batch response names accepted event IDs and per-event rejection codes. Server derives tenant identity from the credential and deduplicates `(organization_id, installation_id, event_id)`; `organization_id` in payload is informational only.

**Test:** validate the OpenAPI document; contract tests exchange documented synthetic requests and responses with the Task 13 client; e2e tests prove JSON, SSE, Kimi-by-config, trace preservation, three-error blocking, offline recording, reconnect delivery, and privacy. No external provider or Control Plane is required.

**Focused validation:** `go test ./tests/contract ./tests/e2e`. **Acceptance:** a third-party service can implement the published schema without reading internal Go types.

### Task 15: Separate service, tenant identity, and ingestion

**Depends on:** 14 and selection of a separate Control Plane repository.

**Files in that repository:** Create `go.mod`, `cmd/controlplane/main.go`, `internal/auth/installation.go`, `internal/auth/installation_test.go`, `internal/ingest/batch.go`, `internal/ingest/batch_test.go`, `internal/ingest/enroll.go`, `internal/ingest/enroll_test.go`, `migrations/001_core.sql`, `migrations/002_events.sql`, `tests/contract/public_protocol_test.go`.

**Interfaces:** Enrollment exchanges a single-use token for a scoped, expiring installation credential; only a hash is stored server-side. Middleware resolves `organization_id` and `installation_id` from that credential. Ingest validates the public schema, refuses a mismatched claimed tenant, and inserts with unique `(organization_id, installation_id, event_id)`. PostgreSQL columns use `timestamptz` for times and `numeric` for calculated costs; indexes start with tenant columns. The application role has no superuser or bypass-RLS privilege.

**Test:** `TestCrossTenantEventRejected`, `TestDuplicateBatchAcknowledgedOnce`, `TestExpiredCredentialDenied`, `TestMixedBatchPerEventErrors`, and the public protocol compatibility test. Local PostgreSQL test instance is required; configure it with an ignored test URL.

**Focused validation:** `go test ./internal/auth ./internal/ingest ./tests/contract`. **Acceptance:** duplicate delivery is idempotent and an installation cannot write another organization's events.

### Task 16: Aggregation, retention, and revocation

**Depends on:** 15.

**Files in the service repository:** Create `internal/aggregate/rollup.go`, `internal/aggregate/rollup_test.go`, `internal/admin/installations.go`, `internal/admin/installations_test.go`, `internal/admin/retention.go`, `internal/admin/retention_test.go`, `migrations/003_rollups.sql`.

**Interfaces:** `aggregate.Rebuild(ctx, organizationID string, window TimeRange) error` computes total and per-project/agent/provider/model usage, calculated cost, errors, and latency from deduplicated events. `admin.RevokeInstallation` requires an organization administrator, writes an audit entry, and denies subsequent ingestion and policy reads. Retention deletes only the tenant's eligible records and respects configured periods.

**Test:** `TestAggregateTotalEqualsIndividualSlices`, `TestDuplicateDoesNotDoubleCost`, `TestRevocationRejectsBatchAndPolicy`, `TestRetentionDoesNotCrossTenant`. Use two synthetic organizations in the same database.

**Focused validation:** `go test ./internal/aggregate ./internal/admin`. **Acceptance:** total and individual cost views agree, and a revoked installation cannot use its credential.

### Task 17: Team interface, alerts, audit, and policy distribution

**Depends on:** 16.

**Files in the service repository:** Create `internal/web/dashboard.go`, `internal/web/dashboard_test.go`, `internal/web/templates/dashboard.html`, `internal/alerts/evaluate.go`, `internal/alerts/evaluate_test.go`, `internal/admin/audit.go`, `internal/admin/audit_test.go`, `internal/policy/current.go`, `internal/policy/current_test.go`, `migrations/004_policies_alerts_audit.sql`.

**Interfaces:** Tenant-scoped dashboard filters by project, environment, agent, provider, and model; shows total/individual cost, tokens, errors, and latency. Alert evaluation compares configured budget and failure thresholds with deduplicated aggregates and records one alert per threshold/window key. Versioned policy endpoint returns an ETag and only policy fields allowed by Task 13. Audit records actor, action, target, tenant, and time, excluding secret values.

**Test:** `TestDashboardTenantFilter`, `TestAlertDeduplicatedPerWindow`, `TestAuditRecordsPolicyChange`, `TestPolicyETagAndVersion`. Rendered HTML escapes labels.

**Focused validation:** `go test ./internal/web ./internal/alerts ./internal/admin ./internal/policy`. **Acceptance:** a team can see its cost and errors, receive an alert, inspect admin actions, and distribute a stricter policy.

### Task 18: Users, RBAC, OIDC, and enterprise SAML gate

**Depends on:** 17.

**Files in the service repository:** Create `internal/auth/roles.go`, `internal/auth/roles_test.go`, `internal/auth/oidc.go`, `internal/auth/oidc_test.go`, `internal/admin/users.go`, `internal/admin/users_test.go`, `migrations/005_users_teams_roles.sql`; for the enterprise milestone create `internal/auth/saml.go` and `internal/auth/saml_test.go` only after a separate SAML trust/metadata design review.

**Interfaces:** Roles distinguish organization admin, project operator, and viewer; authorization checks tenant and project before data access or policy/revocation changes. OIDC validates issuer, audience, signature, nonce, and expiry and maps an external subject to one tenant membership. SAML acceptance additionally requires signed assertions, audience/destination checks, replay protection, and rotation procedure.

**Test:** `TestViewerCannotRevoke`, `TestProjectOperatorCannotReadOtherProject`, `TestOIDCRejectsWrongIssuerAndAudience`, `TestOIDCSessionExpiry`; enterprise gate adds `TestSAMLReplayRejected` and `TestSAMLAudienceRejected`. Use fake identity providers and synthetic identities.

**Focused validation:** `go test ./internal/auth ./internal/admin`. **Acceptance:** authorization is server-side on every route; SAML is released only after its separate security review and tests.

### Task 19: Supervised Runner mode

**Depends on:** stable and released Tasks 1–14; Task 18 is not required.

**Files:** Create `internal/runner/runner.go`, `internal/runner/runner_test.go`, `cmd/virgil/run.go`, `docs/runner.md`.

**Interfaces:** `runner.Run(ctx context.Context, spec RunSpec) (RunResult, error)` starts a configured child process with a generated run ID and gateway URL, enforces a wall-clock deadline, and stops the child process on a local policy block. It passes provider keys only through the child's inherited environment when explicitly configured and never records stdout/stderr content by default.

**Test:** `TestRunnerStopsChildAtDeadline`, `TestRunnerStopsChildAfterPolicyBlock`, `TestRunnerDoesNotPersistChildOutput`. Use synthetic local child commands.

**Focused validation:** `go test ./internal/runner ./cmd/virgil`. **Acceptance:** Virgil can stop only a process it actually supervises and documents that boundary.

### Task 20: Python SDK and one framework integration

**Depends on:** 19.

**Files:** Create `sdk/python/pyproject.toml`, `sdk/python/virgil/__init__.py`, `sdk/python/virgil/client.py`, `sdk/python/virgil/context.py`, `sdk/python/tests/test_client.py`, `sdk/python/tests/test_context.py`, `examples/python/basic.py`, `docs/integrations/python.md`.

**Interfaces:** `VirgilClient(base_url, application_token=None)` sends OpenAI-compatible calls with `run_id` and `traceparent` and can submit structured tool results without payload bodies. `run_context()` groups requests; the integration example wraps one common Python agent loop without copying prompts into telemetry.

**Test:** `test_context_preserves_trace_id`, `test_tool_result_sends_metadata_only`, `test_client_streams_and_cancels`, `test_no_token_in_repr_or_logs`. Use a local fake gateway.

**Focused validation:** `python -m pytest sdk/python/tests`. **Acceptance:** one framework integration and the SDK demonstrate full run correlation while keeping the gateway usable without them.

## Shared validation gate for every public-repository checkpoint

Run the focused command for the task, then `go test ./...`, `go vet ./...`, `go build ./cmd/virgil`, and the synthetic privacy suite `go test ./tests/privacy` once those packages exist. For Task 1 before the privacy suite exists, scan changed files manually for secrets and private references. Review `git diff --check`, `git diff`, `git status --short`, and all untracked files. Commit only the completed checkpoint locally; inspect the configured remote without pushing.

Before claiming the Edge MVP, additionally run:

```text
go test -race ./...
go test ./tests/contract ./tests/e2e
GOOS=windows GOARCH=amd64 go build -o dist/virgil-windows-amd64.exe ./cmd/virgil
GOOS=darwin GOARCH=arm64 go build -o dist/virgil-darwin-arm64 ./cmd/virgil
GOOS=linux GOARCH=amd64 go build -o dist/virgil-linux-amd64 ./cmd/virgil
```

Use platform-appropriate shell syntax for the cross-build environment variables. Build artifacts stay ignored and local. The MVP passes only if the e2e suite demonstrates local startup, JSON and streaming forwarding, Kimi via configuration, token/latency and cost provenance, three-error block, offline recording and reconnect, OTLP decoding, redaction, trace preservation, service aggregation, installation revocation, and remote policy precedence. Service aggregation and revocation require the separate Control Plane repository and its tests; they are not implied by a green Edge-only build.

## Decision gates

Before Task 1, accept or revise Go as the gateway language and install a supported Go toolchain; Go was not installed on the planning machine. Before Task 15, choose the separate service repository and its ownership/license/deployment model. Before enterprise SAML in Task 18, approve a security design with metadata, signing, and key rotation. These gates do not block the local Edge tasks. Any change to an accepted architectural decision is explained for review before implementation.
