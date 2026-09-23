# Virgil Local Circuit Breaker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the unified Virgil gateway and panel into a single-machine product that authenticates supervised executions, terminates their complete process trees after deterministic policy blocks, and presents configuration and outcomes primarily through the local panel.

**Architecture:** A persistent loopback core owns the API gateway, browser panel, SQLite, policies, and active execution registry on one port. `virgil run` authenticates to that core, registers a run, holds an SSE circuit-break channel, and owns the child process tree through Unix process groups or Windows Job Objects. The panel remains available before, during, and after executions.

**Tech Stack:** Go 1.26+, `net/http`, SSE, `crypto/rand`, `crypto/sha256`, `crypto/subtle`, `database/sql`, modernc SQLite, `golang.org/x/sys/windows`, server-rendered HTML, GitHub Actions.

**Spec:** `docs/design/2026-09-23-local-circuit-breaker-design.md`

## Global Constraints

- The product runs on one computer and binds only to a loopback address.
- API gateway and panel use the same canonical origin and port.
- The panel is the primary interface for setup, providers, protections, executions, usage, health, and settings.
- `virgil run` must fail closed when the core or its signal stream is unavailable.
- Configured provider credentials remain in the core and are never injected into supervised children.
- Run tokens and the installation control credential are never logged, returned in URLs, stored in events, or rendered in HTML.
- Prompts and responses remain disabled unless the existing explicit capture settings enable them; tool arguments remain unsupported.
- Do not add LAN access, MCP interception, Control Plane dependencies, routing combos, or enterprise identity in this plan.
- Every task ends with focused tests, `go test` for affected packages, `go vet` when interfaces change, and a local checkpoint commit.
- Before editing `.github/workflows/ci.yml`, load the `github-actions` skill.

---

## File Map

| Path | Responsibility |
|---|---|
| `cmd/virgil/main.go` | CLI dispatch; default local-core command; compatibility alias for `serve`. |
| `cmd/virgil/run.go` | Parse supervision flags and invoke the supervisor. |
| `internal/app/core.go` | Construct and run the persistent gateway-and-panel core on a pre-bound listener. |
| `internal/app/browser.go` | Open the panel URL without shell interpolation. |
| `internal/controlauth/credential.go` | Create, load, hash, and verify the installation control credential. |
| `internal/executions/types.go` | Execution states, lifecycle records, registration and finish contracts. |
| `internal/executions/registry.go` | Active run tokens, subscribers, transitions, one-shot notices. |
| `internal/executions/http.go` | Loopback control endpoints and SSE signal stream. |
| `internal/storage/migrations/008_executions.sql` | Durable execution lifecycle schema. |
| `internal/storage/executions.go` | Transactional lifecycle persistence and recovery. |
| `internal/gateway/execution_auth.go` | Bind authenticated supervised traffic to a registered run. |
| `internal/gateway/chat.go` | Use bound identity and synchronously publish policy-block notices. |
| `internal/gateway/tool_results.go` | Apply the same bound execution identity to tool feedback. |
| `internal/runner/process.go` | Platform-neutral process lifecycle interface. |
| `internal/runner/process_unix.go` | Unix process group startup and termination. |
| `internal/runner/process_windows.go` | Windows Job Object startup and termination. |
| `internal/runner/control_client.go` | Registration, signal stream, running and finish calls. |
| `internal/runner/supervisor.go` | Race child exit, block, deadline, interruption, and connection loss. |
| `internal/dashboard/layout.go` | Shared panel shell and navigation. |
| `internal/dashboard/executions.go` | Execution list and detail pages. |
| `internal/dashboard/protections.go` | Protection settings and explanatory status. |
| `internal/dashboard/providers.go` | Provider setup and integration values. |
| `internal/dashboard/overview.go` | Protection-first overview. |
| `internal/settings/store.go` | Validated atomic TOML updates initiated by the panel. |
| `tests/fixtures/supervised-child/main.go` | Synthetic child used by full supervision tests. |
| `tests/e2e/circuit_breaker_test.go` | Full core → child → policy block → tree termination proof. |

---

## Stage 1: Honest Unified Product Surface

### Task 1: Make the local core the default command

**Files:**
- Modify: `cmd/virgil/main.go`
- Create: `internal/app/core.go`
- Create: `internal/app/browser.go`
- Create: `internal/app/browser_windows.go`
- Create: `internal/app/browser_darwin.go`
- Create: `internal/app/browser_linux.go`
- Test: `cmd/virgil/main_test.go`
- Test: `internal/app/core_test.go`

**Interfaces:**
- Produces: `app.CoreOptions`, `app.RunCore(context.Context, app.CoreOptions) error`, `app.OpenBrowser(string) error`.
- Consumes: existing config loading, `buildHandlerWithEngine`, `gateway.ListenAndServe`, and signal cancellation.

- [ ] **Step 1: Write command-dispatch tests**

Add table tests proving that empty arguments select the core command, `serve` remains an alias, and `run` remains separate. Extract parsing into:

```go
type commandKind string

const (
    commandCore commandKind = "core"
    commandRun  commandKind = "run"
)

func classifyCommand(args []string) (commandKind, []string, error)
```

Expected cases: `nil -> core`, `[]string{"serve"} -> core`, `[]string{"run", "--", "agent"} -> run`, and unknown names return the existing actionable error.

- [ ] **Step 2: Run the failing tests**

Run: `go test ./cmd/virgil ./internal/app -run 'TestClassifyCommand|TestCoreOptions'`

Expected: FAIL because `internal/app` and `classifyCommand` do not exist.

- [ ] **Step 3: Extract persistent core construction**

Create:

```go
type CoreOptions struct {
    ConfigPath string
    EnvPath    string
    OpenPanel  bool
}

func RunCore(ctx context.Context, options CoreOptions) error
```

Move configuration, SQLite pools, retention, handler construction, and HTTP serving out of `run()` into `RunCore`. Add `gateway.Serve(ctx context.Context, listener net.Listener, handler http.Handler) error`, and make `ListenAndServe` delegate to it, so the listener can be bound before readiness is announced. Preserve cleanup order. Open `http://<listen>/dashboard` only after the listener is live. Browser launch errors become warnings and never stop the core.

- [ ] **Step 4: Implement shell-free browser launch**

Use platform files with direct argument arrays:

```go
// Windows
exec.Command("rundll32", "url.dll,FileProtocolHandler", panelURL).Start()

// macOS
exec.Command("open", panelURL).Start()

// Linux
exec.Command("xdg-open", panelURL).Start()
```

Validate that the URL scheme is `http`, hostname is loopback, and path begins with `/dashboard` before launching.

- [ ] **Step 5: Make `virgil` browser-first**

Support `--config`, `--env-file`, and `--no-open` on both the default command and `serve`. Update help to call the browser surface “panel”. Do not change `run` yet.

- [ ] **Step 6: Verify and commit**

Run:

```text
gofmt -w cmd/virgil internal/app
go test ./cmd/virgil ./internal/app
go vet ./cmd/virgil ./internal/app
```

Commit: `feat: make local panel the default Virgil experience`

### Task 2: Align documentation and panel vocabulary

**Files:**
- Modify: `README.md`
- Modify: `docs/runner.md`
- Modify: `docs/design/2026-09-16-virgil-architecture.md`
- Modify: `internal/gateway/setup.go`
- Test: `tests/privacy/scan_test.go`

**Interfaces:**
- Produces: one public product story: local core, panel, and supervised execution.
- Consumes: terminology in `CONTEXT.md`.

- [ ] **Step 1: Add a documentation assertion test**

Read `README.md` in a test and assert it contains `Local circuit breaker`, `virgil run`, and `/dashboard`, and does not describe the Control Plane as the product's required second component.

- [ ] **Step 2: Rewrite the first-use documentation**

The top-level quick start becomes:

```text
1. Install Virgil.
2. Run `virgil` and complete setup in the panel.
3. Run an agent with `virgil run -- <command>`.
4. Inspect protections and execution outcomes in the panel.
```

Move Control Plane material to a clearly marked historical or deferred section. Replace “dashboard” with “panel” when referring to the whole UI; retain “Overview” for the metrics page.

- [ ] **Step 3: Update setup completion copy**

Replace source-oriented text such as `go run ./cmd/virgil serve` with installed-product text: `Restart Virgil to apply this configuration.`

- [ ] **Step 4: Verify and commit**

Run: `go test ./tests/privacy ./...`

Commit: `docs: position Virgil as a unified local circuit breaker`

---

## Stage 2: Local Control and Execution Identity

### Task 3: Create the installation control credential

**Files:**
- Create: `internal/controlauth/credential.go`
- Create: `internal/controlauth/credential_test.go`
- Modify: `.gitignore`
- Modify: `internal/app/core.go`

**Interfaces:**
- Produces: `controlauth.LoadOrCreate(path string) (Credential, error)`, `Credential.Verify(string) bool`.
- Consumes: the configured storage directory.

- [ ] **Step 1: Write credential lifecycle tests**

Test first creation, stable reload, 32-byte entropy after base64 decoding, invalid and empty candidates, and file permissions. On Unix require `0600`; on Windows assert the file is not world-writable through the available mode bits.

- [ ] **Step 2: Run the failing tests**

Run: `go test ./internal/controlauth`

Expected: FAIL because the package is absent.

- [ ] **Step 3: Implement the credential type**

```go
type Credential struct {
    value string
    hash  [32]byte
}

func LoadOrCreate(path string) (Credential, error)
func (c Credential) Bearer() string
func (c Credential) Verify(candidate string) bool
```

Generate with `crypto/rand`, encode with `base64.RawURLEncoding`, write a temporary file with `0600`, sync it, and atomically rename. Verification hashes candidates with SHA-256 and uses `subtle.ConstantTimeCompare`.

- [ ] **Step 4: Wire core startup**

Create the credential beside the SQLite database as `control.token`. Never print its value. Add `data/*.token` to `.gitignore`.

- [ ] **Step 5: Verify and commit**

Run: `go test -race ./internal/controlauth ./internal/app`

Commit: `feat: add installation-local control credential`

### Task 4: Define execution identities and active registry

**Files:**
- Create: `internal/executions/types.go`
- Create: `internal/executions/registry.go`
- Create: `internal/executions/registry_test.go`

**Interfaces:**
- Produces:

```go
type Registration struct {
    RunID       string `json:"run_id"`
    RunToken    string `json:"run_token"`
    SignalToken string `json:"signal_token"`
}

func NewRegistry(store Store) *Registry
func (r *Registry) Register(ctx context.Context, requestedRunID string) (Registration, error)
func (r *Registry) ResolveRunToken(token string) (string, bool)
func (r *Registry) Subscribe(runID, signalToken string) (<-chan Signal, func(), error)
func (r *Registry) NotifyPolicyBlock(PolicyBlockNotice)
func (r *Registry) Finish(ctx context.Context, result ExecutionResult) error
```

- [ ] **Step 1: Write registry behavior tests**

Cover generated and requested IDs, duplicate active IDs, invalid IDs, token isolation between runs, single-use signal tokens, one subscriber per run, one-shot block delivery, cleanup after finish, and concurrent registration under `go test -race`.

- [ ] **Step 2: Implement in-memory identity records**

Store only `[32]byte` hashes. Use `sync.RWMutex` for the active map and per-run `sync.Once` for notifications. Use buffered channels of size one so the request path never blocks on a slow Runner.

- [ ] **Step 3: Add explicit error vocabulary**

Define sentinel errors: `ErrRunActive`, `ErrRunNotFound`, `ErrRunTokenInvalid`, `ErrSignalTokenInvalid`, and `ErrSignalAlreadyClaimed`. HTTP code mapping belongs in the later handler task.

- [ ] **Step 4: Verify and commit**

Run: `go test -race ./internal/executions`

Commit: `feat: add active execution identity registry`

---

## Stage 3: Durable Execution Lifecycle

### Task 5: Persist strict execution transitions

**Files:**
- Create: `internal/storage/migrations/008_executions.sql`
- Create: `internal/storage/executions.go`
- Create: `internal/storage/executions_test.go`
- Modify: `internal/storage/sqlite_test.go`
- Modify: `internal/executions/types.go`

**Interfaces:**
- Produces the storage methods named in the design spec and a storage-backed `executions.Store` implementation.
- Consumes: the existing `Journal` write database and read pool.

- [ ] **Step 1: Write migration and transition tests**

Test `starting -> running -> completed`, `starting -> failed`, `running -> blocked`, blocked plus successful termination, blocked plus failed termination, repeated finish rejection, rejection of other terminal overwrites, duplicate run IDs, list ordering, detail retrieval, and restart recovery of both `starting` and `running` rows.

- [ ] **Step 2: Add migration 008**

Use the exact table and index from the design, including `termination_status` and `termination_error_code`. Add `CHECK` constraints for states and nonnegative `policy_attempt`/`policy_threshold` when present. Update the migration-count assertion from 7 to 8.

- [ ] **Step 3: Implement conditional transitions**

Use guarded updates such as:

```sql
UPDATE executions
SET state='running'
WHERE run_id=? AND state='starting'
```

Require exactly one affected row. Return `executions.ErrInvalidTransition` for zero rows rather than silently accepting races. Give blocked rows one narrow follow-up transition: set terminal metadata and `termination_status` once; keep `blocked` on success and change to `termination_failed` on failure without clearing policy fields.

- [ ] **Step 4: Implement crash recovery**

At core startup, one transaction updates stale `starting` and `running` rows to `interrupted`, sets `ended_at_unix_ns`, and records `virgil_restart`.

- [ ] **Step 5: Verify and commit**

Run: `go test -race ./internal/storage ./internal/executions`

Commit: `feat: persist supervised execution lifecycle`

---

## Stage 4: Process-Tree Ownership

### Task 6: Refactor Runner into a process lifecycle

**Files:**
- Create: `internal/runner/process.go`
- Create: `internal/runner/process_unix.go`
- Create: `internal/runner/process_windows.go`
- Create: `internal/runner/process_unix_test.go`
- Create: `internal/runner/process_windows_test.go`
- Modify: `internal/runner/runner.go`
- Modify: `internal/runner/runner_test.go`
- Modify: `go.mod`

**Interfaces:**
- Produces `runner.Start(context.Context, RunSpec) (Process, error)`, `Process.Wait()`, and `Process.Terminate(context.Context)`.
- Consumes: `golang.org/x/sys/windows` on Windows.

- [ ] **Step 1: Write descendant-process tests**

Create native test commands that start a root process, spawn one long-lived descendant, write both PIDs to files, and wait. Assert both no longer exist after `Terminate`. Keep platform commands in build-tagged tests; do not use a shell in production code.

- [ ] **Step 2: Introduce the platform-neutral contract**

```go
type ProcessResult struct {
    ExitCode int
    Err      error
}

type Process interface {
    PID() int
    Wait() ProcessResult
    Terminate(context.Context) error
}
```

Ensure `Wait` is idempotent by caching its result behind `sync.Once` and a closed channel.

- [ ] **Step 3: Implement Unix process groups**

Start with `SysProcAttr.Setpgid = true`. Terminate using `syscall.Kill(-pid, SIGTERM)`, wait up to three seconds using the provided context, then `SIGKILL`. Always reap the root.

- [ ] **Step 4: Implement Windows Job Objects**

Create a job with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, start the process suspended, assign it to the job, then resume it. `Terminate` calls `TerminateJobObject`. Close handles exactly once. If assignment fails, terminate the suspended process before returning.

- [ ] **Step 5: Preserve the current convenience API temporarily**

Reimplement existing `runner.Run` using `Start`, `Wait`, and `Terminate` so current callers and tests remain valid until Task 9 rewires the CLI.

- [ ] **Step 6: Verify and commit**

Run:

```text
go test -race ./internal/runner
go vet ./internal/runner
```

Commit: `feat: own supervised process trees across platforms`

---

## Stage 5: Persistent Core Supervision Protocol

### Task 7: Add authenticated control endpoints and SSE signals

**Files:**
- Create: `internal/executions/http.go`
- Create: `internal/executions/http_test.go`
- Modify: `internal/gateway/server.go`
- Modify: `internal/app/core.go`

**Interfaces:**
- Produces the four `/api/executions` endpoints in the design.
- Consumes: `controlauth.Credential`, `executions.Registry`, and `storage.Journal`.

- [ ] **Step 1: Write HTTP contract tests**

Use `httptest.Server` to prove: missing/wrong control bearer gets `401`; registration returns three nonempty fields; signal token works once; SSE begins with `event: ready`; running transition returns `204`; finish returns a JSON summary; body size is capped at 16 KiB; unknown JSON fields are rejected.

- [ ] **Step 2: Implement exact request contracts**

```go
type RegisterRequest struct { RunID string `json:"run_id,omitempty"` }
type RunningRequest struct{}
type FinishRequest struct {
    State             State  `json:"state"`
    ExitCode          int    `json:"exit_code"`
    StopReason        string `json:"stop_reason,omitempty"`
    TerminationStatus string `json:"termination_status,omitempty"`
    TerminationCode   string `json:"termination_error_code,omitempty"`
}
```

Allow Runner-reported terminal states `completed`, `failed`, `deadline`, `interrupted`, `gateway_failed`, and `termination_failed`. Only the core can set `blocked` from a policy notice. For an already blocked execution, accept one finish report that records `termination_status`; preserve `blocked` when termination succeeded and use `termination_failed` when it failed.

- [ ] **Step 3: Implement the SSE stream**

Set `Content-Type: text/event-stream`, `Cache-Control: no-store`, and `X-Content-Type-Options: nosniff`. Flush `ready`, send a comment heartbeat every fifteen seconds, serialize `circuit_break` as one JSON data line, and return on request cancellation or terminal execution.

- [ ] **Step 4: Mount control routes beside panel and API**

Mount exact method patterns on the existing `ServeMux`. Apply control authentication only to `/api/executions`; do not alter panel session auth.

- [ ] **Step 5: Verify and commit**

Run: `go test -race ./internal/executions ./internal/gateway ./internal/app`

Commit: `feat: expose local execution control protocol`

### Task 8: Authenticate supervised gateway traffic

**Files:**
- Create: `internal/gateway/execution_auth.go`
- Create: `internal/gateway/execution_auth_test.go`
- Modify: `internal/gateway/server.go`
- Modify: `internal/gateway/chat.go`
- Modify: `internal/gateway/tool_results.go`
- Modify: `internal/gateway/chat_test.go`
- Modify: `internal/gateway/tool_results_test.go`

**Interfaces:**
- Consumes: `ResolveRunToken(string) (string, bool)` from `executions.Registry`.
- Produces: a request context containing the bound run ID and a configured-provider-key authorization decision.

- [ ] **Step 1: Write authentication boundary tests**

Prove that a valid run token uses the gateway-held provider key, a missing/invalid token returns `401`, a forged `X-Virgil-Run-ID` is replaced, invalid requests do not reserve policy budget, two run tokens cannot cross identities, and standalone `serve` pass-through behavior remains covered.

- [ ] **Step 2: Add a typed request identity**

```go
type ExecutionAuthenticator interface {
    ResolveRunToken(string) (runID string, ok bool)
}

type requestIdentity struct {
    RunID              string
    UseConfiguredKey   bool
}
```

Store it under a private context key. Never propagate the plaintext token.

- [ ] **Step 3: Bind identity before policy evaluation**

For supervised requests, resolve the bearer token, replace the header-derived run ID, and resolve only the configured provider credential. For unsupervised requests, retain current standalone behavior. Apply the same identity to `/v1/tool-results`.

- [ ] **Step 4: Stop advertising incompatible Anthropic inbound routing**

Remove `ANTHROPIC_BASE_URL` injection from the Runner until an inbound Messages route exists. Update `docs/runner.md` and its tests.

- [ ] **Step 5: Verify and commit**

Run: `go test -race ./internal/gateway ./internal/policies`

Commit: `feat: bind gateway traffic to supervised executions`

### Task 9: Implement the Runner control client and supervisor

**Files:**
- Create: `internal/runner/control_client.go`
- Create: `internal/runner/control_client_test.go`
- Create: `internal/runner/supervisor.go`
- Create: `internal/runner/supervisor_test.go`
- Modify: `cmd/virgil/run.go`
- Modify: `cmd/virgil/main_test.go`

**Interfaces:**
- Consumes: control endpoints from Task 7 and process lifecycle from Task 6.
- Produces: `runner.Supervise(context.Context, SupervisionSpec) (ExecutionSummary, error)`.

- [ ] **Step 1: Write control-client tests**

Test bearer authentication, loopback URL validation, response size limits, SSE `ready`, circuit-break decoding, malformed/multiple signals, heartbeat tolerance, connection loss, finish summary decoding, and redirect rejection.

- [ ] **Step 2: Implement the client**

```go
type ControlClient struct {
    baseURL   *url.URL
    credential string
    client    *http.Client
}

func (c *ControlClient) Register(context.Context, string) (Registration, error)
func (c *ControlClient) Signals(context.Context, Registration) (<-chan Signal, <-chan error, error)
func (c *ControlClient) MarkRunning(context.Context, string) error
func (c *ControlClient) Finish(context.Context, FinishRequest) (ExecutionSummary, error)
```

Use a client with redirects disabled. Cap JSON responses at 64 KiB and SSE lines at 16 KiB.

- [ ] **Step 3: Write supervisor precedence tests**

Cover normal exit, nonzero exit, circuit break, deadline, Ctrl+C cancellation, signal disconnect, child start failure, termination failure, and races where child exit and circuit break happen together. Assert that one terminal cause wins.

- [ ] **Step 4: Implement fail-closed supervision**

Register, open signals, wait for `ready`, build child environment, start process, mark running, then select over process result, signal, signal error, deadline, and context. On every cause except already-finished child, terminate and wait. Report finish to the core even when process termination reports an error. After a circuit-break signal, report `termination_status=succeeded` while leaving the core-owned state `blocked`; report `termination_failed` plus a normalized error code if tree termination fails.

- [ ] **Step 5: Rewire `virgil run`**

Replace the unused local `policyChan`. Add `--config` to `run`, load the configured storage directory and `control.token` from it, derive the default address from `server.listen`, accept `--address` as a loopback-only override, and reject non-loopback addresses. Inject `OPENAI_API_KEY=<run token>` without printing it.

- [ ] **Step 6: Verify and commit**

Run:

```text
go test -race ./internal/runner ./cmd/virgil
go vet ./internal/runner ./cmd/virgil
```

Commit: `feat: supervise runs through the persistent local core`

---

## Stage 6: Complete the Circuit-Break Path

### Task 10: Persist policy blocks before notifying the Runner

**Files:**
- Modify: `internal/gateway/chat.go`
- Modify: `internal/gateway/server.go`
- Modify: `internal/executions/registry.go`
- Modify: `internal/storage/executions.go`
- Test: `internal/gateway/chat_test.go`
- Test: `internal/executions/registry_test.go`

**Interfaces:**
- Consumes: `PolicyBlockNotifier` from the design and storage lifecycle methods.
- Produces: exactly one durable block event and one `blocked` execution transition before the notice is delivered.

- [ ] **Step 1: Write ordering and failure tests**

Use a notifier spy that queries SQLite at notification time. Assert the policy event and blocked execution already exist. Add a failing recorder and assert the notification still occurs with a normalized persistence-failure flag. Add concurrent blocked requests and assert one terminal transition and one signal.

- [ ] **Step 2: Extract policy-block finalization**

Create a focused helper:

```go
func (r *router) finalizePolicyBlock(
    ctx context.Context,
    attempt telemetry.Attempt,
    decision policies.Decision,
) gateway.PolicyBlockNotice
```

It builds and redacts the event, persists it, conditionally transitions the execution, and returns a notice. Keep content out of both success and error paths.

- [ ] **Step 3: Notify after response data is ready**

Write the structured policy response, flush it when supported, and invoke the nonblocking notifier. Do not use the existing deferred normal-response persistence path for policy blocks.

- [ ] **Step 4: Verify and commit**

Run: `go test -race ./internal/gateway ./internal/executions ./internal/storage`

Commit: `feat: terminate supervised runs on durable policy blocks`

---

## Stage 7: Panel-First Product Experience

### Task 11: Split the panel into a persistent product shell

**Files:**
- Create: `internal/dashboard/layout.go`
- Create: `internal/dashboard/layout_test.go`
- Create: `internal/dashboard/overview.go`
- Modify: `internal/dashboard/handler.go`
- Modify: `internal/gateway/server.go`

**Interfaces:**
- Produces: shared layout data, navigation, page title, active section, flash message, and safe template renderer.
- Consumes: existing panel auth and query functions.

- [ ] **Step 1: Write navigation and auth tests**

Assert every authenticated page contains links for Overview, Executions, Protections, Providers, Usage, Health, and Settings; unauthenticated requests redirect to login; active navigation is marked with `aria-current="page"`; all pages set CSP, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: no-referrer`.

- [ ] **Step 2: Extract the shared layout**

Move shared CSS and header HTML out of the 1,500-line handler. Use one `html/template` layout with page-specific named blocks. Remove the external Google Fonts request so the panel is complete offline.

- [ ] **Step 3: Define protection-first overview cards**

Show active executions, blocked executions in the selected period, successful circuit breaks, termination failures, total calls, and recorded cost. Latency and token charts remain under Usage rather than occupying the primary decision surface.

- [ ] **Step 4: Verify and commit**

Run: `go test ./internal/dashboard ./internal/gateway`

Commit: `refactor: create unified protection-first panel shell`

### Task 12: Add Executions list and detail pages

**Files:**
- Create: `internal/dashboard/executions.go`
- Create: `internal/dashboard/executions_test.go`
- Modify: `internal/gateway/server.go`
- Modify: `internal/storage/executions.go`

**Interfaces:**
- Consumes: `ListExecutions`, `Execution`, and existing event queries.
- Produces: `GET /dashboard/executions` and `GET /dashboard/executions/{runID}`.

- [ ] **Step 1: Seed lifecycle fixtures in tests**

Create temporary SQLite data for running, completed, blocked, deadline, and termination-failed executions plus associated events. Verify ordering, labels, durations, policy details, counts, and escaped IDs.

- [ ] **Step 2: Implement list filters**

Support validated `state`, `hours` (1–168), and `limit` (1–200). Render state badges with text in addition to color. Link every row to detail.

- [ ] **Step 3: Implement execution detail**

Render the lifecycle summary, circuit-break outcome, exact policy reason/threshold/attempt, blocked-call estimate label, termination result, and event timeline. Never use “cost saved”. Return `404` for unknown IDs and `400` for invalid IDs.

- [ ] **Step 4: Redirect legacy run pages**

`/dashboard/run/{runID}` redirects to `/dashboard/executions/{runID}` so old links remain useful.

- [ ] **Step 5: Verify and commit**

Run: `go test ./internal/dashboard ./internal/storage`

Commit: `feat: show supervised execution outcomes in the panel`

### Task 13: Move provider and protection configuration into the panel

**Files:**
- Create: `internal/settings/store.go`
- Create: `internal/settings/store_test.go`
- Create: `internal/dashboard/providers.go`
- Create: `internal/dashboard/providers_test.go`
- Create: `internal/dashboard/protections.go`
- Create: `internal/dashboard/protections_test.go`
- Modify: `internal/gateway/setup.go`
- Modify: `internal/gateway/server.go`

**Interfaces:**
- Produces: atomic validated settings updates and panel routes `/dashboard/providers` and `/dashboard/protections`.
- Consumes: `config.Load`, TOML encoder, and existing dashboard session auth.

- [ ] **Step 1: Write atomic-settings tests**

Test valid provider and guardrail updates, preservation of unrelated sections, invalid values leaving the original file byte-for-byte unchanged, restrictive file mode, temporary-file cleanup, and concurrent update serialization.

- [ ] **Step 2: Implement `settings.Store`**

```go
type Store struct {
    path string
    mu   sync.Mutex
}

func (s *Store) Update(ctx context.Context, mutate func(*config.Config) error) error
```

Load, copy, mutate, encode, write a sibling temporary file with `0600`, validate that temporary file through `config.Load`, sync, and atomically replace the configured file.

- [ ] **Step 3: Implement Providers page**

List configured providers with type, model, base URL, local/remote status, and whether a credential environment name is configured. Forms accept only environment variable names, never credential values. Display copyable base URL, model ID, and the exact environment variables needed by an OpenAI-compatible client.

- [ ] **Step 4: Implement Protections page**

Expose every current local guardrail with plain-language help: per-run calls, tokens, cost, duration, tools, allowed providers/models/tools, daily calls, daily cost, and repetition rule. POST uses CSRF tokens tied to the dashboard session. Invalid values render field errors without writing TOML.

- [ ] **Step 5: Fold setup into the shell**

When no providers exist, Overview redirects to Providers with onboarding copy. Keep `/setup` as a redirect to `/dashboard/providers`; remove its independent template after compatibility tests pass.

- [ ] **Step 6: Make restart state explicit**

After saving, show `Configuration saved. Restart Virgil to apply changes.` Do not claim live reload in this release. Add the applied config hash and pending config hash to Health so the panel can show whether a restart is required.

- [ ] **Step 7: Verify and commit**

Run: `go test -race ./internal/settings ./internal/dashboard ./internal/config`

Commit: `feat: manage providers and protections from the panel`

### Task 14: Add Health, Settings, and secondary Usage navigation

**Files:**
- Create: `internal/dashboard/health.go`
- Create: `internal/dashboard/settings.go`
- Create: `internal/dashboard/health_test.go`
- Modify: `internal/dashboard/handler.go`
- Modify: `internal/gateway/server.go`

**Interfaces:**
- Produces panel routes for `/dashboard/health`, `/dashboard/settings`, and `/dashboard/usage`.
- Consumes existing metric queries and health checks.

- [ ] **Step 1: Write page contract tests**

Health shows core uptime, SQLite readiness, configured providers, applied/pending configuration state, active runs, dead letters, and version without exposing paths or secrets. Settings shows retention, privacy capture flags, panel-password status, and read-only loopback bind.

- [ ] **Step 2: Move current analytics dashboard to Usage**

Retain calls, tokens, costs, latency, models, sessions, and event details under `/dashboard/usage`. Fix hourly grouping to include date when the selected range exceeds 24 hours. Add query tests because this package previously had none.

- [ ] **Step 3: Add accessible empty and error states**

Every page explains the next action when empty. Database errors return a generic browser message and structured local log code. Templates contain no external network resources.

- [ ] **Step 4: Verify and commit**

Run: `go test ./internal/dashboard ./internal/gateway`

Commit: `feat: complete local panel navigation and health views`

---

## Stage 8: End-to-End Proof and Release Hardening

### Task 15: Build the synthetic supervised child and E2E circuit-break test

**Files:**
- Create: `tests/fixtures/supervised-child/main.go`
- Create: `tests/e2e/circuit_breaker_test.go`
- Create: `tests/e2e/control_disconnect_test.go`
- Modify: `tests/e2e/local_gateway_test.go`

**Interfaces:**
- Consumes the installed-style core, control protocol, Runner, gateway, and storage.
- Produces executable proof of the release's main promise.

- [ ] **Step 1: Implement the fixture child**

The fixture reads the injected base URL and token, optionally spawns itself as a descendant, writes PID markers, and repeats a synthetic chat request until blocked. It never reads a real credential or external network.

- [ ] **Step 2: Write the full circuit-break test**

Start a fake provider, temporary core, and `virgil run` with `max_requests_per_run = 1`. Assert request one reaches the provider, request two is blocked, root and descendant exit, one block event exists, execution state is `blocked`, and the finish summary reports successful termination.

- [ ] **Step 3: Write fail-closed tests**

Prove the child does not start if registration fails, dies if the SSE stream closes, and dies if the core stops. Prove malformed signals cannot trigger a different run.

- [ ] **Step 4: Write privacy assertions**

Scan SQLite, logs, CLI output, and panel HTML for the run token, control credential, provider key canary, prompt canary, and response canary. All must be absent with default privacy settings.

- [ ] **Step 5: Verify and commit**

Run: `go test -race -timeout 180s ./tests/e2e ./tests/privacy`

Commit: `test: prove local circuit breaker end to end`

### Task 16: Add cross-platform CI gates

**Files:**
- Modify: `.github/workflows/ci.yml`
- Create: `scripts/smoke.ps1`
- Create: `scripts/smoke.sh`

**Interfaces:**
- Produces required Windows, Ubuntu, and macOS evidence.
- Consumes the complete test suite from Tasks 1–15.

- [ ] **Step 1: Load the `github-actions` skill and validate current action versions**

Keep permissions read-only and pin official actions by immutable commit SHA if required by the skill. Do not add publishing credentials to the test workflow.

- [ ] **Step 2: Convert CI to a three-OS matrix**

Run `go vet ./...`, `go test -race -timeout 180s ./...`, and `go build ./cmd/virgil` on `ubuntu-latest`, `windows-latest`, and `macos-latest`. Preserve Go version from `go.mod`.

- [ ] **Step 3: Add installed-binary smoke tests**

Each script starts the built core with a temporary configuration, waits for `/health`, verifies `/dashboard`, registers a synthetic execution, and shuts down without using external network.

- [ ] **Step 4: Verify workflow syntax and commit**

Run the repository's YAML validator if present, then `go test ./...` locally.

Commit: `ci: verify circuit breaker on all supported platforms`

### Task 17: Produce versioned binaries and an acceptance checklist

**Files:**
- Create: `.github/workflows/release.yml`
- Create: `docs/install.md`
- Create: `docs/release-acceptance.md`
- Modify: `README.md`
- Modify: `cmd/virgil/main.go`

**Interfaces:**
- Produces downloadable archives and checksums for Windows, Linux, and macOS.
- Consumes successful CI from Task 16.

- [ ] **Step 1: Add version output**

Support `virgil version` with build-injected `version`, `commit`, and `buildDate`. Defaults are `dev`, `unknown`, and `unknown`; output contains no machine metadata.

- [ ] **Step 2: Write release workflow tests/checks**

Define a tag-triggered workflow that builds `windows-amd64`, `linux-amd64`, `linux-arm64`, `darwin-amd64`, and `darwin-arm64`; archives the binary, README, LICENSE, and example config; creates SHA-256 checksums; and uploads artifacts. Publishing a GitHub Release remains a separate user-authorized action.

- [ ] **Step 3: Write installation instructions**

Document extraction, first `virgil` start, browser setup, first `virgil run`, backup of the SQLite database and TOML, upgrade replacement, and uninstall. Include PowerShell and POSIX commands without assuming Docker or Go.

- [ ] **Step 4: Write the release acceptance checklist**

The checklist requires: clean install; offline panel; provider setup; valid supervised request; policy block; descendant termination; restart recovery; privacy canary; invalid-token denial; dashboard history; backup restore; three-OS CI; `go vet`; race suite; and `govulncheck` with current vulnerability data.

- [ ] **Step 5: Final verification and commit**

Run:

```text
gofmt -w cmd internal tests
go test -race -timeout 180s ./...
go vet ./...
govulncheck ./...
git diff --check
```

Commit: `release: prepare local circuit breaker distribution`

---

## Stage Gates

| Gate | Required evidence | Product state |
|---|---|---|
| After Stage 1 | `virgil` opens a unified panel; docs describe one local product. | Honest developer preview. |
| After Stage 2 | Control and run credentials are isolated and race-tested. | Safe identity foundation. |
| After Stage 3 | Execution history and transitions survive restart. | Durable local audit. |
| After Stage 4 | Root and descendants terminate on all supported systems. | Reliable process ownership. |
| After Stage 5 | Runner registers through the persistent core and fails closed. | Integrated supervised execution. |
| After Stage 6 | A durable policy block terminates the corresponding tree. | Feature-complete circuit breaker. |
| After Stage 7 | Providers, protections, executions, health, and usage are usable from the panel. | Product-complete local beta. |
| After Stage 8 | Cross-platform CI, privacy proof, binaries, and acceptance checklist pass. | Release candidate. |

## Explicit Follow-Up Plans

After this plan reaches release candidate, create separate design and implementation plans for:

1. Secure LAN mode with TLS termination, client credentials, login throttling, and protected configuration.
2. MCP/tool execution control with argument policies and human approval.
3. OpenAI Responses API and inbound Anthropic Messages support.
4. Native desktop shell, tray, auto-start, and notifications if browser-first distribution proves insufficient.

These items must not delay the local circuit-breaker release.
