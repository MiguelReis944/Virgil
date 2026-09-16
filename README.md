# Virgil

## Open LLM Gateway and Guardrails for AI Agents

Virgil is an open source, local-first gateway for observing, controlling, and protecting LLM-powered applications.

It runs next to your application, intercepts calls to providers such as OpenAI, Anthropic, and Kimi, enforces safety and budget policies, stores telemetry locally when needed, and optionally sends selected metadata to a centralized Control Plane.

The Edge Gateway is designed to work without an account, without a mandatory cloud service, and without sending prompts or responses anywhere by default.

> Status: architecture and product definition phase. The interfaces described here are the target design and may change before the first stable release.

---

## Why Virgil exists

LLM agents can fail in ways that are expensive and difficult to diagnose:

- an agent repeatedly calls the same tool after receiving the same error;
- a workflow exceeds its expected token budget;
- a model silently changes between environments;
- a tool returns malformed data and causes an execution loop;
- an application sends a large context repeatedly;
- a provider request remains open for too long;
- a team cannot identify which user, project, or agent caused the cost;
- a production incident cannot be reconstructed from the available logs.

Most existing observability tools show that something went wrong after the fact. Virgil is designed to observe the execution and enforce policies while it is still running.

---

## Product model

Virgil has two complementary components.

### Edge Gateway

The Edge Gateway is open source and runs on the user's machine, server, container, or development environment.

It is responsible for:

- receiving LLM requests through a local HTTP endpoint;
- forwarding requests to the selected provider;
- normalizing provider responses;
- recording execution metadata;
- enforcing local guardrails;
- buffering telemetry in SQLite;
- exporting telemetry to OpenTelemetry or a Virgil Control Plane;
- continuing to work when the network or Control Plane is unavailable.

### Control Plane

The Control Plane is the optional centralized service for teams and organizations.

It is responsible for:

- organizations, users, teams, and projects;
- centralized dashboards;
- cost aggregation;
- global budgets;
- audit trails;
- SSO and RBAC;
- alert routing;
- policy distribution;
- connected installation management;
- retention and export controls.

The Edge Gateway remains functional without the Control Plane.

---

## Architecture

```text
┌──────────────────────────────┐
│        AI application        │
│     agent, framework, SDK    │
└──────────────┬───────────────┘
               │ OpenAI-compatible HTTP
               ▼
┌──────────────────────────────┐
│       Virgil Edge Gateway    │
│                              │
│  Provider adapters           │
│  Policy engine               │
│  Execution tracker           │
│  Redaction pipeline          │
│  SQLite event journal        │
│  Reliable telemetry exporter │
└──────────────┬───────────────┘
               │ direct provider request
               ▼
┌──────────────────────────────┐
│       LLM provider APIs      │
│ OpenAI · Anthropic · Kimi    │
│ OpenAI-compatible endpoints  │
└──────────────────────────────┘

Optional:

┌──────────────────────────────┐
│       Virgil Control Plane   │
│                              │
│ Ingestion API                │
│ Tenant isolation             │
│ Cost aggregation             │
│ Dashboards and alerts        │
│ SSO, RBAC, audit             │
│ Policy distribution          │
└──────────────────────────────┘
```

The Edge Gateway is the data-plane component. The Control Plane is the optional control-plane component.

---

## Operating modes

### Local mode

```text
Application → Edge Gateway → Provider
```

- no account required;
- no network connection to Virgil;
- events stored locally in SQLite;
- local policies still apply;
- suitable for development, personal use, and privacy-sensitive workloads.

### Team mode

```text
Application → Edge Gateway → Provider
                         └──→ Control Plane
```

- each installation is registered to an organization;
- selected telemetry is sent to the Control Plane;
- costs are aggregated across users and projects;
- global policies can be distributed to gateways;
- prompts and responses remain disabled by default.

### Custom exporter mode

The gateway can export telemetry to systems that already exist in the user's infrastructure:

- OpenTelemetry Collector;
- Grafana;
- Jaeger;
- Datadog;
- an internal HTTP endpoint;
- a custom event consumer.

Virgil should not force a company to use the hosted Control Plane.

---

## Core principles

### Local-first

The gateway must remain useful when disconnected from the Control Plane.

### Provider-neutral

Policies and telemetry should work across providers instead of being tied to one vendor.

### Privacy by default

Prompts, responses, hidden reasoning, and tool arguments are not persisted or exported by default.

### Deterministic guardrails

The first guardrails should be based on explicit, testable rules rather than another LLM judging the first LLM.

### Open interoperability

The event model should be compatible with OpenTelemetry and documented well enough for alternative servers and exporters to be built.

### No provider key exfiltration

Provider API keys remain at the edge. The Control Plane does not need access to OpenAI, Anthropic, Kimi, or other provider credentials.

---

## Supported providers

The first provider integrations are planned as follows:

| Provider | Integration |
|---|---|
| OpenAI | Native adapter and OpenAI-compatible HTTP |
| Anthropic | Native adapter |
| Kimi | OpenAI-compatible adapter |
| Other providers | Configurable OpenAI-compatible endpoint |

Kimi provides a chat completions endpoint at `https://api.moonshot.ai/v1/chat/completions` and documents support for streaming, tool calling, and usage information.

The Kimi adapter should use configuration rather than hardcoded model names:

```toml
[providers.kimi]
type = "openai-compatible"
base_url = "https://api.moonshot.ai/v1"
api_key = "${MOONSHOT_API_KEY}"
model = "kimi-model"
```

Provider model names, pricing, and capabilities must remain configurable because they change over time.

---

## Guardrails

Guardrails are evaluated locally before a request is sent, while a stream is active, and after tool calls are observed.

Initial policy types:

- maximum cost per execution;
- maximum input tokens;
- maximum output tokens;
- maximum number of provider calls;
- maximum execution duration;
- maximum tool calls;
- allowed providers;
- allowed models;
- allowed tools;
- repeated tool call detection;
- repeated error detection;
- context growth detection;
- rate limits;
- required metadata fields.

### Repeated error example

A policy can identify a repeated failure using:

```text
tool name
normalized tool arguments
error type
error code
provider
```

After three equivalent failures, the gateway can:

1. stop forwarding the next request;
2. return a structured policy error;
3. write an audit event;
4. emit a local alert;
5. send a remote alert when the Control Plane is available.

### Important limitation

A proxy can block requests that pass through it. It cannot terminate an arbitrary agent process that is looping entirely outside the gateway.

For stronger control, Virgil will eventually support a Runner or SDK mode in which Virgil owns or supervises the agent loop.

---

## Observability data

Virgil should record a provider-neutral execution model.

Example identifiers:

```text
organization_id
installation_id
project_id
environment
agent_id
run_id
trace_id
span_id
```

Example event fields:

```json
{
  "event_type": "llm.response.completed",
  "trace_id": "trace_01H...",
  "run_id": "run_01H...",
  "installation_id": "install_01H...",
  "provider": "kimi",
  "requested_model": "kimi-model",
  "response_model": "kimi-model",
  "input_tokens": 1240,
  "output_tokens": 318,
  "latency_ms": 1840,
  "finish_reason": "stop",
  "cost": {
    "amount": 0.0042,
    "currency": "USD",
    "calculation": "provider_usage",
    "pricing_version": "2026-01-01"
  },
  "content_capture": false
}
```

The event schema should use OpenTelemetry-compatible names wherever possible.

The following data is disabled by default:

- raw prompts;
- raw model responses;
- hidden reasoning or reasoning traces;
- raw tool arguments;
- user secrets;
- provider API keys.

If content capture is enabled, it must be explicit, locally configurable, and subject to redaction rules.

---

## Cost accounting

Virgil separates usage from pricing.

### Usage

Usage is collected from provider responses whenever available:

- input tokens;
- output tokens;
- cached tokens;
- reasoning tokens when a provider exposes them;
- request duration;
- number of tool calls;
- number of retries.

### Pricing

Pricing is stored separately in a versioned catalog.

This allows Virgil to:

- recalculate historical costs;
- support custom provider endpoints;
- distinguish actual and estimated costs;
- update prices without changing historical usage;
- show which pricing version produced a report.

If a provider does not return usage data, the event must be marked as estimated rather than silently presenting an exact amount.

---

## Reliable telemetry delivery

The Edge Gateway uses a local outbox pattern:

```text
1. Create event
2. Apply redaction
3. Persist event in SQLite
4. Assign idempotency key
5. Attempt export
6. Receive server acknowledgment
7. Mark event as delivered
8. Retry failed events later
```

The gateway must tolerate:

- no network connection;
- temporary Control Plane failure;
- server timeouts;
- duplicate delivery;
- process restarts;
- partial batch failures;
- policy version changes.

The Control Plane must deduplicate events by idempotency key.

---

## Installation identity

Each connected installation receives its own identity.

An installation represents one gateway deployment on one computer, server, container, or environment.

The connection model is planned as:

1. An administrator creates an organization and project.
2. The Control Plane generates an enrollment token.
3. The user registers the local gateway.
4. The gateway creates or receives an installation identity.
5. The user stores the credential locally.
6. The gateway sends only the telemetry allowed by policy.

Installation credentials must support:

- revocation;
- rotation;
- expiration;
- environment separation;
- audit history;
- least-privilege ingestion.

The provider key and the Control Plane credential are different secrets.

---

## Example configuration

```toml
[server]
listen = "127.0.0.1:8787"
log_level = "info"

[storage]
driver = "sqlite"
path = "./data/virgil.db"
retention_days = 30

[privacy]
capture_prompts = false
capture_responses = false
capture_tool_arguments = false
capture_reasoning = false
redact_secrets = true

[guardrails]
max_requests_per_run = 40
max_cost_per_run_usd = 1.00
max_duration_seconds = 300
repeated_error_threshold = 3

[control_plane]
enabled = false
endpoint = "https://control.example.com"
installation_token = "${VIRGIL_INSTALLATION_TOKEN}"
export_content = false

[telemetry]
enabled = true
exporter = "local"

[providers.openai]
type = "openai"
api_key = "${OPENAI_API_KEY}"

[providers.anthropic]
type = "anthropic"
api_key = "${ANTHROPIC_API_KEY}"

[providers.kimi]
type = "openai-compatible"
base_url = "https://api.moonshot.ai/v1"
api_key = "${MOONSHOT_API_KEY}"
```

Secrets should be provided through environment variables, the operating system's secret store, or an ignored local configuration file.

They must never be committed to Git.

---

## Application integration

Applications should be able to use Virgil by changing their base URL:

```python
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["OPENAI_API_KEY"],
    base_url="http://127.0.0.1:8787/v1"
)
```

The provider key remains the provider key. Virgil receives the request, evaluates policies, and forwards it to the configured provider.

The same approach should work with:

- OpenAI-compatible SDKs;
- command-line agents;
- local agent frameworks;
- web applications;
- background workers;
- CI jobs;
- containerized services.

---

## Control Plane capabilities

The hosted Control Plane is intended for teams that need shared visibility and governance.

### Dashboards

- total spend;
- spend by user;
- spend by project;
- spend by model;
- spend by provider;
- token consumption;
- latency percentiles;
- error rates;
- repeated-tool-call incidents;
- policy blocks;
- active installations;
- historical comparisons.

### Identity and access

- organizations;
- teams;
- users;
- projects;
- environments;
- RBAC;
- OIDC SSO;
- SAML SSO for enterprise plans;
- API tokens;
- installation management.

### Audit

The Control Plane should record administrative actions such as:

- policy changes;
- user invitations;
- role changes;
- installation registration;
- installation revocation;
- alert changes;
- data-retention changes;
- export configuration changes.

### Alerts

Alerts can be triggered by:

- budget thresholds;
- unusual cost spikes;
- repeated provider errors;
- repeated tool failures;
- latency regressions;
- context-size growth;
- blocked policy decisions;
- disconnected installations;
- export failures.

---

## Commercial model

The Edge Gateway is planned to remain open source.

The Control Plane SaaS is the paid product.

Initial packaging hypothesis:

| Plan | Intended use | Features |
|---|---|---|
| Community | Individuals and evaluation | Local gateway, SQLite, local policies, local export |
| Team | Small teams | Shared dashboards, cost aggregation, alerts, projects, basic RBAC |
| Business | Growing organizations | SSO, audit history, global policies, longer retention, advanced alerts |
| Enterprise | Regulated or large organizations | Custom retention, mTLS, advanced access control, support, deployment options |

Pricing, execution limits, and retention periods will be validated against infrastructure costs before launch.

The commercial boundary should remain clear:

- the gateway is usable without the SaaS;
- the SaaS adds coordination, administration, and shared visibility;
- provider keys never need to move to the SaaS;
- the event protocol remains documented and interoperable.

---

## Privacy and security

Virgil is designed around a local-first privacy model.

### Default behavior

- no mandatory cloud account;
- no mandatory telemetry upload;
- no prompt storage;
- no response storage;
- no hidden reasoning storage;
- no provider key upload;
- no cross-customer data sharing;
- no silent content capture.

### Hosted mode

When the Control Plane is enabled:

- the organization chooses the exported fields;
- metadata is sent over TLS;
- installation credentials can be revoked;
- events are scoped to a tenant;
- event payloads are redacted before export;
- the local gateway records export status;
- the user can return to local mode.

### Public repository policy

The public repository must contain:

- synthetic examples;
- fake API keys;
- generic fixtures;
- no private project names;
- no personal vault data;
- no private infrastructure endpoints;
- no real customer data;
- no credentials;
- no copied work history.

A privacy test should fail the build if prohibited files, tokens, or project-specific references are introduced.

---

## Distribution

The Edge Gateway is intended to be distributed as:

- Windows binary;
- macOS binary;
- Linux binary;
- Docker image;
- source build;
- package-manager distribution when stable.

The first distribution target should be a single static or mostly self-contained binary with a local HTTP server and SQLite storage.

The Control Plane may be offered as:

- hosted SaaS;
- private deployment for enterprise customers;
- a future self-hosted edition, subject to licensing and support decisions.

---

## Repository structure

The planned public repository may use the following structure:

```text
virgil/
├── cmd/
│   └── virgil/
├── internal/
│   ├── gateway/
│   ├── providers/
│   ├── policies/
│   ├── telemetry/
│   ├── storage/
│   ├── redaction/
│   └── controlplane/
├── proto/
├── schemas/
├── configs/
├── examples/
├── docs/
├── tests/
├── Dockerfile
├── LICENSE
└── README.md
```

The repository should contain the Edge Gateway and public protocol definitions.

The hosted Control Plane may live in a separate private repository while keeping its public API and event protocol documented.

---

## Development roadmap

### Phase 1 — Local gateway

- local HTTP server;
- OpenAI-compatible request forwarding;
- provider abstraction;
- SQLite event journal;
- basic usage capture;
- timeout and request limits;
- local configuration;
- synthetic integration tests.

### Phase 2 — Provider coverage

- Anthropic adapter;
- Kimi adapter;
- configurable OpenAI-compatible endpoints;
- streaming support;
- provider error normalization;
- pricing catalog structure.

### Phase 3 — Guardrails

- repeated tool-call detection;
- repeated error detection;
- execution budgets;
- policy decisions;
- structured policy errors;
- local alert hooks;
- policy test fixtures.

### Phase 4 — Telemetry export

- OpenTelemetry traces and metrics;
- OTLP/HTTP exporter;
- reliable local outbox;
- redaction pipeline;
- custom HTTP exporter;
- delivery and deduplication tests.

### Phase 5 — Control Plane

- tenant and organization model;
- ingestion API;
- installation registration;
- event deduplication;
- cost aggregation;
- user and project dimensions;
- first dashboard;
- local-to-server synchronization.

### Phase 6 — Team governance

- RBAC;
- global policies;
- budget alerts;
- audit history;
- installation management;
- OIDC SSO;
- retention controls.

### Phase 7 — Advanced execution control

- Runner mode;
- Python SDK;
- agent lifecycle supervision;
- framework integrations;
- remote policy updates;
- enterprise deployment options.

---

## What Virgil does not try to do

Virgil is not intended to:

- replace the LLM provider;
- train or fine-tune models;
- inspect hidden model reasoning;
- guarantee that a model is factually correct;
- automatically approve unsafe tool calls using another opaque model;
- require all telemetry to pass through Virgil's cloud;
- store complete conversations by default;
- act as a full workflow engine in the first release.

Its first responsibility is to make LLM executions observable, bounded, and accountable.

---

## Limitations

A gateway can only enforce policies over traffic that passes through it.

If an agent:

- calls a provider directly;
- executes tools without the gateway;
- loops internally without making another intercepted request;
- runs with a provider-specific transport that is not supported;

then the gateway may not be able to observe or stop the entire execution.

Use Runner mode or an SDK when full lifecycle control is required.

---

## Contributing

Contributions are welcome.

Before opening a pull request:

1. read the contribution guidelines;
2. avoid adding provider secrets or real payloads;
3. use synthetic test fixtures;
4. add tests for privacy-sensitive behavior;
5. document provider-specific assumptions;
6. preserve the provider-neutral event model;
7. run the complete test suite;
8. explain any compatibility or security impact.

Provider adapters should not introduce provider credentials into the Control Plane.

---

## License

The Edge Gateway is intended to be released under the MIT License.

The public protocol, schemas, examples, and documentation will be licensed according to the files in this repository.

The Virgil hosted Control Plane is a separate commercial service. Availability, licensing, and self-hosting options for the Control Plane will be documented separately.

---

## Disclaimer

Virgil is an observability and guardrails tool. It does not guarantee that an agent, provider, tool, or model is safe or correct.

Organizations remain responsible for configuring policies, protecting credentials, reviewing telemetry, and complying with applicable privacy and data-retention requirements.
```

Esse README já separa claramente o que é open source, o que pertence ao SaaS e quais são os limites técnicos do proxy.
