# Workspace contracts

What the `workspace` package and the `tmux-workspace` command promise beyond
what the [README](../README.md) needs to get a workspace built. Each section is
the full text behind a short bullet there. The command's flags and output
formats are in [`CLI.md`](../CLI.md).

## Parsing a workspace

[`Parse`][workspace-parse] refuses a field it does not recognise rather than
dropping it, so a workspace that loads is a workspace that was understood. A
field it does not know, or a value it cannot read, stops it at the first one,
with its line.

Once every field reads, it reports every problem with what they ask for at
once, such as a missing name or two windows claiming one index. Every such
failure matches [`ErrInvalidWorkspace`][workspace-errinvalidworkspace] under
`errors.Is`.

A layout written out by hand, in the form tmux prints for `window_layout`, is
checked before building: its checksum, its numeric fields, and that its tree is
not empty. A tree may nest 256 levels deep.

Layouts written as JSON are accepted from tmux 3.8, and tmux checks what they
hold. An older daemon is refused them with `ErrVersionTooLow`.

## Building a workspace

A workspace that half exists is never what the caller wanted, so
[`Build`][workspace-build] and [`BuildInto`][workspace-buildinto] check every
layout before they change anything: that the tmux they were handed knows the
layout by that name, and that the layout has room for the panes its window asks
for.

A failure tmux raises while building, such as an option it does not know, is a
tmux command error, and the tmux package's own errors classify it.

`Build` is not atomic. tmux has no transaction, so a failure partway through
leaves what was already created in place. The returned session identifies it,
so you can kill it.

The session `Build` returns came from a creation call, so it carries no windows
of its own: [`session.Windows()`][tmux-session-windows] reports none. Ask tmux
for them with [`Session.SearchWindows`][tmux-session-searchwindows], as the
quick start does, or read everything at once with
[`Server.Snapshot`][tmux-server-snapshot].

`Build` uses ordinary subprocess operations on tmux 3.3a, 3.4, 3.5, and 3.6. On
qualified tmux 3.7 or newer, it holds a control connection during the build.
tmux counts that connection as a client until `Build` closes it before
returning. A cold daemon is first created through an ordinary detached command,
then qualified for control attachment. A later attachment failure can leave the
created session, which the error return identifies.

## Directories that are not there

A `start_directory` that does not exist still builds. tmux falls back to the
user's home directory and reports success, so a workspace naming a directory
that is absent on a given machine builds and puts every pane somewhere else.

[`MissingDirectories`][workspace-workspace-missingdirectories] reports those
before you build, for a caller who would rather say something than let it pass.
`Parse` does not refuse them, because a directory that a `shell_command_before`
creates is ordinary.

## Supported fields

A pane may be written as a bare command string, and `shell_command` and
`shell_command_before` each accept one command or a list. Booleans accept
tmuxp's quoted spellings, so `focus: "true"` and `focus: true` are the same
wherever a boolean is taken.

In Go they are the exported [`Bool`][workspace-bool] type, so a workspace can
be built in code as well as read from a file.

The workspaces in tmuxp's own `examples/` directory parse, apart from those
that use `plugins` or `before_script`, and a test builds them against real
tmux.

## What is not supported

`plugins` loads Python classes and `before_script` runs a script through
tmuxp's own process handling. Both need a Python runtime, so both are refused
rather than ignored.

Values are passed to tmux as written. tmuxp expands `${VAR}` references before
building; this module does not, so a workspace that depends on expansion should
be rendered before it is parsed.

`sleep_before` and `sleep_after` are numbers of seconds, as in tmuxp, so 0.5
is half a second. They pause the build rather than the pane: the delay exists
to let a previous command settle before the next is typed.

`environment` at any level is written to the session, because that is the only
environment tmux keeps. A name set by two windows or two panes ends up holding
whichever was written last, and every process started in that session
afterwards sees it, including panes the user opens by hand.

`options` are applied to the session. A name from tmux's window table is
accepted, as tmux accepts one there, and lands on the session's current window.

`global_options` are applied after the session exists, because tmux has no
global scope until a server is running. The first window is created with the
session and so cannot inherit them; every later window can.

Any option tmux itself accepts under `set-option -g` is accepted here,
whichever of its three option tables declares the name, so `mode-keys` and
`status-style` may sit side by side as they do in a tmuxp file.

A window's first pane is created with the window, so `window_shell` and a
`shell` on that pane name the same command. Either one runs it; setting both is
rejected.

## The library and the command

The CLI and this library are separate implementations, and they read different
dialects of the format: a file one accepts is not always a file the other
accepts. The CLI is the tmuxp-compatible one. Build automation on it and parse
its `--json` or `--ndjson` output.

The table in the README lists where they differ.

## The commands

The command lands in `$(go env GOBIN)`, or in `$(go env GOPATH)/bin` when
`GOBIN` is unset. The examples use a tmux socket named `project`, which keeps
them off the tmux server you have open.

[`tmux-workspace load`][cli-load] prints `Loaded project` and the new
session's id, and `tmux -L project attach` joins the session. Without `-d`,
`load` attaches the terminal to the session, and it needs a terminal to do so.
A second load of the same file finds the session by its name and reuses it.
For a script, `--json` prints a summary of what loaded and never prompts.

[`tmux-workspace ls`][cli-ls] lists a `.tmuxp.yaml`, `.tmuxp.yml`, or
`.tmuxp.json` file in the current directory and each parent up to your home
directory, and the files in the first workspace directory that exists:
`$TMUXP_CONFIGDIR`, `$XDG_CONFIG_HOME/tmuxp` or `~/.config/tmuxp`, then
`~/.tmuxp`. A file in that directory loads by its name, as
`tmux-workspace load project`.

[`tmux-workspace search`][cli-search] prints the name and path of each
workspace with a window matching the term. Without `--field`, a term matches
every field.

[`tmux-workspace freeze`][cli-freeze] writes the session named `project` to
`frozen.yaml`, a workspace file that `load` accepts. With `--json` in place of
`--save-to`, it prints the document.

[`tmux-workspace convert`][cli-convert] turns YAML into JSON, and JSON into
YAML. Keys that native loading does not use are kept.

[`tmux-workspace import`][cli-import] prints the document the file describes,
and refuses what a native workspace cannot express.
[`import teamocil`][cli-import-teamocil] takes the same arguments as
[`import tmuxinator`][cli-import-tmuxinator].

[cli-convert]: https://libtmux.org/en/go/latest/workspace/cli/convert/
[cli-freeze]: https://libtmux.org/en/go/latest/workspace/cli/freeze/
[cli-import]: https://libtmux.org/en/go/latest/workspace/cli/import/
[cli-import-teamocil]: https://libtmux.org/en/go/latest/workspace/cli/import-teamocil/
[cli-import-tmuxinator]: https://libtmux.org/en/go/latest/workspace/cli/import-tmuxinator/
[cli-load]: https://libtmux.org/en/go/latest/workspace/cli/load/
[cli-ls]: https://libtmux.org/en/go/latest/workspace/cli/ls/
[cli-search]: https://libtmux.org/en/go/latest/workspace/cli/search/
[tmux-server-snapshot]: https://libtmux.org/en/go/latest/reference/tmux-server-snapshot/
[tmux-session-searchwindows]: https://libtmux.org/en/go/latest/reference/tmux-session-searchwindows/
[tmux-session-windows]: https://libtmux.org/en/go/latest/reference/tmux-session-windows/
[workspace-bool]: https://libtmux.org/en/go/latest/workspace/reference/workspace-bool/
[workspace-build]: https://libtmux.org/en/go/latest/workspace/reference/workspace-build/
[workspace-buildinto]: https://libtmux.org/en/go/latest/workspace/reference/workspace-buildinto/
[workspace-errinvalidworkspace]: https://libtmux.org/en/go/latest/workspace/reference/workspace-errinvalidworkspace/
[workspace-parse]: https://libtmux.org/en/go/latest/workspace/reference/workspace-parse/
[workspace-workspace-missingdirectories]: https://libtmux.org/en/go/latest/workspace/reference/workspace-workspace-missingdirectories/
