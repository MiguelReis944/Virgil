# Virgil

Virgil limits and stops local AI agent executions before repeated failures or budget overruns can continue consuming resources.

## Language

**Supervised execution**:
A process tree started by Virgil with one stable execution identity and an embedded local gateway.
_Avoid_: Session, job, workflow

**Execution identity**:
The `run_id` and secret run token that bind gateway traffic to one supervised execution.
_Avoid_: User identity, installation credential

**Policy block**:
A deterministic decision that rejects an intercepted model request because an active local limit was reached.
_Avoid_: Alert, warning

**Circuit break**:
The transition caused by a policy block that rejects the request and terminates the supervised process tree.
_Avoid_: Kill switch, crash

**Process tree**:
The supervised root process and every descendant process created by it.
_Avoid_: Agent process

**Run token**:
A random, short-lived secret created for one supervised execution and accepted only by its embedded gateway.
_Avoid_: API key, provider key

**Provider credential**:
A secret used by the gateway to authenticate an outbound request to an LLM provider.
_Avoid_: Run token, application token

**Blocked call estimate**:
The configured or calculated estimated cost of the single provider call rejected by a policy block.
_Avoid_: Cost saved, avoided spend
