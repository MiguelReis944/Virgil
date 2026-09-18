"""Basic example: one agent loop with Virgil run correlation.

Run:
    # Start the gateway first
    virgil serve --config virgil.toml &

    # Then run this script
    python examples/python/basic.py
"""

import os
from virgil import VirgilClient, run_context

GATEWAY_URL = os.environ.get("VIRGIL_GATEWAY_URL", "http://127.0.0.1:8787")
MODEL = os.environ.get("VIRGIL_MODEL", "gpt-4o")


def run_agent(user_message: str) -> str:
    """Run a simple single-turn agent loop through the Virgil gateway."""
    client = VirgilClient(base_url=GATEWAY_URL)

    with run_context() as ctx:
        print(f"[virgil] run_id={ctx.run_id}")

        response = client.chat_completions(
            model=MODEL,
            messages=[{"role": "user", "content": user_message}],
            run_id=ctx.run_id,
            traceparent=ctx.traceparent,
            max_tokens=512,
        )

        choice = response["choices"][0]
        return choice["message"]["content"]


if __name__ == "__main__":
    answer = run_agent("What is the capital of Portugal?")
    print(f"Answer: {answer}")
