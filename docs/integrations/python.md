# Python SDK Integration

The Virgil Python SDK (`sdk/python/`) provides `VirgilClient` and `run_context`
to send OpenAI-compatible requests through the gateway with full run correlation
— without copying prompts or responses into telemetry.

## Installation

```bash
pip install -e sdk/python
```

Python ≥ 3.10 required. No third-party dependencies.

## Quick start

```python
from virgil import VirgilClient, run_context

client = VirgilClient(base_url="http://127.0.0.1:8787")

with run_context() as ctx:
    response = client.chat_completions(
        model="gpt-4o",
        messages=[{"role": "user", "content": "Hello"}],
        run_id=ctx.run_id,
        traceparent=ctx.traceparent,
        max_tokens=256,
    )
    print(response["choices"][0]["message"]["content"])
```

## VirgilClient

```python
VirgilClient(base_url=None, application_token=None)
```

| Parameter | Environment variable | Description |
|-----------|----------------------|-------------|
| `base_url` | `VIRGIL_GATEWAY_URL` | Gateway URL |
| `application_token` | `VIRGIL_APPLICATION_TOKEN` | Bearer token for the gateway |

The token is **never** included in `repr()`, logged, or sent as event metadata.

### Methods

#### `chat_completions(*, model, messages, run_id=None, traceparent=None, stream=False, **kwargs)`

Sends a chat completion request. Forwards `run_id` as `X-Run-Id` and
`traceparent` as the W3C trace header. When `stream=True`, returns an iterator
of parsed SSE chunks.

#### `tool_results(*, run_id, tool_results, traceparent=None)`

Submits structured tool results. The `content` field is stripped before
sending so no payload body reaches the gateway or the journal.

## run_context

```python
with run_context(run_id=None, trace_id=None) as ctx:
    ctx.run_id       # stable ID for all requests in this run
    ctx.trace_id     # 32-hex W3C trace ID
    ctx.traceparent  # W3C traceparent header value
```

- Falls back to `VIRGIL_RUN_ID` when `run_id` is not provided.
- Thread-local: nested contexts restore the outer context on exit.
- `run_id` is auto-injected into `chat_completions` when `VIRGIL_RUN_ID` is set
  in the environment (e.g. by `virgil run`).

## Framework integration pattern

Wrap any agent loop that calls `client.chat_completions` inside a `run_context`
block. The gateway groups all requests under the same `run_id`, enforces
configured limits, and records a single telemetry event per call — with no
prompt or response content.

```python
with run_context() as ctx:
    while True:
        resp = client.chat_completions(
            model=model, messages=history,
            run_id=ctx.run_id, traceparent=ctx.traceparent,
        )
        msg = resp["choices"][0]["message"]
        if msg.get("finish_reason") == "stop":
            break
        # process tool calls, update history, submit tool results ...
        client.tool_results(run_id=ctx.run_id, tool_results=[...])
```

## Running under virgil run

When the gateway is started with `virgil run`, it injects `VIRGIL_RUN_ID` and
`VIRGIL_GATEWAY_URL` into the child process environment. `VirgilClient` picks
these up automatically:

```bash
virgil run --deadline 30m -- python my_agent.py
```

The child does not need to pass `base_url` or `run_id` explicitly; they are
read from the environment.
