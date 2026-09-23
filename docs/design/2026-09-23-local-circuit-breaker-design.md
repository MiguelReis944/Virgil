# Virgil Local Circuit Breaker Design

Status: approved direction, 2026-09-23.

## Product decision

The first release is a local circuit breaker for AI agents. Its primary outcome is not another observability dashboard: Virgil must reject an unsafe or over-budget model call and terminate the process tree that owns the execution. It runs on one computer, requires no account or remote service, and keeps SQLite as its local source of truth.

The product has two user-facing entry points:

```text
virgil
virgil run -- <command> [args...]
```

`virgil` starts one persistent local product containing the API gateway, configuration UI, execution dashboard, policy engine, and SQLite storage on the same loopback port, then opens the panel in the browser. `virgil run` connects to that local core, registers a supervised execution, injects its identity and gateway connection, and supervises the complete process tree. The core remains running after the child exits, so history and configuration remain available in the panel. The existing `serve` spelling remains as a compatibility alias.

The release excludes LAN access, multi-user administration, Control Plane behavior, MCP tool interception, prompt management, evaluations, and enterprise identity. Those are separate product decisions. The README and setup UI must not present them as part of this release.

## Success criteria

A release candidate is complete when all of these statements are true:

1. One command starts the local core and opens the panel; a second explicit command starts a supervised application through that core.
2. Every supervised model call accepted by the local core is authenticated and bound to exactly one `run_id`.
3. A deterministic policy block rejects the provider call, records the block, and terminates the supervised process tree.
4. The behavior works on Windows, Linux, and macOS, including descendant processes.
5. An unavailable core prevents the child from starting. Loss of the authenticated control connection during execution terminates the child.
6. Provider credentials are never injected into the child unless the user explicitly supplies one through `--env`; the normal path gives the child only the short-lived run token.
7. The terminal and dashboard explain what policy fired, its threshold, the observed value, and the resulting process state.
8. Existing standalone proxy, JSON/SSE forwarding, local SQLite, privacy defaults, and budget accounting continue to pass their tests.

## Command contract

`virgil` accepts:

```text
virgil [--config virgil.toml] [--env-file .env] [--no-open]
```

It starts the API and panel on the configured loopback address. Unless `--no-open` is supplied, it opens `/dashboard` after readiness. This follows the useful OmniRoute convention of one local process, one canonical port, and browser-first configuration without copying OmniRoute's routing-focused information architecture.

`virgil run` accepts:

```text
virgil run \
  [--address http://127.0.0.1:8787] \
  [--run-id run_example] \
  [--deadline 30m] \
  [--env KEY=VALUE,KEY2=VALUE2] \
  -- command arg1 arg2
```

`--address` selects the running local core and must resolve to loopback. `--run-id` is optional and must pass the existing run ID validation. A generated ID is used otherwise. `--deadline` remains a hard wall-clock limit. `--env` remains an explicit escape hatch for child-specific variables.

The existing `--gateway` option becomes `--address`. It accepts only a trusted loopback Virgil core implementing the circuit-break control protocol.

On startup, the command prints only non-secret information:

```text
Virgil supervising run_abcd
Gateway: http://127.0.0.1:8787/v1
Panel: http://127.0.0.1:8787/dashboard/executions/run_abcd
```

On exit it prints a structured summary with `run_id`, final state, exit code, elapsed time, provider calls, tokens, recorded cost, and policy block details when present. It never prints the run token, provider credential, prompt, response, or raw tool data.

## Runtime architecture

The persistent core and Runner share a circuit-break protocol composed of five units:

| Unit | Responsibility |
|---|---|
| `runtime.LocalCore` | Serve the API gateway and panel on one configured loopback port and own storage, policy, and execution registration. |
| `runtime.ExecutionRegistry` | Register execution identities, authenticate traffic, persist lifecycle transitions, and publish one-shot circuit-break signals. |
| `runner.Process` | Start the root process in an operating-system process group or job object and terminate the whole tree. |
| `runner.ControlClient` | Authenticate to the local core, register and finish executions, and hold the blocking signal stream. |
| `runtime.Supervisor` | Order registration and process startup, race child exit against deadline, circuit break, cancellation, and control-connection loss, then report the final state. |

The core dependency direction is `cmd/virgil -> internal/runtime -> internal/gateway + internal/storage`. The Runner depends on `internal/runner` and its small control client. The gateway never imports process management. It publishes typed policy-block notices to the execution registry, which delivers them through the authenticated local control protocol.

Startup order:

1. Load the local core address and its installation control credential.
2. Generate or validate `run_id`.
3. Register the execution and receive a random run token plus a single-use signal token.
4. Open the authenticated signal stream and require its readiness event.
5. Start the child process tree with the injected run token and gateway environment.
6. Mark the execution `running` through the control API.

Shutdown order:

1. Select exactly one terminal cause.
2. If the child is still running, terminate its complete process tree.
3. Wait until the root process has been reaped.
4. Report the terminal execution state to the core.
5. Close the signal stream.
6. Print the execution summary returned by the core and return the appropriate exit code.

The supervisor owns the process and control connection and closes them in reverse creation order. Partial startup failures follow the same cleanup path. The child never starts if registration or signal-stream readiness fails.

## Execution identity and authentication

The persistent execution registry accepts multiple sequential or concurrent local execution identities:

```go
type ExecutionIdentity struct {
    RunID     string
    TokenHash [32]byte
}
```

The plaintext run token is returned once to the Runner and exists only in Runner memory and the child environment. The core stores its SHA-256 hash only while the execution is active and removes it at the terminal transition. Verification hashes the supplied token and compares fixed-size values with `subtle.ConstantTimeCompare`.

For OpenAI-compatible clients, the supervisor injects:

```text
VIRGIL_RUN_ID=<run_id>
VIRGIL_GATEWAY_URL=http://127.0.0.1:8787
OPENAI_BASE_URL=http://127.0.0.1:8787/v1
OPENAI_API_BASE=http://127.0.0.1:8787/v1
OPENAI_API_KEY=<run_token>
```

The gateway treats `Authorization: Bearer <run_token>` as local authentication, replaces any client-supplied `X-Virgil-Run-ID` with the bound `run_id`, and resolves the configured provider credential internally. A missing or invalid token returns `401` without evaluating policy or contacting a provider. A client cannot select another run by changing a header.

`VIRGIL_RUN_ID`, `VIRGIL_GATEWAY_URL`, and `VIRGIL_RUN_TOKEN` are also injected for explicit integrations. `/v1/tool-results` accepts the same run token in `X-Virgil-App-Token` and forces the bound run ID. The legacy global `VIRGIL_LOCAL_APP_TOKEN` remains available only to standalone `serve` mode.

The first circuit-breaker release supports OpenAI-compatible client traffic. It must stop injecting `ANTHROPIC_BASE_URL` until the gateway exposes a compatible inbound `/v1/messages` route. Provider adapters may still send outbound Anthropic Messages after receiving an OpenAI-compatible request.

The core creates `data/control.token` with mode `0600` on first start. `virgil run` reads this installation-local credential to call four loopback-only control endpoints:

```text
POST /api/executions
GET  /api/executions/{run_id}/signals
POST /api/executions/{run_id}/running
POST /api/executions/{run_id}/finish
```

The control credential is sent as a bearer token and compared in constant time. Registration returns the plaintext run token and a separate single-use signal token. The signal endpoint uses SSE, emits `ready` before the child may start, emits at most one `circuit_break` event, sends heartbeat comments every fifteen seconds, and closes after a terminal transition. A disconnected signal stream makes the Runner fail closed and terminate the process tree. Browser handlers never accept the control credential or run token.

## Policy block notification

The gateway dependencies gain a narrow callback:

```go
type PolicyBlockNotice struct {
    RunID     string
    Reason    string
    Policy    string
    Attempt   int64
    Threshold int64
    BlockedCallEstimateUSD string
    OccurredAt time.Time
}

type PolicyBlockNotifier interface {
    NotifyPolicyBlock(PolicyBlockNotice)
}
```

The execution registry supplies a notifier backed by `sync.Once` and a buffered subscriber channel of size one for each active run. Unsupervised API calls have no subscriber and retain request-blocking behavior without claiming process termination.

When `Preflight` returns `block`, the handler performs this order:

1. Build the canonical `policy_block` event.
2. Persist the event and policy decision.
3. Write the structured HTTP policy response.
4. Notify the supervisor once.

If persistence fails, the handler still sends the block notification because failing to write telemetry must not permit an unsafe process to continue. The notice records that persistence failed in structured local logs without including request content. Multiple simultaneous blocked requests produce one circuit break; later notifications are ignored.

Only policy decisions terminate the process. Provider errors, client cancellation, dashboard errors, exporter failures, and malformed requests do not become circuit breaks. A deadline is a distinct terminal cause.

## Process-tree termination

The Runner API becomes lifecycle-oriented:

```go
type Process interface {
    Wait() ProcessResult
    Terminate(context.Context) error
}

func Start(ctx context.Context, spec RunSpec) (Process, error)
```

Unix starts the child in a new process group with `Setpgid: true`. Termination sends `SIGTERM` to the negative process-group ID, waits up to three seconds, then sends `SIGKILL` and reaps the root process.

Windows creates a Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, assigns the child before it can create unsupervised descendants, and terminates the job on circuit break, deadline, gateway failure, or parent cancellation. Implementation uses `golang.org/x/sys/windows`; it must not invoke `taskkill` or construct shell commands.

macOS uses the Unix process-group implementation and has a dedicated CI smoke test. Tests must create a root process that spawns a descendant and prove both disappear. Platform-specific files use build tags:

```text
internal/runner/process_unix.go
internal/runner/process_windows.go
internal/runner/process_unix_test.go
internal/runner/process_windows_test.go
```

If tree termination fails, the terminal state is `termination_failed`, the command returns a nonzero exit status, and the error names only the process ID and operating-system error. Virgil must not claim that the circuit break succeeded.

## Persistence model

A new migration creates:

```sql
CREATE TABLE executions (
    run_id TEXT PRIMARY KEY,
    state TEXT NOT NULL CHECK (state IN (
        'starting', 'running', 'completed', 'failed', 'blocked',
        'deadline', 'interrupted', 'gateway_failed', 'termination_failed'
    )),
    started_at_unix_ns INTEGER NOT NULL,
    ended_at_unix_ns INTEGER,
    exit_code INTEGER,
    stop_reason TEXT,
    policy TEXT,
    policy_reason TEXT,
    policy_attempt INTEGER,
    policy_threshold INTEGER,
    blocked_call_estimate_usd TEXT
);

CREATE INDEX executions_state_started_idx
    ON executions(state, started_at_unix_ns DESC);
```

The command line and environment are not persisted. `blocked_call_estimate_usd` describes only the rejected provider call and is populated only when a configured estimate exists. The UI must never label it “cost saved” or extrapolate hypothetical future calls.

Storage exposes transactional lifecycle methods:

```go
func (j *Journal) StartExecution(ctx context.Context, runID string, startedAt time.Time) error
func (j *Journal) MarkExecutionRunning(ctx context.Context, runID string) error
func (j *Journal) FinishExecution(ctx context.Context, result ExecutionResult) error
func (j *Journal) Execution(ctx context.Context, runID string) (Execution, error)
func (j *Journal) ListExecutions(ctx context.Context, limit int) ([]Execution, error)
```

Transitions are conditional: `starting -> running -> terminal`, with `starting -> failed` allowed for a child startup failure. Terminal states cannot be overwritten. On startup, executions left in `starting` or `running` by a previous Virgil crash become `interrupted` with reason `virgil_restart`; this recovery happens before a new execution is inserted.

## Dashboard and terminal experience

The panel is the primary product shell. API and panel share the canonical origin. Persistent navigation contains Overview, Executions, Protections, Providers, Usage, Health, and Settings. The initial setup wizard becomes the empty-state experience inside this shell instead of a visually separate page. Following OmniRoute's useful onboarding pattern, provider setup and copyable integration values are visible in the panel; Virgil keeps its own protection-first hierarchy and does not add routing combos or unrelated provider-marketplace features.

The Executions view is ordered by start time. Each row shows:

- execution ID;
- current or terminal state;
- start time and duration;
- provider call count, tokens, and recorded cost;
- block reason and policy when present;
- whether process-tree termination succeeded.

The execution detail page shows its event timeline and a prominent circuit-break card. The card uses exact language: “Request blocked” and “Process tree terminated.” If termination failed, it displays “Termination failed” and never presents the execution as protected.

The panel keeps the existing dashboard-password session authentication. Run and control tokens authenticate API traffic only and are never accepted as a dashboard password. When no dashboard password is configured, the existing open loopback behavior remains and the core prints a warning. No token appears in a query string, log, HTML page, or persisted cookie.

The CLI summary is part of the acceptance contract and is tested independently from visual templates. Dashboard query code gets package tests against a temporary migrated SQLite database; the current dashboard has no test coverage and cannot remain that way after execution state is added.

## Failure behavior

| Failure | Required behavior |
|---|---|
| Invalid configuration | Do not start gateway or child; return actionable error. |
| Database or migration failure | Do not start child. |
| Listener or handler failure | Do not start child. |
| Child start failure | Persist `failed`, stop gateway, return nonzero. |
| Policy persistence failure | Reject call, notify circuit break, terminate child, log normalized code. |
| Signal stream or local core connection is lost | Terminate child, return nonzero, and persist `gateway_failed` when connectivity returns. |
| Deadline reached | Terminate tree and persist `deadline`. |
| User interrupt | Terminate tree and persist `interrupted`. |
| Child exits normally | Persist `completed` with its exit code. |
| Child exits nonzero | Persist `failed` with its exit code. |
| Process-tree termination fails | Persist `termination_failed` and return nonzero. |
| SQLite final-state write fails | Return nonzero after process cleanup; do not rewrite history optimistically. |

The first terminal cause wins through `sync.Once`; shutdown errors are joined to that cause for the CLI while the persisted state reflects the operational outcome.

## Security and privacy constraints

- Bind the persistent API and panel core to the configured loopback address only.
- Generate the run token with `crypto/rand`; use at least 32 bytes.
- Never log, persist, return, or place the token in a URL.
- For authenticated supervised traffic, replace every client-provided run ID with the identity registered by the core.
- Do not pass configured provider credentials to the child.
- Keep prompts and responses disabled unless the existing explicit capture settings enable them.
- Keep raw tool arguments disabled.
- Reject redirects from provider endpoints as the adapters do today.
- Apply request size and timeout controls before authentication work that allocates proportional memory.
- Do not introduce a shell to start or terminate the child.
- Preserve the current fail-closed behavior for unavailable policy storage.

The run token protects the provider credential from other local processes that do not possess the child environment. It does not defend against an administrator, debugger, or same-user process able to inspect another process environment. That limitation is documented plainly.

## Testing strategy

Unit tests cover token verification, run ID binding, one-shot notifications, lifecycle transition validation, terminal-cause precedence, CLI summaries, and platform process-tree termination.

Integration tests use a synthetic child executable built from `tests/fixtures/supervised-child`. It can issue authenticated requests, deliberately exceed a call limit, spawn a descendant, and write PID markers to a temporary directory. Tests verify:

1. Core registration and the signal stream are ready before the first child request.
2. The child sees the expected environment without provider credentials.
3. An invalid run token returns `401` and does not increment policy counters.
4. A forged run ID is replaced by the bound identity.
5. Exceeding `max_requests_per_run` returns the policy error and terminates root and descendant processes.
6. Exactly one `policy_block` event and one `blocked` execution are stored.
7. Normal exit stores `completed` and preserves the child's exit code.
8. Deadline and interrupt produce their distinct states.
9. Loss of the core signal connection terminates the tree.
10. Restart recovery marks abandoned active executions `interrupted`.

CI runs `go test -race -timeout 180s ./...` and `go vet ./...` on Ubuntu, Windows, and macOS. Process tests that require OS-specific primitives must run on their native platform and may not be replaced solely by mocked unit tests.

## Delivery stages

### Stage 1: Honest product surface

Update README, setup copy, CLI help, and architecture documentation to present Virgil as a single-machine circuit breaker. Remove the Control Plane and LAN promises from the primary path. Document the difference between standalone proxy blocking and supervised process termination.

### Stage 2: Execution identity

Add the installation control credential and short-lived run-token authentication, bind requests to the supervisor-owned run ID, and ensure configured provider credentials remain in the core. This prevents another ordinary local client from registering executions or spending a configured key.

### Stage 3: Persistent lifecycle

Add the executions migration, strict transitions, recovery of abandoned states, and query APIs. The CLI can now report durable execution outcomes even before automatic termination is connected.

### Stage 4: Process-tree ownership

Refactor the Runner into a start/wait/terminate lifecycle and implement native Unix process groups and Windows Job Objects. Prove descendant termination on all supported platforms.

### Stage 5: Persistent core supervision protocol

Create execution registration, signal, running, and finish endpoints plus `runner.ControlClient`. Make the persistent API-and-panel core the default `virgil` command, open the browser after readiness, and make `virgil run` fail closed when the local core is unavailable.

### Stage 6: Circuit-break signal

Persist policy blocks, deliver one typed notice to the supervisor, terminate the tree, and persist the final `blocked` state. This is the first feature-complete circuit-breaker milestone.

### Stage 7: Action-oriented product experience

Make the panel the primary product shell with navigation for Overview, Executions, Protections, Providers, Usage, Health, and Settings. Add terminal summaries as a secondary interface, dashboard database tests, and remove unsupported claims from the UI.

### Stage 8: Release hardening

Run cross-platform race tests, security scans, privacy canaries, crash recovery tests, build signed or checksummed binaries, write installation instructions, and publish an acceptance checklist. LAN mode and MCP control start only after this gate passes.

## Deferred follow-up plans

The following work must not be folded into the circuit-breaker release:

1. Secure LAN appliance mode with API clients, TLS termination, login throttling, and protected setup.
2. MCP and tool-execution gateway with argument policies and approval flow.
3. OpenAI Responses API and inbound Anthropic Messages compatibility.
4. Local notifications and webhooks.
5. Automatic price catalog maintenance.
6. Control Plane removal or archival beyond documentation and default-path cleanup.

Each item changes a distinct trust boundary or public protocol and requires its own design and implementation plan.
