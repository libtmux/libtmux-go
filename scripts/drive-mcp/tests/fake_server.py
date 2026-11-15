"""A stand-in MCP server for the driver's tests.

It speaks newline-delimited JSON-RPC on stdio and answers the few methods the
driver sends. How it behaves is chosen by the tool a plan calls:

- list_panes returns structured content unless FAKE_UNSTRUCTURED is set
- ask puts a question to the client, and answers with what the client replied
- environment answers with the names in its own environment
- hang never answers
- wide answers with a reply far past 64 KiB
"""

import json
import os
import sys
from typing import Any

TOOLS = [
    {"name": "list_panes", "outputSchema": {"type": "object"}},
    {"name": "ask"},
    {"name": "environment"},
    {"name": "hang"},
    {"name": "wide"},
    {"name": "never_called"},
]


def send(message: dict[str, Any]) -> None:
    sys.stdout.write(json.dumps(message) + "\n")
    sys.stdout.flush()


def result_for(name: str) -> dict[str, Any]:
    if name == "environment":
        return {"content": [{"type": "text", "text": json.dumps(sorted(os.environ))}]}
    if name == "wide":
        return {"content": [{"type": "text", "text": "x" * (1 << 17)}]}
    if name == "list_panes" and not os.environ.get("FAKE_UNSTRUCTURED"):
        return {"structuredContent": {"panes": []}, "content": []}
    return {"content": [{"type": "text", "text": name}]}


def serve() -> None:
    print("server started", file=sys.stderr, flush=True)
    for line in sys.stdin:
        message = json.loads(line)
        method, identifier = message.get("method"), message.get("id")
        if identifier is None:
            if method == "notifications/initialized":
                send({"jsonrpc": "2.0", "method": "notifications/message", "params": {"seen": 1}})
        elif method == "initialize":
            send({"jsonrpc": "2.0", "id": identifier, "result": {"capabilities": {"tools": {}}}})
        elif method == "tools/list":
            send({"jsonrpc": "2.0", "id": identifier, "result": {"tools": TOOLS}})
        elif method == "tools/call":
            name = message["params"]["name"]
            if name == "hang":
                continue
            if name == "ask":
                send({"jsonrpc": "2.0", "id": 900, "method": "elicitation/create", "params": {}})
                answer = json.loads(sys.stdin.readline())
                text = json.dumps(answer["result"])
                send({"jsonrpc": "2.0", "id": identifier, "result": {"content": [{"text": text}]}})
                continue
            send({"jsonrpc": "2.0", "id": identifier, "result": result_for(name)})
        else:
            send({"jsonrpc": "2.0", "id": identifier, "error": {"message": f"no method {method}"}})
    print("server stopped", file=sys.stderr, flush=True)


if __name__ == "__main__":
    serve()
