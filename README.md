# Virgil

**Local circuit breaker for supervised AI agents.** Virgil runs on your computer, keeps provider credentials in its local core, applies deterministic request policies, and can stop a supervised agent's complete process tree when a policy blocks a request.

The local core serves the API gateway and the primary product interface—the panel—on the same loopback address. Setup, protections, providers, executions, usage, health, and settings are managed from the panel at `/dashboard`.

## Quick start

1. Install Virgil and make the `virgil` executable available on your `PATH`.
2. Run `virgil`, open the panel at `http://127.0.0.1:8787/dashboard`, and complete setup.
3. Run an agent under supervision:

   ```sh
   virgil run -- python my_agent.py
   ```

4. Review protections and execution outcomes in the panel.

Use `virgil --help` and `virgil run --help` for available options. The core and panel bind to loopback. Keep the core running while supervised executions are active.

## What the circuit breaker does

Virgil routes supported OpenAI-compatible agent requests through its local gateway and evaluates configured limits before forwarding them to a provider. A policy block signals `virgil run`, which terminates the supervised process tree and records the outcome for the panel. Standalone gateway clients receive the policy response, but Virgil cannot terminate an application it does not supervise.

The child process receives a short-lived run credential for its gateway traffic. Configured provider credentials remain in the local core and are not passed to the child. Virgil does not record child stdout or stderr. Prompts, responses, and raw tool arguments remain outside telemetry by default; see the local configuration for any explicitly enabled capture behavior.

The initial supervised integration supports OpenAI-compatible request traffic. Provider adapters may translate supported requests to configured upstreams. Unsupported APIs and features are not implied by a provider name alone.

## Local setup and privacy

- The gateway and panel share one loopback origin; the panel is available at `/dashboard`.
- SQLite stores local execution and request metadata. Virgil does not require an account or a remote service for local operation.
- Provider credentials are configured for the local core, including through environment-variable references.
- A standalone API client can use local request policies, but process-tree termination requires `virgil run` supervision.
- Use synthetic data in examples and tests. Never put real provider credentials in documentation or fixtures.

## Development

This repository is MIT licensed. Build and run the application from source with Go using the repository's development instructions and example configuration. Source-run commands are for development; installed-product instructions above describe the normal user experience.

Focused Go checks can be run with `go test ./...`. Integration and platform-specific process tests use local synthetic fixtures and do not require real provider credentials.

## Historical and deferred context

An earlier architecture proposal described an optional hosted Control Plane for team-wide event aggregation, remote policy distribution, enrollment, and revocation. That proposal is preserved in [the 2026-09-16 architecture document](docs/design/2026-09-16-virgil-architecture.md) as historical design context. The Control Plane is not a required component of the local product or its primary setup path.

Secure LAN access, hosted team governance, remote policy distribution, MCP/tool interception, and additional inbound provider APIs are separate deferred designs. They are not current product capabilities or promises. They require their own security and compatibility plans before implementation.
