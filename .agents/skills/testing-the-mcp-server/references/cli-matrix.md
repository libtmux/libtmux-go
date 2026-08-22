# CLI matrix

Per-CLI isolation levers, cheapest proofs, and what each failure actually
means. Verified 2026-08-20 by driving a local build of this server against a
throwaway `mcp-target` socket. Flags drift — re-check with `<cli> --help`
before trusting an invocation here.

No CLI needs `mcp-swap` to be tested. Every one has a config lever that
isolates a run, and using it leaves the person's own configuration alone.

## Quick matrix

| CLI | Headless one-shot | Config isolation | Cheapest proof | Approval bypass |
| --- | --- | --- | --- | --- |
| claude | `claude -p` | `--mcp-config <f> --strict-mcp-config` | `--output-format stream-json` init event | `--permission-mode bypassPermissions` |
| cursor | `cursor-agent --print` | project `.cursor/mcp.json` | a headless run | `--force --approve-mcps` |
| grok | `grok -p` | `GROK_HOME` | `grok mcp doctor <name> --json` — a real handshake | `--permission-mode bypassPermissions` |
| codex | `codex exec` | `CODEX_HOME` | `codex mcp get <name>` — parses config, no spawn | `--dangerously-bypass-approvals-and-sandbox` |
| gemini | `gemini -p` | project `.gemini/settings.json` | `gemini mcp list` | `--approval-mode yolo --skip-trust` |
| agy | `agy -p` | `--gemini_dir <path>` | none short of a model call | `--dangerously-skip-permissions` |
| opencode | `opencode run` | `opencode.json` in cwd | not established | not established |
| pi | none — no MCP client | n/a | n/a | n/a |

## What was reached

Driving a build of this server registered as `tmuxlab`, against an isolated
socket holding two panes:

Six of the seven call a tool for real. The seventh connects.

- **claude** — `status: connected`, 53 tools, a model-driven `list_panes` then
  `set_pane_title`; the socket showed the title afterwards.
- **cursor** — returned the pane ids from a real tool call.
- **codex** — `mcp: tmux/list_panes (completed)`, then the ids.
- **grok** — the ids, and `mcp doctor` reports handshake OK at protocol
  2025-11-25 with 53 tools.
- **antigravity** — returned the ids.
- **opencode** — called `tmux_list_panes` and returned the ids.
- **gemini** — `mcp list` reports the server **Connected**; the model call is
  refused by `IneligibleTierError`, a vendor block on individual accounts that
  points at Antigravity, which does work. This is as far as the client goes.

Nothing here failed on the server.

**Two of these looked like server faults and were not**, which is the reason
this file exists. codex returned `401 Unauthorized` and agy timed out to
nothing — both while running under a throwaway config home, which relocates
configuration and leaves the credential behind. Run against the real config,
written by `mcp-swap`, both call tools on the first try. opencode's failure was
a third of the same kind: an install missing its `postinstall.mjs`, fixed by
running it. Isolate config when you can, but when a client dies, reach for its
own status command before writing anything down about the server.

## The traps

**Whether a CLI passes your environment to the server it spawns is per CLI,
and it decides which tmux the server reaches.** With `TMUX_TMPDIR` exported and
the same prompt, claude and grok reached the probe's tmux; codex and
cursor-agent reached the default socket and reported the machine as empty. A
test that sets `TMUX_TMPDIR` and asserts on panes therefore passes on two of
them and quietly measures nothing on the other two. Put the socket in the
config's `env` when it must be certain, and read the reply's `serverNote` or
`get_server_info`'s `socketPath` to find out which one answered.

**A config-home flag relocates config, not credentials.** `CODEX_HOME` and
`--gemini_dir` move the configuration and leave the credential behind, so a run
under one fails to authenticate while the same CLI works normally. That reads
exactly like a broken server. Check whether the credential is even a file
before concluding anything.

**Copy credentials into a throwaway home, never symlink them.** A CLI
refreshing an OAuth token writes back through the symlink and overwrites the
real one.

**Name the throwaway server distinctively.** Most CLIs already carry a `tmux`
entry pointing at something else. An identical name merges, shadows, or gets
resolved instead of yours. `tmuxlab` makes any leakage obvious.

**Config leaks between CLIs.** grok merges Claude Code's `~/.claude.json` and
any `.mcp.json` in the working directory into its own set. Assume ambient
servers are present unless the config home is fully overridden.

**Read past the first few stream events.** `claude --output-format stream-json`
emits hook events before the `init` event that carries `mcp_servers` and the
tool list. Reading only the first lines reports zero tools on a server that
connected fine.

**gemini needs trust as well as approval.** `--approval-mode yolo` is
downgraded to `default` in an untrusted directory; it needs `--skip-trust` or
`GEMINI_CLI_TRUST_WORKSPACE=true` too.
