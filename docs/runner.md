# Supervised execution

`virgil run` supervises an agent process tree through the local Virgil core. The core must be running first; start `virgil` and complete setup in the panel. API traffic and the panel share the core's loopback origin.

## Usage

```sh
virgil run [flags] -- <command> [args...]
```

Example:

```sh
virgil run --deadline 10m -- python my_agent.py
```

The default panel entry is `http://127.0.0.1:8787/dashboard`. An execution's details are available in the panel at `/dashboard/executions/<run_id>`.

## Options

| Flag | Description |
|------|-------------|
| `--config` | Configuration file used by the local core. |
| `--address` | Loopback address of the local core; defaults to the configured address. |
| `--run-id` | Optional validated execution ID; Virgil generates one when omitted. |
| `--deadline` | Optional hard wall-clock limit, such as `30m` or `2h`. |
| `--env` | Extra child-specific `KEY=VALUE` pairs, comma-separated. |

The core creates an execution identity before starting the child. It injects the run ID, local gateway address, and a short-lived run credential needed for authenticated gateway traffic. The core keeps configured provider credentials; Virgil does not copy them into the child's environment.

## Circuit-break behavior

When a configured policy blocks a request from a supervised run, the core signals the runner and the runner terminates the complete process tree. The execution detail in the panel records the policy outcome and whether termination succeeded. A deadline, parent interruption, or loss of the core control connection also stops the supervised tree and is reported as its own outcome.

A client using the gateway without `virgil run` can receive a policy block response, but the gateway cannot stop a process it does not supervise. The initial supervised request integration supports OpenAI-compatible traffic. `virgil run` fails closed if the core or its signal stream is unavailable.

## Privacy

The runner does not capture child stdout or stderr. Provider credentials stay in the core. Prompts, responses, and raw tool arguments remain excluded from telemetry unless the existing explicit content-capture settings are enabled. Examples and test fixtures use synthetic values only.
