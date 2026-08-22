#!/usr/bin/env python3
"""Drive the MCP server over stdio JSON-RPC, from a plan on stdin.

Layer 0 of SKILL.md. Standard library only, so it runs wherever the binary
does.

    ./drive.py /path/to/libtmux-mcp <<'PLAN'
    [{"method": "tools/list", "params": {}},
     {"method": "tools/call", "params": {"name": "list_panes", "arguments": {}}}]
    PLAN

Environment is replaced rather than inherited, which is what a client does:
pass only what the server needs, as NAME=VALUE arguments after the binary.
Inheriting a shell's environment hides a whole class of bug -- a missing UTF-8
locale made tmux rewrite a tab separator, and the control-connection poll never
matched.

To exercise self-detection, run this script itself inside a pane of the tmux
server under test. It keeps TMUX and TMUX_PANE unless you replace them, so the
server sees the pane it is running in.
"""

from __future__ import annotations

import json
import os
import queue
import subprocess
import sys
import threading
import time

# Long enough for a wait tool asked to wait, short enough to fail a hang.
REPLY_TIMEOUT = 60.0


def main() -> int:
    if len(sys.argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    binary, assignments = sys.argv[1], sys.argv[2:]
    plan = json.load(sys.stdin)

    environment = {"PATH": os.environ.get("PATH", ""), "HOME": os.environ.get("HOME", "")}
    for name in ("TMUX", "TMUX_PANE", "LANG", "LC_ALL"):
        if name in os.environ:
            environment[name] = os.environ[name]
    for assignment in assignments:
        name, _, value = assignment.partition("=")
        environment[name] = value

    server = subprocess.Popen(
        [binary],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=environment,
        bufsize=1,
    )
    replies: queue.Queue = queue.Queue()
    notifications: list = []

    def read() -> None:
        for line in server.stdout:  # type: ignore[union-attr]
            try:
                message = json.loads(line)
            except json.JSONDecodeError:
                notifications.append({"unparsed": line[:400]})
                continue
            if "id" in message:
                replies.put(message)
            else:
                notifications.append(message)
        replies.put(None)

    threading.Thread(target=read, daemon=True).start()

    identifier = [0]

    def send(message: dict) -> None:
        server.stdin.write(json.dumps(message) + "\n")  # type: ignore[union-attr]
        server.stdin.flush()  # type: ignore[union-attr]

    def rpc(method: str, params: dict, timeout: float = REPLY_TIMEOUT) -> dict:
        identifier[0] += 1
        wanted = identifier[0]
        send({"jsonrpc": "2.0", "id": wanted, "method": method, "params": params})
        deadline = time.time() + timeout
        while time.time() < deadline:
            message = replies.get(timeout=max(0.1, deadline - time.time()))
            if message is None:
                return {"error": {"message": "the server closed the connection"}}
            # A server may ask the client something mid-call; declining keeps
            # the driver from hanging on a question nobody is here to answer.
            if message.get("id") is not None and "method" in message:
                send({"jsonrpc": "2.0", "id": message["id"],
                      "result": {"action": "decline"}})
                continue
            if message.get("id") == wanted:
                return message
        return {"error": {"message": "no reply within %.0fs" % timeout}}

    handshake = rpc("initialize", {
        "protocolVersion": "2025-06-18",
        "capabilities": {},
        "clientInfo": {"name": "drive.py", "version": "1"},
    })
    send({"jsonrpc": "2.0", "method": "notifications/initialized"})
    print("=== initialize")
    print(json.dumps(handshake.get("result", handshake).get("capabilities", handshake), indent=2))

    listed = rpc("tools/list", {}).get("result", {}).get("tools", [])
    advertised = {tool["name"]: tool for tool in listed}
    called: set = set()

    for step in plan:
        method = step["method"]
        params = step.get("params", {})
        reply = rpc(method, params, step.get("timeout", REPLY_TIMEOUT))
        name = params.get("name") if method == "tools/call" else None
        if name:
            called.add(name)
        print("=== %s %s" % (method, json.dumps(params)[:120]))
        print(json.dumps(reply, indent=2)[: step.get("maxChars", 4000)])
        # A tool that declares an output schema and answers without structured
        # content has broken a contract it published, which is a bug no
        # assertion had to be written for.
        if name and advertised.get(name, {}).get("outputSchema"):
            result = reply.get("result", {})
            if not result.get("isError") and result.get("structuredContent") is None:
                print("!!! %s declares an outputSchema and returned none" % name)

    uncalled = sorted(set(advertised) - called)
    if uncalled:
        print("=== %d of %d tools were never called" % (len(uncalled), len(advertised)))
        print("    " + " ".join(uncalled))
    if notifications:
        print("=== %d notifications" % len(notifications))
        for message in notifications[:20]:
            print("    " + json.dumps(message)[:200])

    server.stdin.close()  # type: ignore[union-attr]
    try:
        server.wait(timeout=10)
    except subprocess.TimeoutExpired:
        server.kill()
    stderr = server.stderr.read()  # type: ignore[union-attr]
    if stderr.strip():
        print("=== stderr\n" + stderr[:3000])
    return 0


if __name__ == "__main__":
    sys.exit(main())
