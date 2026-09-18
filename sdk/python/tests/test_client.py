"""Tests for virgil.client.VirgilClient."""

import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest

from virgil.client import VirgilClient


# ---------------------------------------------------------------------------
# Helpers: minimal fake gateway server
# ---------------------------------------------------------------------------

class _Handler(BaseHTTPRequestHandler):
    """Single-request handler that executes a provided callable."""

    def log_message(self, *_):
        pass  # suppress request logging in test output

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)
        self.server.last_request_path = self.path
        self.server.last_request_headers = dict(self.headers)
        self.server.last_request_body = body
        response, content_type, status = self.server.handler_fn(self.path, self.headers, body)
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.end_headers()
        self.wfile.write(response)


def _start_server(handler_fn):
    """Start a local HTTP server with handler_fn and return (server, url)."""
    server = HTTPServer(("127.0.0.1", 0), _Handler)
    server.handler_fn = handler_fn
    server.last_request_path = None
    server.last_request_headers = {}
    server.last_request_body = b""
    thread = threading.Thread(target=server.handle_request)
    thread.daemon = True
    thread.start()
    return server, f"http://127.0.0.1:{server.server_address[1]}"


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

def test_tool_result_sends_metadata_only():
    """tool_results() must strip the content field before sending."""
    received = {}

    def handler(path, headers, body):
        received["body"] = json.loads(body)
        return b'{}', "application/json", 200

    server, url = _start_server(handler)
    client = VirgilClient(base_url=url)
    client.tool_results(
        run_id="run_test",
        tool_results=[
            {
                "tool_call_id": "call_abc",
                "role": "tool",
                "content": "secret-payload",  # must be stripped
                "is_error": False,
            }
        ],
    )
    server.server_close()
    tr = received["body"]["tool_results"][0]
    assert "content" not in tr, f"content was not stripped: {tr}"
    assert tr["tool_call_id"] == "call_abc"


def test_client_streams_and_cancels():
    """chat_completions(stream=True) returns an iterable of parsed chunks."""

    def sse_body():
        chunks = [
            {"id": "c1", "object": "chat.completion.chunk", "choices": [
                {"index": 0, "delta": {"content": "hello"}, "finish_reason": None}
            ]},
            {"id": "c1", "object": "chat.completion.chunk", "choices": [
                {"index": 0, "delta": {}, "finish_reason": "stop"}
            ]},
        ]
        lines = b""
        for ch in chunks:
            lines += b"data: " + json.dumps(ch).encode() + b"\n\n"
        lines += b"data: [DONE]\n\n"
        return lines

    def handler(path, headers, body):
        return sse_body(), "text/event-stream", 200

    server, url = _start_server(handler)
    client = VirgilClient(base_url=url)
    chunks = list(client.chat_completions(
        model="gpt-4o",
        messages=[{"role": "user", "content": "hi"}],
        stream=True,
    ))
    server.server_close()
    assert len(chunks) == 2, f"expected 2 chunks, got {len(chunks)}"
    assert chunks[0]["choices"][0]["delta"]["content"] == "hello"


def test_no_token_in_repr_or_logs():
    """The application token must never appear in repr or string form."""
    secret = "sk-super-secret-application-token"
    client = VirgilClient(base_url="http://127.0.0.1:8787", application_token=secret)
    r = repr(client)
    assert secret not in r, f"secret token found in repr: {r}"
    s = str(client)
    assert secret not in s, f"secret token found in str: {s}"


def test_chat_completions_injects_run_id_header(monkeypatch):
    """chat_completions sends X-Run-Id when run_id is provided."""
    received_headers = {}

    def handler(path, headers, body):
        received_headers.update(headers)
        resp = {
            "id": "c1", "object": "chat.completion", "model": "m",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}],
        }
        return json.dumps(resp).encode(), "application/json", 200

    server, url = _start_server(handler)
    client = VirgilClient(base_url=url)
    client.chat_completions(
        model="gpt-4o",
        messages=[{"role": "user", "content": "hi"}],
        run_id="run_inject_test",
    )
    server.server_close()
    assert received_headers.get("X-Run-Id") == "run_inject_test", \
        f"X-Run-Id header missing or wrong: {received_headers}"


def test_chat_completions_inherits_run_id_from_env(monkeypatch):
    """When VIRGIL_RUN_ID is set in the environment, it is injected automatically."""
    monkeypatch.setenv("VIRGIL_RUN_ID", "run_from_env")
    received_headers = {}

    def handler(path, headers, body):
        received_headers.update(headers)
        resp = {
            "id": "c1", "object": "chat.completion", "model": "m",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}],
        }
        return json.dumps(resp).encode(), "application/json", 200

    server, url = _start_server(handler)
    client = VirgilClient(base_url=url)
    client.chat_completions(
        model="gpt-4o",
        messages=[{"role": "user", "content": "hi"}],
    )
    server.server_close()
    assert received_headers.get("X-Run-Id") == "run_from_env", \
        f"env-based run_id not injected: {received_headers}"
