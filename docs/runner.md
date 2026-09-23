# Runner: current behavior and planned circuit breaker

## Current behavior

The current `virgil run` command starts one child process while a Virgil gateway is already running. It adds a correlation ID and gateway URL to the child environment, enforces an optional wall-clock deadline, and stops the root process when the command is interrupted. Child stdout and stderr are passed through to the parent terminal; Virgil does not store them.

The command currently supports these flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--gateway` | `http://127.0.0.1:8787` | Gateway URL injected into the child. |
| `--run-id` | *(generated)* | Correlation ID injected as `VIRGIL_RUN_ID`. |
| `--deadline` | `0` (unlimited) | Wall-clock limit, such as `30m` or `2h`. |
| `--env` | | Extra `KEY=VALUE` pairs for the child, comma-separated. |

Example:

```sh
# Terminal 1: start the local core and panel.
virgil

# Terminal 2: start an agent with a deadline.
virgil run --deadline 10m -- python my_agent.py
```

The current runner is not connected to policy-block notifications from the gateway. Although the internal runner API has a policy-stop hook, the CLI does not connect it. On interruption or deadline, it kills the root child process; it does not yet guarantee termination of descendants.

The child inherits the parent's environment after variables whose names include `API_KEY=`, `API_TOKEN=`, or `SECRET=` and `VIRGIL_LOCAL_APP_TOKEN` are removed. Use `--env` for child-specific values. Provider credentials configured in the core are not injected into the child.

The existing product panel is at `/dashboard`. The planned per-execution page at `/dashboard/executions/<run_id>` is not implemented yet.

## Approved circuit-breaker target (in progress)

The approved design will register runs with the local core, use an authenticated control stream to deliver policy-block signals, and terminate the complete process tree when a supervised request is blocked. It will record the final execution outcome for panel review. Run tokens, the SSE control connection, full-tree termination, and the execution detail page are target behavior and are not part of the current implementation.

## Privacy boundary

The current runner does not persist child stdout or stderr. Gateway telemetry is subject to the current privacy and redaction behavior documented in the [README](../README.md). The planned control protocol will keep run credentials out of logs, URLs, and panel HTML; this remains an implementation requirement for the in-progress work.
