# drive-mcp

Drives the MCP server over stdio JSON-RPC from a plan on stdin, and prints what
each step returned.

It is layer 0 of the
[`testing-the-mcp-server`](../../.agents/skills/testing-the-mcp-server/SKILL.md)
skill, which says when to use it.

It starts the binary with an environment it builds rather than inherits, holds
stdin open, matches replies by id, and declines any question the server asks.

It lists the tools a plan never called and flags a tool that declares an
`outputSchema` and answers without structured content. It does not validate JSON
Schema. `--strict` before the binary makes a flagged tool exit 1, so a script can
gate on it; without it the exit status is 0.

When the plan ends the driver closes the server's stdin and waits 10 s
(`CLOSE_TIMEOUT`) for it to exit. A server still running then is killed with
everything it started, because it runs in its own process group.

Pass the binary, then `NAME=VALUE` settings for its environment. `PATH`, `HOME`,
`TMUX`, `TMUX_PANE`, `LANG` and `LC_ALL` carry over from the caller. A step may
set `timeout` in seconds and `maxChars`, the length its reply is cut to.

Call `list_panes` on the server a binary serves:

```console
$ uv run scripts/drive-mcp/drive_mcp.py \
    "$(command -v libtmux-mcp)" \
    TMUX_TMPDIR=/tmp/libtmux-go-probe \
    <<< '[{"method":"tools/call","params":{"name":"list_panes","arguments":{}}}]'
```

Run its tests:

```console
$ uv run --project scripts pytest scripts/drive-mcp
```
