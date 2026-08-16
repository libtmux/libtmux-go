# Security

## Reporting a vulnerability

Report privately through GitHub's [private vulnerability
reporting](https://github.com/libtmux/libtmux-go/security/advisories/new) rather
than in a public issue. Include what you ran, what happened, and the tmux and Go
versions involved.

This is an alpha library maintained by one person; expect an acknowledgement
rather than a service-level agreement.

## What this software does

Knowing the shape of it makes a report easier to judge.

**The tmux module runs tmux.** It builds an argument vector and executes the
tmux binary, or writes commands to a control-mode connection. It never passes a
command through a shell, so an argument containing shell metacharacters reaches
tmux as one argument. What tmux then does with it is tmux's semantics: a value
reaching `Server.RunShell`, `Server.IfShell`, a hook, or a pane's command **is**
executed by a shell, because that is what those tmux commands mean. Treat any
value you did not author as untrusted before putting it in one.

**Panes run programs.** `SendKeys`, `SplitPane`, `NewWindow`, and `NewSession`
all end in a shell running something. A program that types attacker-controlled
text into a pane is running attacker-controlled commands.

**The MCP server exposes a tmux server to a client.** `mcp/` lets an agent read
panes, send keys, and run commands on whichever tmux server it was pointed at.
Its `LIBTMUX_SAFETY` setting hides tools above a chosen tier, which narrows what
a client can reach but is not a sandbox. Point it at a tmux server you are
willing to let the client drive.

**Captured pane content is whatever was on screen.** A pane may hold secrets a
person typed or a program printed. `capture_pane`, `capture_since`, and the MCP
tools built on them return it verbatim.

## Out of scope

- tmux's own behaviour once it has a command. Report those to
  [tmux](https://github.com/tmux/tmux/issues).
- A program you asked this library to run in a pane doing what you asked.
- Reading a pane you gave the library access to.

## Supported versions

Alpha releases: only the most recent tag is supported. Go 1.23 or newer, tmux
3.2a through 3.7b.
