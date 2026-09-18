"""VirgilClient — OpenAI-compatible HTTP client that adds run correlation."""

from __future__ import annotations

import json
import os
import urllib.request
import urllib.error
from typing import Any, Iterator

_SENTINEL = object()


class VirgilClient:
    """Sends OpenAI-compatible requests through a Virgil gateway.

    Parameters
    ----------
    base_url:
        URL of the Virgil gateway, e.g. ``http://127.0.0.1:8787``.
        Falls back to the ``VIRGIL_GATEWAY_URL`` environment variable.
    application_token:
        Bearer token for the gateway's Authorization header. Never logged or
        included in telemetry. Falls back to ``VIRGIL_APPLICATION_TOKEN``.
    """

    def __init__(
        self,
        base_url: str | None = None,
        application_token: str | None = None,
    ) -> None:
        self._base_url = (base_url or os.environ.get("VIRGIL_GATEWAY_URL", "")).rstrip("/")
        self._token = application_token or os.environ.get("VIRGIL_APPLICATION_TOKEN", "")

    def __repr__(self) -> str:
        # Token is intentionally absent — never leaked to logs or repr.
        return f"VirgilClient(base_url={self._base_url!r})"

    # ------------------------------------------------------------------
    # Chat completions
    # ------------------------------------------------------------------

    def chat_completions(
        self,
        *,
        model: str,
        messages: list[dict[str, Any]],
        run_id: str | None = None,
        traceparent: str | None = None,
        stream: bool = False,
        **kwargs: Any,
    ) -> dict[str, Any] | Iterator[dict[str, Any]]:
        """Send a chat completion request and return the response.

        When ``stream=True`` returns an iterator of parsed SSE chunks.
        """
        body: dict[str, Any] = {"model": model, "messages": messages, **kwargs}
        if stream:
            body["stream"] = True

        headers: dict[str, str] = {"Content-Type": "application/json"}
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        if run_id or os.environ.get("VIRGIL_RUN_ID"):
            headers["X-Run-Id"] = run_id or os.environ["VIRGIL_RUN_ID"]
        if traceparent:
            headers["traceparent"] = traceparent

        url = f"{self._base_url}/v1/chat/completions"
        data = json.dumps(body).encode()

        req = urllib.request.Request(url, data=data, headers=headers, method="POST")
        try:
            resp = urllib.request.urlopen(req)  # noqa: S310
        except urllib.error.HTTPError as e:
            raise RuntimeError(f"gateway error {e.code}: {e.read().decode()}") from e

        if stream:
            return self._iter_sse(resp)
        return json.loads(resp.read())

    @staticmethod
    def _iter_sse(resp: Any) -> Iterator[dict[str, Any]]:
        """Iterate over SSE data lines, yielding parsed JSON objects."""
        for raw in resp:
            line: str = raw.decode() if isinstance(raw, bytes) else raw
            line = line.rstrip("\n\r")
            if not line.startswith("data:"):
                continue
            payload = line[len("data:"):].strip()
            if payload == "[DONE]":
                return
            yield json.loads(payload)

    # ------------------------------------------------------------------
    # Tool results
    # ------------------------------------------------------------------

    def tool_results(
        self,
        *,
        run_id: str,
        tool_results: list[dict[str, Any]],
        traceparent: str | None = None,
    ) -> None:
        """Submit structured tool results without including payload bodies.

        Only metadata (tool_call_id, is_error) is sent; content is omitted.
        """
        # Strip content fields to avoid sending payload bodies.
        sanitised = [
            {k: v for k, v in tr.items() if k not in ("content",)}
            for tr in tool_results
        ]
        body = {"run_id": run_id, "tool_results": sanitised}
        headers: dict[str, str] = {"Content-Type": "application/json"}
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        if traceparent:
            headers["traceparent"] = traceparent

        url = f"{self._base_url}/v1/tool-results"
        data = json.dumps(body).encode()
        req = urllib.request.Request(url, data=data, headers=headers, method="POST")
        try:
            urllib.request.urlopen(req)  # noqa: S310
        except urllib.error.HTTPError as e:
            raise RuntimeError(f"gateway error {e.code}: {e.read().decode()}") from e
