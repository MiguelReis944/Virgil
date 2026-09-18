# Virgil Supervised Runner

The runner starts and supervises a child process under a running Virgil gateway.
It enforces a wall-clock deadline, stops the child when a local policy block
fires, and never records the child's stdout or stderr content.

## Usage

```bash
virgil run [flags] -- <command> [args...]
```

The gateway must already be running before you call `virgil run`.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--gateway` | `http://127.0.0.1:8787` | Gateway URL injected as `VIRGIL_GATEWAY_URL` |
| `--run-id` | *(generated)* | Correlation ID injected as `VIRGIL_RUN_ID` |
| `--deadline` | `0` (unlimited) | Wall-clock limit (e.g. `30m`, `2h`) |
| `--env` | | Extra `KEY=VALUE` pairs for the child, comma-separated |

### Example

```bash
# Start the gateway first
virgil serve --config virgil.toml &

# Run a Python agent with a 10-minute hard deadline
virgil run --deadline 10m -- python my_agent.py
```

## Environment variables injected into the child

| Variable | Value |
|----------|-------|
| `VIRGIL_RUN_ID` | Correlation ID for all requests in this run |
| `VIRGIL_GATEWAY_URL` | Gateway endpoint the child should use |

Provider API keys are **not** injected automatically. Pass them explicitly via
`--env PROVIDER_API_KEY=<value>` or configure them as environment references in
`virgil.toml`.

## Stop reasons

| Reason | Meaning |
|--------|---------|
| *(none)* | Child exited on its own |
| `deadline` | Wall-clock limit exceeded; child was killed |
| `policy_block` | Local policy engine blocked this run; child was killed |
| `context_cancelled` | Parent process was interrupted (SIGINT / SIGTERM) |

## Privacy boundary

Virgil never captures, logs, or stores the child process's stdout or stderr.
The runner sets both to `io.Discard`. Only structured telemetry events emitted
through the gateway are recorded in the journal.
