#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Drive the MCP server over stdio JSON-RPC, from a plan on stdin.

Layer 0 of the testing-the-mcp-server skill. Standard library only, so it runs
wherever the binary does.

    uv run scripts/drive-mcp/drive_mcp.py /path/to/libtmux-mcp <<'PLAN'
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

import asyncio
import json
import os
import sys
from collections.abc import Mapping, Sequence
from typing import Any, TextIO

type Message = dict[str, Any]

# Long enough for a wait tool asked to wait, short enough to fail a hang.
REPLY_TIMEOUT = 60.0

# A tools/list reply carries every tool's schema, far past asyncio's default
# line limit of 64 KiB.
LINE_LIMIT = 1 << 24

# What a client passes its servers: enough for tmux to run, and the markers a
# server uses to find the pane it was started in.
KEPT = ("TMUX", "TMUX_PANE", "LANG", "LC_ALL")


def build_environment(environ: Mapping[str, str], assignments: Sequence[str]) -> dict[str, str]:
    """Build the server's environment from nothing, as a client does.

    >>> build_environment({"PATH": "/bin", "HOME": "/h", "TMUX": "x", "SECRET": "s"}, ["A=b=c"])
    {'PATH': '/bin', 'HOME': '/h', 'TMUX': 'x', 'A': 'b=c'}
    """
    environment = {"PATH": environ.get("PATH", ""), "HOME": environ.get("HOME", "")}
    environment.update({name: environ[name] for name in KEPT if name in environ})
    for assignment in assignments:
        name, _, value = assignment.partition("=")
        environment[name] = value
    return environment


class Connection:
    """One server process, spoken to over JSON-RPC lines."""

    def __init__(self, process: asyncio.subprocess.Process) -> None:
        assert process.stdin is not None
        assert process.stdout is not None
        assert process.stderr is not None
        self.process = process
        self.notifications: list[Message] = []
        self.stderr = b""
        self._identifier = 0
        self._closed = False
        self._inbox: asyncio.Queue[Message | None] = asyncio.Queue()
        self._readers = [
            asyncio.create_task(self._read_stdout(process.stdout)),
            asyncio.create_task(self._read_stderr(process.stderr)),
        ]

    async def _read_stdout(self, stream: asyncio.StreamReader) -> None:
        while line := await stream.readline():
            try:
                message = json.loads(line)
            except json.JSONDecodeError:
                self.notifications.append({"unparsed": line.decode(errors="replace")[:400]})
                continue
            if "id" in message:
                await self._inbox.put(message)
            else:
                self.notifications.append(message)
        await self._inbox.put(None)

    async def _read_stderr(self, stream: asyncio.StreamReader) -> None:
        self.stderr = await stream.read()

    async def send(self, message: Message) -> None:
        assert self.process.stdin is not None
        self.process.stdin.write(json.dumps(message).encode() + b"\n")
        try:
            await self.process.stdin.drain()
        except ConnectionError:
            # The server is gone; the reply wait reports it.
            self._closed = True

    async def _await_reply(self, wanted: int) -> Message:
        while not self._closed:
            message = await self._inbox.get()
            if message is None:
                self._closed = True
                break
            # A server may ask the client something mid-call; declining keeps
            # the driver from hanging on a question nobody is here to answer.
            if message.get("id") is not None and "method" in message:
                await self.send(
                    {"jsonrpc": "2.0", "id": message["id"], "result": {"action": "decline"}}
                )
                continue
            if message.get("id") == wanted:
                return message
        return {"error": {"message": "the server closed the connection"}}

    async def rpc(self, method: str, params: Message, timeout: float = REPLY_TIMEOUT) -> Message:
        """Call a method and return the reply with the matching id."""
        self._identifier += 1
        wanted = self._identifier
        await self.send({"jsonrpc": "2.0", "id": wanted, "method": method, "params": params})
        try:
            async with asyncio.timeout(timeout):
                return await self._await_reply(wanted)
        except TimeoutError:
            return {"error": {"message": f"no reply within {timeout:.0f}s"}}

    async def close(self) -> None:
        """Close stdin, give the server ten seconds to exit, and collect stderr."""
        assert self.process.stdin is not None
        self.process.stdin.close()
        try:
            await asyncio.wait_for(self.process.wait(), timeout=10)
        except TimeoutError:
            self.process.kill()
            await self.process.wait()
        await asyncio.gather(*self._readers)


async def drive(
    binary: str,
    assignments: Sequence[str],
    plan: Sequence[Message],
    environ: Mapping[str, str],
) -> None:
    """Run a plan against the server and print what each step returned."""
    process = await asyncio.create_subprocess_exec(
        binary,
        stdin=asyncio.subprocess.PIPE,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
        env=build_environment(environ, assignments),
        limit=LINE_LIMIT,
    )
    server = Connection(process)

    handshake = await server.rpc(
        "initialize",
        {
            "protocolVersion": "2025-06-18",
            "capabilities": {},
            "clientInfo": {"name": "drive-mcp", "version": "1"},
        },
    )
    await server.send({"jsonrpc": "2.0", "method": "notifications/initialized"})
    print("=== initialize")
    print(json.dumps(handshake.get("result", handshake).get("capabilities", handshake), indent=2))

    listed = (await server.rpc("tools/list", {})).get("result", {}).get("tools", [])
    advertised = {tool["name"]: tool for tool in listed}
    called: set[str] = set()

    for step in plan:
        method = step["method"]
        params = step.get("params", {})
        reply = await server.rpc(method, params, step.get("timeout", REPLY_TIMEOUT))
        name = params.get("name") if method == "tools/call" else None
        if name:
            called.add(name)
        print(f"=== {method} {json.dumps(params)[:120]}")
        print(json.dumps(reply, indent=2)[: step.get("maxChars", 4000)])
        # A tool that declares an output schema and answers without structured
        # content has broken a contract it published, which is a bug no
        # assertion had to be written for.
        if name and advertised.get(name, {}).get("outputSchema"):
            result = reply.get("result", {})
            if not result.get("isError") and result.get("structuredContent") is None:
                print(f"!!! {name} declares an outputSchema and returned none")

    if uncalled := sorted(set(advertised) - called):
        print(f"=== {len(uncalled)} of {len(advertised)} tools were never called")
        print("    " + " ".join(uncalled))
    if server.notifications:
        print(f"=== {len(server.notifications)} notifications")
        for message in server.notifications[:20]:
            print("    " + json.dumps(message)[:200])

    await server.close()
    if (stderr := server.stderr.decode(errors="replace")).strip():
        print("=== stderr\n" + stderr[:3000])


def main(argv: Sequence[str], environ: Mapping[str, str], stdin: TextIO = sys.stdin) -> int:
    if not argv:
        print(__doc__, file=sys.stderr)
        return 2
    plan = json.load(stdin)
    asyncio.run(drive(argv[0], argv[1:], plan, environ))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:], os.environ))
