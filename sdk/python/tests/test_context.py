"""Tests for virgil.context."""

import threading
import re

from virgil.context import run_context, current_context, RunContext

_TRACE_RE = re.compile(r"^[0-9a-f]{32}$")
_TRACEPARENT_RE = re.compile(r"^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$")


def test_context_preserves_trace_id():
    with run_context() as ctx:
        assert _TRACE_RE.match(ctx.trace_id), f"invalid trace_id: {ctx.trace_id}"
        # traceparent embeds the same trace_id
        assert ctx.trace_id in ctx.traceparent
        assert _TRACEPARENT_RE.match(ctx.traceparent), f"invalid traceparent: {ctx.traceparent}"
        # context is accessible from the same thread
        assert current_context() is ctx


def test_run_context_stable_run_id():
    with run_context(run_id="run_explicit_test") as ctx:
        assert ctx.run_id == "run_explicit_test"


def test_run_context_generates_run_id_when_empty():
    with run_context() as ctx:
        assert ctx.run_id.startswith("run_")
        assert len(ctx.run_id) > 4


def test_context_restores_previous_after_exit():
    with run_context(run_id="outer") as outer:
        with run_context(run_id="inner"):
            assert current_context().run_id == "inner"
        assert current_context() is outer
    assert current_context() is None


def test_context_thread_isolation():
    results = {}

    def worker(name: str, run_id: str):
        with run_context(run_id=run_id):
            import time; time.sleep(0.02)
            results[name] = current_context().run_id

    threads = [threading.Thread(target=worker, args=(f"t{i}", f"run_{i}")) for i in range(3)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    for i in range(3):
        assert results[f"t{i}"] == f"run_{i}", f"thread isolation broken: {results}"


def test_traceparent_format():
    ctx = RunContext(run_id="run_tp", trace_id="0102030405060708090a0b0c0d0e0f10")
    tp = ctx.traceparent
    parts = tp.split("-")
    assert parts[0] == "00"
    assert parts[1] == "0102030405060708090a0b0c0d0e0f10"
    assert len(parts[2]) == 16
    assert parts[3] == "01"
