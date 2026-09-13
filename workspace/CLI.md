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

The workspace module also builds with `GOWORK=off` against its published core
dependency. Cobra and presentation dependencies remain outside the core tmux
module.

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
controls the CLI stream. Human freeze saves a file. `--quiet` suppresses status
text; it does not answer prompts. `--yes` does not authorize replacement.
Existing destinations require `--force`. Writes use a same-directory temporary
file and atomic publication.

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
Layout names, checksums, unsigned 32-bit fields, tree structure and required
pane capacity are checked for every native input before scripts or tmux
mutations. Names accept abbreviations that are unique on the selected daemon;
only an unbound cold endpoint uses the configured client version. Permission
and live-daemon query failures remain errors. Trees may nest up to 256 parents;
geometry remains tmux's responsibility.
Default history suppression is enabled. `workspace_builder_options` accepts
`pane_readiness: auto|always|never` and the reference boolean aliases. Auto waits
for zsh prompts; explicit pane or window launch commands skip readiness waits.

Relative directories resolve through the session, window and pane hierarchy
from the configuration directory. This avoids the reference normalizer's
missing-parent and mixed-relative-path cases. Before-script argv uses shell
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

## Local verification

The retained verifier requires tmux and an installed tmuxp 1.74.0 Python
runtime. It uses private sockets, a controlling PTY, and isolated workspace
files. Set `TMUXP_REFERENCE_CHECKOUT` to the pinned reference checkout to add
the example corpus and matched installed-tmuxp timings.

```console
$ python3 workspace/scripts/verify_cli.py \
    --binary "$(go env GOPATH)/bin/tmux-workspace" \
    --output workspace-results.json \
    --reference "$TMUXP_REFERENCE_CHECKOUT"
```

The report includes raw timing samples, binary/script hashes, command inventory,
stream checks and fixture outcomes. A missing example plugin is reported as a
dependency gap; topology checks do not assert that external applications start.
