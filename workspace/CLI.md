# tmux-workspace

`tmux-workspace` manages tmuxp YAML and JSON workspaces through native Go
services. It requires Go 1.26 to build and tmux 3.2a or later to load or capture
sessions. Discovery, conversion, import, help and documentation generation do
not require tmux.

The CLI is available from this checkout; it has no published CLI release yet.
Install from the repository root:

```console
$ go install ./workspace/cmd/tmux-workspace
```

Each released workspace version also builds with `GOWORK=off` against the core
release it requires. A branch that adds to the core builds only with the
workspace on until that core release is published, which is the order
[CONTRIBUTING](../.github/CONTRIBUTING.md) sets out. Cobra and presentation
dependencies remain outside the core tmux module.

## An example workspace

A workspace names a session and lays out its windows and panes; the full field
reference is in [README](README.md#supported-fields).

```yaml
session_name: project
windows:
  - window_name: editor
    layout: main-vertical
    panes:
      - vim .
      - shell_command: [git status]
  - window_name: logs
    panes:
      - start_directory: /var/log
        shell_command: tail -f syslog
```

```console
$ tmux-workspace load ./project.yaml
```

## Commands

The command tree includes `load`, `ls`, `search`, `edit`, `freeze`, `convert`,
`import teamocil`, `import tmuxinator`, `shell`, and `debug-info`. Every command
accepts inherited `--json`, `--ndjson`, and `--color` options before or after
its name. `--ndjson` takes precedence when both output flags are enabled.
Use each command's `--help` for its complete options.

Load into a named socket without attaching:

```console
$ tmux-workspace load \
    -L project \
    -d \
    --json \
    ./project.yaml
```

List discovered documents with complete configuration data:

```console
$ tmux-workspace ls \
    --full \
    --json
```

Human `ls --tree` groups adjacent workspaces by directory in discovery order.
`--full` includes each configuration as YAML, indented under its workspace in
tree mode. Human names and paths escape terminal control characters; JSON and
NDJSON preserve the original values.

Capture a session as a JSON document without writing a file:

```console
$ tmux-workspace freeze \
    -L project \
    --json \
    project
```

Convert a document while preserving keys that native execution does not use:

```console
$ tmux-workspace convert \
    --json \
    ./project.yaml
```

Machine conversion, import and freeze return a document when `--save-to` is
absent. `--workspace-format` controls saved YAML or JSON; the output flag
controls the CLI stream. Human freeze saves a file, named after the session in
the workspace directory. tmux allows a session name that cannot stand alone as
a file name — one holding a path separator, `..` or a control byte — and freeze
refuses such a name instead of resolving it; pass `--save-to`. `--quiet`
suppresses status text; it does not answer prompts. `--yes` does not authorize
replacement. Existing destinations require `--force`. Writes use a
same-directory temporary file and atomic publication; the published file keeps
the mode of a file it replaces and otherwise follows the process creation mask.

## Import source workspaces

Tmuxinator and Teamocil imports validate native workspace shape and layout syntax
before returning or saving a document. Unknown fields, invalid types and
unsupported source behaviour fail before output or overwrite. Only one spelling
of an aliased field may be present, including null values. Generic `convert`
continues to preserve arbitrary document fields.

Tmuxinator imports preserve ordered windows and panes, directories, layouts and
sequential commands. A window command array stays in one pane. Project
`pre_window` arrays retain `; ` grouping; per-window `pre` arrays retain `&&`
grouping and require explicit panes. `synchronize: true` or `before` enables
synchronization before sequential pane creation and command delivery; `after`
enables it after all commands have been sent. Project lifecycle hooks, endpoint
and runtime settings, and named pane maps require the source tool and are
refused. So is ERB markup: Tmuxinator expands it through Ruby before parsing,
and no native reader does, so an unexpanded template fails before output or
overwrite.

Teamocil imports preserve window options, directories, layouts, pane commands
and focus. `commands` arrays retain their `; ` grouping; legacy `splits` and
`cmd` are accepted. Window options apply before pane creation and command
delivery. Filters, `clear` and pane widths are refused. Teamocil evaluates no
templates, so `<%` in a Teamocil source is ordinary text and is preserved. Both
formats select the first window and pane by default; Teamocil's first explicit
focus takes precedence.

The imported root is absolute and anchored to the import invocation's directory,
including when the source omits it. Relative window directories resolve relative to
that root, so saving elsewhere keeps the working directory. Import does not
require tmux, Python or existing directories. Load checks layout compatibility
against the selected daemon.

## Inspect a workspace through MCP

Loaded workspaces are ordinary tmux sessions. The separate [MCP server](../mcp/)
can inspect them when both commands select the same socket. Install its command
from the repository root:

```console
$ go install ./mcp/cmd/libtmux-mcp
```

After the detached load above, configure your MCP client to launch
`libtmux-mcp` with arguments `-socket-name project` and environment variable
`LIBTMUX_TOOLSETS=inspect`. The socket is selected once when the server starts.
Use `-socket-path` when the workspace command uses `-S` instead of `-L`.

Discover the advertised tools with `tools/list`, then call `list_sessions` and
`list_panes`. Call `list_windows` with its `session` argument set to the
configuration's `session_name`. Use the returned pane IDs with `capture_pane`
or `wait_for_text`; keep `max_lines` and wait timeouts bounded. A pending text
wait allows inspection requests on the same connection. `tmux://capabilities`
reports the selected socket and
available tools. See the [MCP recipes](../mcp/TOOLS.md#recipes) for live output
and incremental capture.

## Execution behavior

Load creates a session, reuses an existing session with the same name, or
appends to the current session with `--append`. With multiple inputs, `-s`
overrides only the final workspace's session name. Append requires `TMUX` and
`TMUX_PANE` identifying the same live daemon as the selected socket. Socket
paths can contain commas. The CLI authenticates the inherited daemon PID and
retains one session for every input, even if a script moves the inherited pane.
Linked panes use tmux's canonical session. Native commands retain the core's
daemon replacement checks, including global options and raw queries.

A foreground load requires terminal stdin and acquires the controlling terminal
before building, or authenticates the current pane and selects one terminal
client viewing it. Inside tmux, human mode offers switch, detached-load and
append choices; `--yes` selects switching. Multiple matching clients and
independent `active-pane` clients on the invoking window require `-d` or
`--append`. The selected client's identity and attachment are checked again
before switching; a changed client is refused. Diagnostics retain completed
workspace results when handoff fails.
Progress stops and output is flushed before terminal handoff. Existing
sessions ask before attachment unless `--yes` is set. Machine load requires `-d` or `--append` and
does not read implicit prompts.

Load rejects `-8` and `--88-colors` before reading workspace files or invoking
tmux. Supported tmux releases have no 88-color mode; use `-2` for 256 colors.

The native normalizer supports command and pane shorthand, inherited commands,
command delays and enter-state overrides, history suppression, directories,
launch-time environment, shell overrides, layouts, explicit window indexes,
focus, options, before-script execution and pane readiness. A pane environment
mapping replaces the window mapping; the session environment still applies.
Native loads reject unknown workspace, window, pane, command and readiness
keys before opening logs, invoking a backend or running scripts, unless the
key starts with `x-`: an extension key is accepted and ignored at every
level, so an anchor holder such as `x-pane-defaults: &shell` loads instead of
refusing the whole document. Workspace, window and pane mappings accept
`description` as ignored metadata. Option and
environment names remain open dictionaries. Command mappings accept `cmd`,
`enter`, `sleep_before` and `sleep_after`; the readiness catalog accepts only
`pane_readiness`. Generic conversion preserves source fields. Imported `config`
and `socket_name` fields are not native load settings; select the endpoint and
tmux configuration with CLI flags. A window naming no `layout` tiles as panes
are added; the reference stacks halving splits instead. This is a deliberate
difference, not an omission. With no pane naming `focus`, the last pane
created stays active, matching the reference. Python-delegated documents
retain extension fields, with the existing checks on recognized common
values.
Layout names, checksums, unsigned 32-bit fields, tree structure and required
pane capacity are checked for every native input before scripts or tmux
mutations. Names accept abbreviations that are unique on the selected daemon;
only an unbound cold endpoint uses the configured client version. Permission
and live-daemon query failures remain errors. Trees may nest up to 256 parents;
geometry remains tmux's responsibility.
Default history suppression is enabled. `workspace_builder_options` accepts
`pane_readiness: auto|always|never` and the reference boolean aliases. Auto waits
for zsh prompts; explicit pane or window launch commands skip readiness waits.
The wait polls for up to two seconds, same as the reference. A missed deadline
is a `pane_readiness_timeout` warning event; commands still send regardless of
whether the pane answered ready. It surfaces at `--log-level info` or `debug`,
not the default `warning`, matching the reference's own debug-only logging --
concurrent pane creation occasionally outrunning the wait is expected, not a
misconfiguration.

An explicit relative `start_directory` resolves through the session, window
and pane hierarchy from the configuration directory. This avoids the
reference normalizer's missing-parent and mixed-relative-path cases. Absent
at every level, a session, window or pane's directory stays unset instead of
defaulting to the configuration directory, so tmux falls back to the
invocation directory -- matching the reference and the before-script cwd
below, not the document's own location. Before-script argv uses shell
quoting without an implicit shell. Native load validates it for every input
before creating or modifying sessions. Its cwd is the explicit session directory,
or the invocation cwd when `start_directory` is absent. A failed script removes
an owned newly created session and preserves a borrowed append session.

Later failures retain completed tmux effects. JSON summaries identify inputs,
session IDs and failures. NDJSON emits records as work happens with increasing
sequence numbers and one terminal `completed` or `failed` event when output
remains writable. SIGINT and SIGTERM cancel active scripts through the normal
cleanup path and return exit status 130. Child stdout and stderr are drained
concurrently; retained text is capped at 1 MiB per stream. Invalid UTF-8 uses
replacement characters and control bytes are encoded inside JSON strings.
On Unix, confirmation and destination prompts also cancel while stdin remains
open, preserving terminal settings and input intended for later consumers.
Capture cannot recover original process arguments, command history, plugins or
before-script definitions.

After a load processes an input, failures include its observed summary under
`result` in the JSON stderr diagnostic. Human diagnostics list known session
IDs and stages. Completed results remain available there if output fails or
publication is interrupted. These outcomes describe what loading observed;
session liveness may change afterward.

Human load progress requires usable stderr terminal dimensions and a nonempty
`TERM` other than `dumb`. The `default`, `minimal`,
`window`, `pane` and `verbose` formats also accept custom template tokens.
`--progress-lines` bounds retained script lines; `-1` uses terminal height and
`0` streams script output directly. `--no-progress` or `TMUXP_PROGRESS=0`
disables the presenter. `TMUXP_PROGRESS_FORMAT` and `TMUXP_PROGRESS_LINES`
provide defaults while the presenter is active. Disabled progress ignores
these environment defaults; explicit command-line values are still validated.
Machine output always disables terminal rendering.
Script stdout keeps its destination when redirected or connected to a
different terminal; the panel retains stdout only when it shares stderr's
terminal.
Progress follows terminal resizing and restarts on a fresh line after reflow.
If the terminal becomes too small, script output streams directly until the
panel can resume. Fixed line limits remain fixed; `-1` follows the new height.

`--log-level` filters optional warnings and file records. Command failures and
machine operation records remain visible at every level. `load --log-file`
appends UTF-8 JSON lines, with lifecycle records at `info`, script output at
`debug`, and failures at `error`. Each record includes its level, message,
command and event data; child records distinguish stdout from stderr.

File logging is available on Unix. The destination must be a regular file;
directories, pipes, devices and final-path symlinks are rejected before tmux or
Python runs. New files use owner-only permissions, subject to umask. Existing
content and permissions remain intact. A later write or close error disables
the sink and attempts one optional warning after work finishes. Workspace
results, cancellation and child exit statuses remain unchanged by logging
failures, including failure to write that secondary warning.

## Reference tables

The prose above states every rule exactly; these tables exist to be scanned
instead of searched.

### Exit codes

| Exit | Means |
| --- | --- |
| 0 | Succeeded, or a prompt was declined -- answering a question asked is not a failure. |
| 1 | Refused or failed: an `error` code from the shared or machine-code sets below, unless the command spawned a child and passes its exit status through directly (`shell`, `edit`, the Python bridge). |
| 2 | `usage`: the invocation itself was wrong -- bad flags, a required terminal that is not there, a prompt with no way to answer it. |
| 130 | `interrupted`: stopped by a signal, not a refusal or a failure. |

### The shared machine codes

Every `--json`/`--ndjson` terminal error record's `code` is one of these ten,
unless it names this binary's own machinery (next table):

`workspace_not_found`, `invalid_workspace`, `unsupported_key`,
`session_not_found`, `session_mismatch`, `tmux_unavailable`, `tmux_failed`,
`script_failed`, `destination_exists`, `usage`.

### Machine codes outside the shared set

A few conditions describe this binary's own machinery rather than the
workspace or tmux, and keep a code of their own instead of borrowing one of
the shared ones dishonestly:

| Code | Means |
| --- | --- |
| `output_failed` | Writing JSON, NDJSON or human progress output, or restoring the terminal on exit, failed. Not `tmux_failed` -- tmux did not fail. |
| `child_failed` | The `shell` command's Python child (tmuxp) exited unsuccessfully. A `before_script` or plugin bridge failing during `load` is `script_failed`, not this. |
| `editor_failed` | The editor launched for an interactive edit exited unsuccessfully. |
| `compatibility_runtime` | The Python compatibility runtime `shell` and plugin loads depend on is missing or does not meet the version requirement. |
| `client_changed` | The terminal client identified for handoff changed identity between selection and `switch-client`. |
| `unsupported_log_file` | `--log-file` on a platform without file-logging support. |
| `unsupported_terminal` | Terminal state restoration on a platform this binary does not support. |

`interrupted` (exit 130) is a signal outcome, not a refusal or a failure, and
stands beside the shared set for the same reason.

### NDJSON event vocabulary

`--ndjson` emits one JSON object per line, each carrying `schema_version`,
`command`, `event` and an increasing `sequence`. `load`'s events, in the
order one input can produce them:

| Event | When |
| --- | --- |
| `started` | Once, before the first input is touched. |
| `workspace-started` | Before an input's session is resolved. |
| `session-created` | A session was created for an input (not on reuse or append). |
| `window-created` | A window finished building. |
| `pane-created` | A pane finished building. |
| `pane-completed` | A pane's commands were sent. |
| `window-completed` | Every pane in a window is complete. |
| `script-started` / `script-output` / `script-completed` | A `before_script` or Python bridge child ran; `script-output` streams its stdout/stderr. |
| `workspace-completed` | An input finished, success or failure. |
| `warning` | A non-fatal problem: an unrecognised `workspace_builder_options` key, a missing `start_directory`, a declined prompt, or a per-input load failure that leaves the command's other inputs unaffected. |
| `completed` / `failed` | Once, the whole command's outcome. |

### Environment variables

| Variable | Effect |
| --- | --- |
| `TMUX`, `TMUX_PANE` | Identify the attached daemon and invoking pane for an attached `load` and for `--append`. |
| `NO_COLOR`, `CLICOLOR`, `CLICOLOR_FORCE`, `FORCE_COLOR` | The usual precedence for disabling or forcing ANSI styling; `--color` overrides all of them explicitly. |
| `TERM` | Must be set and not `dumb` for human load progress to render. |
| `TMUXP_PROGRESS`, `TMUXP_PROGRESS_FORMAT`, `TMUXP_PROGRESS_LINES` | Defaults for `--no-progress`, `--progress-format` and `--progress-lines` while the presenter is active. |
| `TMUXP_DETECT_TERMINAL_SIZE` | Set to anything but `1` to skip live terminal-size detection in progress rendering. |
| `TMUX_WORKSPACE_PYTHON` | The Python interpreter used for `shell`, plugins and Python-backed search, overriding auto-detection. |
| `EDITOR` | The editor `edit` launches. |
| `TMUXP_CONFIGDIR`, `XDG_CONFIG_HOME`, `TMUXINATOR_CONFIG` | Extra workspace-file search directories, matching tmuxp and tmuxinator's own conventions. |

## Search and Python compatibility

Search uses native Go regular expressions by default, including `-F` literal
matching. It does not require Python. Whole-word matching groups alternatives;
its word boundaries follow Go's ASCII word rules. Structured pane commands are
searched as JSON text.

Use `--regex-engine python` explicitly for lookaround, backreferences, Python
object representations or Unicode word boundaries. That mode checks Python
3.10 or newer and uses the same native discovery and field extraction. Both
engines group whole-word alternatives. Unsupported Go syntax reports the
explicit Python option without starting a compatibility process automatically.

`shell` and workspace plugin/custom-builder execution require an installed
tmuxp 1.74.0 distribution. Set `TMUX_WORKSPACE_PYTHON` to its Python executable;
the CLI checks distribution metadata before invoking it. Python selectors,
startup settings and vi-mode settings retain their Python meaning. Startup
files require `--use-pythonrc`; the default is disabled. The paired startup and
vi-mode options use the last occurrence. Missing optional runtimes
produce an explicit error. Plugin and custom-builder loads use the same
append selection as native loads: `--append` targets the retained session even
when `-d` is also supplied. Append authenticates before checking the Python
runtime. Its private adapter checks the borrowed daemon and session before
importing extensions and again after constructing the builder. A missing or
replaced target fails without creating a replacement session, and failures
retain the original session ID. These checks do not make later Python builder
execution atomic against external daemon replacement or arbitrary plugin code.
Append with both a Python plugin/custom builder and a document `before_script`
is unavailable and fails during preflight. The checked Python builder deletes
borrowed sessions on script failure; the CLI blocks that combination. Native
scripted append and plugin append without a document script remain supported.

Run Python against a selected session:

```console
$ tmux-workspace shell \
    -L project \
    -c 'print(session.session_name)' \
    --json \
    project
```

`EDITOR` uses POSIX shell-style quoting and passes argv directly. Editor exit
status is returned. Global workspace discovery follows `TMUXP_CONFIGDIR`, XDG,
then the legacy tmuxp directory. Tmuxinator import honors `TMUXINATOR_CONFIG`.
Extensionless names search the corresponding configuration directory; filenames
with extensions resolve from the invocation directory.

`LIBTMUX_TMUX_FORMAT_SEPARATOR` is not supported by the native framed codec.

## Command reference and completion

Export the machine-readable command graph:

```console
$ tmux-workspace --command-tree
```

Generate a complete Markdown reference from the same Cobra tree:

```console
$ tmux-workspace --generate-docs markdown
```

`man` and `yaml` are also accepted. Generate completion for the current shell:

```console
$ tmux-workspace --generate-completion zsh
```

The completion generators also support `bash`, `fish`, and `powershell`.
