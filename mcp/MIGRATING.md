# Moving from an earlier alpha

The capability switch changes names and the default socket together. Use this
mapping when updating an existing client entry:

| Earlier setting | Current setting |
| --- | --- |
| `LIBTMUX_SAFETY=readonly` | `LIBTMUX_TOOLSETS=inspect` |
| `LIBTMUX_SAFETY=mutating` | `LIBTMUX_TOOLSETS=inspect,manage,execute` |
| `LIBTMUX_SAFETY=destructive` | all four toolsets on a process-created minimal daemon |
| `LIBTMUX_MCP_CAPABILITIES=` | Start with `LIBTMUX_TOOLSETS=inspect`, then exclude content and configuration readers to retain the earlier metadata-only default |
| `LIBTMUX_MCP_CAPABILITIES=inspect` | `LIBTMUX_TOOLSETS=inspect` |
| `LIBTMUX_MCP_CAPABILITIES=operate` | `LIBTMUX_TOOLSETS=inspect,manage,execute` |
| `LIBTMUX_MCP_CAPABILITIES=all` | all four toolsets |
| `LIBTMUX_MCP_CAPABILITIES=metadata-read` or `LIBTMUX_MCP_CAPABILITIES=content-read` | `inspect`; use exact exclusions when the earlier metadata/content split matters |
| `LIBTMUX_MCP_CAPABILITIES=pane-control` | `execute`; arbitrary tmux-format expansion has no replacement |
| `LIBTMUX_MCP_CAPABILITIES=workspace-create` | `execute`; current creation tools accept no program, so start bounded work with `run_shell_command` |
| `LIBTMUX_MCP_CAPABILITIES=tmux-layout` | `manage`; use exact inclusions for the earlier narrower group |
| `LIBTMUX_MCP_CAPABILITIES=tmux-settings` | Use exact constrained setters from `manage` and `execute`; generic buffer, environment, and option mutation is removed |
| `LIBTMUX_MCP_CAPABILITIES=tmux-destroy` | `teardown`; server-wide teardown has no replacement |
| `LIBTMUX_MCP_TOOLS=a,b` | `LIBTMUX_TOOLS=a,b`, with exclusions available separately |
| `LIBTMUX_MCP_PROMPTS_AS_TOOLS=1` | removed; use the [typed workflow mapping](TOOLS.md#retired-prompt-workflow-mapping) |
| implicit ordinary tmux socket | explicit `LIBTMUX_SOCKET=default` or another chosen name |

An unchanged launch now opens the dedicated `libtmux-mcp` world, so it may look
empty until the agent creates a session. The startup line prints the exact
attach command. Retired tool names and their workflow replacements are listed
in the [generated tool reference](TOOLS.md#retired-mcp-surface).
