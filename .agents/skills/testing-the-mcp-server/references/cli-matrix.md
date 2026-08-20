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

- **claude** — `status: connected`, 53 tools listed, and a model-driven call of
  `list_panes` followed by `set_pane_title`; the isolated socket showed the
  title afterwards. Full proof.
- **cursor** — returned the pane ids from a real tool call. Full proof.
- **grok** — `mcp doctor`: command found, server started, handshake OK at
  protocol 2025-11-25, 53 tools discovered.
- **codex** — server registered and enabled, config parsed. The model call hit
  `401 Unauthorized`.
- **gemini** — `IneligibleTierError`: no longer an eligible client for
  individual accounts.
- **agy** — timed out with no output.
- **opencode** — the local install was missing its postinstall step.

Every failure above is a condition on the client side. None was the server.

## The traps

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
