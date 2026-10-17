<!-- libtmux-logo -->
<p align="center">
  <picture>
    <source srcset="assets/logo.svg" type="image/svg+xml">
    <img src="assets/logo.png" width="128" height="128" alt="libtmux for Go workspace">
  </picture>
</p>
<!-- /libtmux-logo -->

<div align="center">

# workspace

Load tmuxp-style YAML workspace files and build them with the [tmux module].

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/workspace.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/workspace)

</div>

Add it to your module:

```console
$ go get github.com/libtmux/libtmux-go/workspace@latest
```

Alpha software. Releases carry an -alpha prerelease tag and the API is not
settled. Pin an exact version.

**Contents** — [Quick start](#quick-start) ·
[A workspace file](#a-workspace-file) · [Using it](#using-it) ·
[Supported fields](#supported-fields) · [Not supported](#not-supported) ·
[The tmux-workspace CLI](#the-tmux-workspace-cli) · [See also](#see-also)

## Quick start

A workspace is a YAML file that names a session, its windows, and the panes
in each. It is the format [tmuxp](https://tmuxp.git-pull.com/) reads. This
program builds one and prints what tmux made of it:

<!-- docs:file workspace/examples/build-workspace/main.go -->

```go
// Command build-workspace builds a tmuxp-style workspace and prints what tmux
// made of it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/workspace"
)

// document is a workspace file. A program of your own would read one from
// disk with os.ReadFile.
const document = `
session_name: project
windows:
  - window_name: editor
    layout: main-vertical
    panes:
      - echo editing
      - echo testing
  - window_name: logs
    panes:
      - echo api
      - echo worker
      - echo database
`

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) (err error) {
	// Parse refuses a field it does not know rather than dropping it, so a
	// workspace that loads is one that was understood.
	parsed, err := workspace.Parse([]byte(document))
	if err != nil {
		return fmt.Errorf("parse workspace: %w", err)
	}

	// A private server: a socket and an empty config of its own, so this
	// neither touches nor is changed by a tmux you already use.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-build-workspace",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		err = errors.Join(err, server.Kill(context.WithoutCancel(ctx)))
	}()

	session, err := workspace.Build(ctx, server, parsed)
	if err != nil {
		return fmt.Errorf("build workspace: %w", err)
	}

	// Ask tmux what exists now, rather than repeating what the file asked for.
	windows, err := session.SearchWindows(ctx, nil)
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}
	name, _ := session.Name()
	fmt.Printf("session %s: %d windows\n", name, len(windows))
	for _, window := range windows {
		panes, err := window.SearchPanes(ctx, nil)
		if err != nil {
			return fmt.Errorf("list panes: %w", err)
		}
		windowName, _ := window.Name()
		fmt.Printf("window %s: %d panes\n", windowName, len(panes))
	}
	return nil
}
```

<!-- docs:end -->

To run it, save it as `main.go` in an empty directory, with tmux 3.3a or newer
on `PATH`:

```console
$ go mod init demo && \
    go get github.com/libtmux/libtmux-go/workspace@latest && \
    go run .
```

It prints:

<!-- docs:output workspace/examples/build-workspace/output.txt -->

```text
session project: 2 windows
window editor: 2 panes
window logs: 3 panes
```

<!-- docs:end -->

The program builds on a tmux server of its own and stops that server before
it exits, so it leaves nothing behind and does not touch a tmux you already
use.

## A workspace file

A fuller one than the program builds. Each pane is a command to type, or a
list of them:

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

## Using it

A block that starts with `// Given:` is the body of a function that prints
what failed and returns, with the values that line names already in hand.

### [`workspace.Parse`][workspace-parse]: read a workspace

It takes the bytes of a workspace file:

<!-- docs:workspace-parse -->

```go
// Given: document []byte
parsed, err := workspace.Parse(document)
if err != nil {
	fmt.Println("parse:", err)
	return
}
```

<!-- docs:end -->

`Parse` refuses a field it does not recognise rather than dropping it, so a
workspace that loads is a workspace that was understood. A field it does not
know, or a value it cannot read, stops it at the first one, with its line.
Once every field reads, it reports every problem with what they ask for at
once, such as a missing name or two windows claiming one index. Every such
failure matches [`ErrInvalidWorkspace`][workspace-errinvalidworkspace] under
`errors.Is`.

A layout written out by hand, in the form tmux prints for `window_layout`, is
checked before building: its checksum, its numeric fields, and that its tree
is not empty. A tree may nest 256 levels deep. Layouts written as JSON first
appear in tmux 3.8, its release candidates included, and tmux checks what
they hold.

### [`workspace.Build`][workspace-build]: build it as a new session

<!-- docs:workspace-build -->

```go
// Given: ctx context.Context; server tmux.Server; parsed workspace.Workspace
session, err := workspace.Build(ctx, server, parsed)
if err != nil {
	fmt.Println("build:", err)
	return
}

name, _ := session.Name()
windows, err := session.SearchWindows(ctx, nil)
if err != nil {
	fmt.Println("search windows:", err)
	return
}
fmt.Println(name, len(windows))
```

<!-- docs:end -->

The block comes from the package's own example, in
[`example_test.go`](example_test.go), which builds a two-window workspace.

<!-- docs:checked workspace/example_test.go -->
That workspace names its session `review`, so the block prints `review 2`.
<!-- docs:end -->

A workspace that half exists is never what the caller wanted, so `Build` and
`BuildInto` check every layout before they change anything: that the tmux
they were handed knows the layout by that name, and that the layout has room
for the panes its window asks for. A failure tmux raises while building, such
as an option it does not know, is a tmux command error, and the tmux
package's own errors classify it.

`Build` is not atomic. tmux has no transaction, so a failure partway through
leaves what was already created in place. The returned session identifies it,
so you can kill it.

The session `Build` returns came from a creation call, so it carries no
windows of its own: `session.Windows()` reports none. Ask tmux for them with
[`Session.SearchWindows`][tmux-session-searchwindows], as the quick start
does, or read everything at once with
[`Server.Snapshot`][tmux-server-snapshot].

`Build` uses ordinary subprocess operations on tmux 3.3a, 3.4, 3.5, and 3.6. On
qualified tmux 3.7 or newer, it holds a control connection during the build.
tmux counts that connection as a client until `Build` closes it before
returning. A cold daemon is first created through an ordinary detached command,
then qualified for control attachment. A later attachment failure can leave the
created session, which the error return identifies.

### [`workspace.BuildInto`][workspace-buildinto]: build into a session you made

When the session has to exist first, for example to retain a connection on
tmux 3.7 or newer,
[`Workspace.InitialSessionRequest`][workspace-workspace-initialsessionrequest]
gives the request that creates it and `BuildInto` fills in the rest:

<!-- docs:workspace-build-into -->

```go
// Given: ctx context.Context; server tmux.Server; described workspace.Workspace
request, err := described.InitialSessionRequest()
if err != nil {
	fmt.Println("request:", err)
	return
}
_, connection, err := server.NewSessionConnection(
	ctx,
	request,
	tmux.ConnectionOptions{},
)
if err != nil {
	fmt.Println("create:", err)
	return
}
defer func() { _ = connection.Close() }()
session := connection.Session()
if err := workspace.BuildInto(ctx, session, described); err != nil {
	fmt.Println("build:", err)
	return
}
```

<!-- docs:end -->

### [`Workspace.MissingDirectories`][workspace-workspace-missingdirectories]: directories that are not there

A `start_directory` that does not exist is not an error. tmux falls back to
the user's home directory and reports success, so a workspace naming a
directory that is absent on a given machine builds and puts every pane
somewhere else. `MissingDirectories` reports those before you build, for a
caller who would rather say something than let it pass. `Parse` does not
refuse them, because a directory that a `shell_command_before` creates is
ordinary.

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
wherever a boolean is taken. In Go they are the exported
[`Bool`][workspace-bool] type, so a workspace can be built in code as well as
read from a file.

The workspaces in tmuxp's own `examples/` directory parse, apart from those
that use `plugins` or `before_script`, and a test builds them against real
tmux.

## Not supported

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
session and so cannot inherit them; every later window can. Any option tmux
itself accepts under `set-option -g` is accepted here, whichever of its three
option tables declares the name, so `mode-keys` and `status-style` may sit side
by side as they do in a tmuxp file.

A window's first pane is created with the window, so `window_shell` and a
`shell` on that pane name the same command. Either one runs it; setting both is
rejected.

## The tmux-workspace CLI

The [`tmux-workspace` CLI](CLI.md) loads, builds, and manages workspaces from
a shell, with structured output and optional Python compatibility.

The CLI and this library are separate implementations, and they read
different dialects of the format: a file one accepts is not always a file the
other accepts. The CLI is the tmuxp-compatible one. Build automation on it
and parse its `--json` or `--ndjson` output. Where they differ:

| Behaviour | This library | `tmux-workspace` |
| --- | --- | --- |
| `$VAR` in `start_directory` | left alone | expanded |
| `description:` metadata | refused | accepted at every level |
| Keys beginning with `x-` | refused | accepted at every level |
| `before_script` | refused | run |
| `plugins`, `workspace_builder` | refused | run through tmuxp |
| `workspace_builder_options` | refused | read |
| Waiting for a pane's prompt | never | before the pane's first command |
| A missing `start_directory` | `MissingDirectories` | warned on load |
| Problems reported | the first unreadable field, or every problem with what the fields ask for, with lines | one per window, with its line; a document-level refusal still stops at the first |

## See also

This is a module of its own, so `go get` on the tmux library never pulls in
its YAML parser.

| | |
| --- | --- |
| [Reference](https://libtmux.org/en/go/latest/workspace/reference/) | Every exported identifier, method, and field, one page each |
| [`CLI.md`](CLI.md) | The `tmux-workspace` command |
| [`examples/build-workspace`](examples/build-workspace) | The quick start, as a directory you can run |
| [`../tmux/`](../tmux/) | The library this builds with |
| [`../examples/planned-build`](../examples/planned-build) | A layout built in code rather than from a file |

[tmux module]: ../tmux/
[tmux-server-snapshot]: https://libtmux.org/en/go/latest/reference/tmux-server-snapshot/
[tmux-session-searchwindows]: https://libtmux.org/en/go/latest/reference/tmux-session-searchwindows/
[workspace-bool]: https://libtmux.org/en/go/latest/workspace/reference/workspace-bool/
[workspace-build]: https://libtmux.org/en/go/latest/workspace/reference/workspace-build/
[workspace-buildinto]: https://libtmux.org/en/go/latest/workspace/reference/workspace-buildinto/
[workspace-errinvalidworkspace]: https://libtmux.org/en/go/latest/workspace/reference/workspace-errinvalidworkspace/
[workspace-parse]: https://libtmux.org/en/go/latest/workspace/reference/workspace-parse/
[workspace-workspace-initialsessionrequest]: https://libtmux.org/en/go/latest/workspace/reference/workspace-workspace-initialsessionrequest/
[workspace-workspace-missingdirectories]: https://libtmux.org/en/go/latest/workspace/reference/workspace-workspace-missingdirectories/
