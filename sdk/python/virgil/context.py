"""run_context — groups requests under a shared run ID and trace."""

from __future__ import annotations

import contextlib
import os
import secrets
import threading
from typing import Generator

_local = threading.local()


class RunContext:
    """Holds the correlation identifiers for an active run.

    Attributes
    ----------
    run_id:     Stable ID shared across all requests in this run.
    trace_id:   W3C traceparent trace ID (32 hex digits).
    """

    def __init__(self, run_id: str | None = None, trace_id: str | None = None) -> None:
        self.run_id = run_id or os.environ.get("VIRGIL_RUN_ID") or _new_run_id()
        self.trace_id = trace_id or _new_trace_id()

    @property
    def traceparent(self) -> str:
        """W3C traceparent header value derived from the trace ID."""
        span_id = secrets.token_hex(8)
        return f"00-{self.trace_id}-{span_id}-01"


def current_context() -> RunContext | None:
    """Return the active RunContext for this thread, or None."""
    return getattr(_local, "ctx", None)


@contextlib.contextmanager
def run_context(
    run_id: str | None = None,
    trace_id: str | None = None,
) -> Generator[RunContext, None, None]:
    """Context manager that sets a RunContext for the current thread.

    Example::

        with run_context() as ctx:
            client.chat_completions(
                model="gpt-4o",
                messages=[...],
                run_id=ctx.run_id,
                traceparent=ctx.traceparent,
            )
    """
    ctx = RunContext(run_id=run_id, trace_id=trace_id)
    previous = getattr(_local, "ctx", None)
    _local.ctx = ctx
    try:
        yield ctx
    finally:
        _local.ctx = previous


# ------------------------------------------------------------------
# Internal helpers
# ------------------------------------------------------------------

def _new_run_id() -> str:
    return "run_" + secrets.token_hex(16)


def _new_trace_id() -> str:
    return secrets.token_hex(16)
