# workspace

Load tmuxp-style YAML workspace files and build them with the [tmux module].

This is a consumer of the tmux module, not part of it. The tmux module takes no
runtime dependency; parsing YAML needs one, so this lives in its own module and
`go get` on the tmux module never pulls it in.

```console
$ go get github.com/libtmux/libtmux-go/workspace
```

## Using it

```go
import (
    "github.com/libtmux/libtmux-go/tmux"
    "github.com/libtmux/libtmux-go/workspace"
)
```

```go
document, err := os.ReadFile("project.yaml")
if err != nil {
    return err
}
described, err := workspace.Parse(document)
if err != nil {
    return err
}
session, err := workspace.Build(ctx, tmux.NewServer(tmux.ServerOptions{}), described)
```

`Parse` rejects a field it does not recognise rather than dropping it, so a
workspace that loads is a workspace that was understood. Validation reports
every problem it finds at once, each with the line it is on, so a file is fixed
in one pass rather than one run per mistake. It reports every parse
and validation failure as `ErrInvalidWorkspace`; a failure tmux raises while
building, such as an unknown layout or option name, is a tmux command error and
is classified with the tmux package's own sentinels. `Build` uses strict
errors regardless of the server it is handed, because a workspace that half
exists is never what the caller wanted.

`Build` runs over a control connection, so a workspace costs a handful of tmux
processes rather than one per command. That connection is a tmux client while
the build runs: it shows in `list-clients`, counts toward `session_attached`,
and fires a `client-attached` hook. Pass a server carrying
`SubprocessEngine()` to build on processes instead.

`Build` is not atomic. tmux has no transaction, so a failure partway through
leaves what it already created in place; the returned session identifies it, so
you can kill it. Its relation accessors are empty, because it came from a
creation call rather than a snapshot — take a `Server.Snapshot` to inspect what
was built.

A `start_directory` that does not exist is not an error. tmux falls back to the
user's home directory and reports success, so a workspace naming a directory
that is absent on a given machine builds and puts every pane somewhere else.
`Workspace.MissingDirectories` reports those before you build, for a caller who
would rather say something than let it pass. It is not a validation failure,
because a directory a `shell_command_before` creates is ordinary.

## A workspace

```yaml
session_name: project
start_directory: ~/src/project    # created beforehand; tmux falls back to $HOME if absent
environment:
  PROJECT_ENV: development
windows:
  - window_name: editor
    layout: main-vertical
    focus: true
    shell_command_before:
      - cd ~/src/project
    panes:
      - $EDITOR .
      - shell_command:
          - git status
          - cmd: make watch
            sleep_before: 1
  - window_name: shell
    panes:
      - {}
```

## Supported fields

| Scope | Fields |
| --- | --- |
| workspace | `session_name`, `start_directory`, `environment`, `options`, `global_options`, `shell_command_before`, `suppress_history`, `windows` |
| window | `window_name`, `window_index`, `layout`, `start_directory`, `window_shell`, `focus`, `suppress_history`, `options`, `options_after`, `environment`, `shell_command_before`, `panes` |
| pane | `shell_command`, `shell_command_before`, `start_directory`, `shell`, `focus`, `suppress_history`, `environment`, `enter`, `sleep_before`, `sleep_after` |
| command | `cmd`, `sleep_before`, `sleep_after`, `enter` |

A pane may be written as a bare command string, and `shell_command` and
`shell_command_before` each accept one command or a list. Booleans accept
tmuxp's quoted spellings, so `focus: "true"` and `focus: true` are the same
wherever a boolean is taken. In Go they are the exported `Bool` type, so a
workspace can be built in code as well as read from a file.

Of the 23 workspaces in tmuxp's own `examples/` directory, 20 parse and all 20
of those build against real tmux. The three that do not parse use `plugins` or
`before_script`, which need a Python runtime and are rejected rather than
ignored.

## Not supported

`plugins` loads Python classes and `before_script` runs a script through
tmuxp's own process handling. Both need a Python runtime, so both are rejected
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
session and so cannot inherit them; every later window can. Any option tmux
itself accepts under `set-option -g` is accepted here, whichever of its three
option tables declares the name, so `mode-keys` and `status-style` may sit side
by side as they do in a tmuxp file.

A window's first pane is created with the window, so `window_shell` and a
`shell` on that pane name the same command. Either one runs it; setting both is
rejected.

[tmux module]: ../tmux/
