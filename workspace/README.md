<!-- libtmux-logo -->
<div align="center">
  <picture>
    <source srcset="assets/logo.svg" type="image/svg+xml">
    <img src="assets/logo.png" width="128" height="128" alt="libtmux for Go workspace">
  </picture>
</div>
<!-- /libtmux-logo -->

<div align="center">

# workspace

Load tmuxp-style YAML workspace files and build them with the [tmux module].

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/workspace.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/workspace)
[![libtmux.org](https://img.shields.io/badge/docs-libtmux.org-0b7285)](https://libtmux.org/en/go/latest/workspace/)

</div>

Add it to your module:

```console
$ go get github.com/libtmux/libtmux-go/workspace@latest
```

Releases are numbered `0.1.N`, and each release increments `N`. The API is not
settled: any release may change or remove exported identifiers, and the
changelog marks each break. Pin an exact version to avoid one.

**Contents** — [Quick start](#quick-start) ·
[A workspace file](#a-workspace-file) · [Using it](#using-it) ·
[Supported fields](#supported-fields) · [What it leaves out](#what-it-leaves-out) ·
[The tmux-workspace CLI](#the-tmux-workspace-cli) · [Packages](#packages) ·
[Support and status](#support-and-status) · [See also](#see-also)

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

	// Choose an unused socket name: cleanup kills the server selected here.
	// The empty config keeps personal settings out of a new server.
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

The program uses the socket name `libtmux-build-workspace` and stops that server
before it exits. Choose an unused name: an existing server on that socket can be
modified or killed. [`workspace.Parse`][workspace-parse] reads the document and
[`workspace.Build`][workspace-build] builds it.

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

Each key has a page on libtmux.org: [session][config-session],
[windows][config-windows], [panes][config-panes],
[commands][config-commands], [environment][config-environment], and
[layouts][config-layouts].

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

- **It refuses what it does not know.** A field it does not recognise, or a
  value it cannot read, stops it at the first one, with its line.
- **Then it reports every problem at once,** such as a missing name or two
  windows claiming one index. Each matches
  [`ErrInvalidWorkspace`][workspace-errinvalidworkspace] under `errors.Is`.
- **Layouts written by hand are checked** for their checksum, numeric fields,
  and a tree that is not empty.

The package example below misspells `windows` as `window`:

<!-- docs:parse-unknown -->

```go
// The key is windows, so window fails the parse.
_, err := workspace.Parse([]byte("session_name: review\nwindow:\n  - {}\n"))
fmt.Println(err != nil)
```

<!-- docs:end -->

<!-- docs:checked workspace/example_test.go -->
The parse fails, so it prints `true`.
<!-- docs:end -->

[The contract][contract-parsing] covers JSON layouts and nesting.

### [`workspace.Build`][workspace-build]: build it as a new session

It builds the parsed workspace on a tmux server and returns the session:

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

- **Layouts are checked first.** [`Build`][workspace-build] and
  [`BuildInto`][workspace-buildinto] check every layout before they change
  anything.
- **A build is not atomic.** tmux has no transaction, so a failure partway
  through leaves what was already created. The returned session identifies it.
- **A control connection from tmux 3.7.** Below tmux 3.7, `Build` uses
  ordinary subprocess operations. On qualified tmux 3.7 or newer it holds a
  control connection that tmux counts as a client until `Build` returns.
- **The session carries no windows.** Ask tmux with
  [`Session.SearchWindows`][tmux-session-searchwindows], as the quick start
  does.

[The contract][contract-building] explains the connection
[`Build`][workspace-build] holds while it runs.

### [`workspace.BuildInto`][workspace-buildinto]: build into a session you made

When the session has to exist first, for example to retain a connection on
tmux 3.7 or newer,
[`Workspace.InitialSessionRequest`][workspace-workspace-initialsessionrequest]
gives the request that creates it and [`BuildInto`][workspace-buildinto] fills
in the rest:

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

The package example lists the finished session's windows.

<!-- docs:checked workspace/example_test.go -->
The workspace has two windows, so it prints `2`.
<!-- docs:end -->

### [`Workspace.MissingDirectories`][workspace-workspace-missingdirectories]: directories that are not there

A `start_directory` that does not exist still builds: tmux falls back to the
home directory and reports success. `MissingDirectories` reports those
before you build, and [`Parse`][workspace-parse] does not refuse them.
[The contract][contract-directories] says why.

## Supported fields

| Scope | Fields |
| --- | --- |
| workspace | `session_name`, `start_directory`, `environment`, `options`, `global_options`, `shell_command_before`, `suppress_history`, `windows` |
| window | `window_name`, `window_index`, `layout`, `start_directory`, `window_shell`, `focus`, `suppress_history`, `options`, `options_after`, `environment`, `shell_command_before`, `panes` |
| pane | `shell_command`, `shell_command_before`, `start_directory`, `shell`, `focus`, `suppress_history`, `environment`, `enter`, `sleep_before`, `sleep_after` |
| command | `cmd`, `sleep_before`, `sleep_after`, `enter` |

- **A pane may be a bare string,** and `shell_command` takes one command or a
  list.
- **Booleans accept tmuxp's quoted spellings,** so `focus: "true"` and
  `focus: true` match. In Go they are the [`Bool`][workspace-bool] type.
- **tmuxp's own examples parse,** apart from those that use `plugins` or
  `before_script`, and a test builds them against real tmux.

[The contract][contract-fields] has the rest.

## What it leaves out

- **Python plugins and scripts.** `plugins` and `before_script` need a Python
  runtime, so the library refuses both. [The CLI](#the-tmux-workspace-cli) runs
  them.
- **Variable expansion.** Values reach tmux as written, so `${VAR}` is not
  expanded. Render the workspace before you parse it.
- **Per-pane environments.** `environment` at any level goes to the session,
  the only environment tmux keeps. A name set twice holds the last value.
- **Sleeping panes.** `sleep_before` and `sleep_after` pause the build, in
  seconds. They do not pause the pane.

[The contract][contract-unsupported] covers `options`, `global_options`, and
`window_shell`.

## The tmux-workspace CLI

The [`tmux-workspace` CLI](CLI.md) loads, builds, and manages workspaces from
a shell, with structured output and optional Python compatibility. The
[command reference][cli-reference] on libtmux.org renders `CLI.md` as a page.

The library and the CLI are separate implementations that read different
dialects. The CLI is the tmuxp-compatible one: build automation on it, and
parse its `--json` or `--ndjson` output. Where they differ:

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

### [`go install`][guide-installation]: get the command

```console
$ go install github.com/libtmux/libtmux-go/workspace/cmd/tmux-workspace@latest
```

The command lands in `$(go env GOBIN)`, or in `$(go env GOPATH)/bin` when
`GOBIN` is unset. The examples below use the tmux socket name `project`.
Choose an unused name; the cleanup command kills every session on that socket.

### [`tmux-workspace load`][cli-load]: build a workspace

Save the file under [A workspace file](#a-workspace-file) as `project.yaml`.
This builds it without attaching:

```console
$ tmux-workspace load \
    -L project \
    -d \
    ./project.yaml
```

It prints `Loaded project` and the new session's id, and `tmux -L project
attach` joins the session. A second load of the same file reuses the session.

For a script, `--json` prints a summary of what loaded and never prompts:

```console
$ tmux-workspace load \
    -L project \
    -d \
    --json \
    ./project.yaml
```

Without `-d`, `load` attaches your terminal, so it needs one.

### [`tmux-workspace ls`][cli-ls]: find workspace files

```console
$ tmux-workspace ls --tree
```

It lists a `.tmuxp.yaml`, `.tmuxp.yml`, or `.tmuxp.json` file in the current
directory and each parent up to your home directory, and the files in your
workspace directory.
A file there loads by its name, as [`tmux-workspace load project`][cli-load].

### [`tmux-workspace search`][cli-search]: find a workspace by what it holds

```console
$ tmux-workspace search \
    --field window \
    logs
```

It prints the name and path of each workspace with a window matching `logs`.
Without `--field`, a term matches every field.

### [`tmux-workspace freeze`][cli-freeze]: capture a running session

```console
$ tmux-workspace freeze \
    -L project \
    --save-to ./frozen.yaml \
    project
```

It writes the session named `project` to `frozen.yaml`, a workspace file that
`load` accepts. With `--json` in place of `--save-to`, it prints the document.

### [`tmux-workspace convert`][cli-convert]: change a document's format

```console
$ tmux-workspace convert \
    --save-to ./project.json \
    ./project.yaml
```

YAML becomes JSON, and JSON becomes YAML. Keys that native loading does not use
are kept.

### [`tmux-workspace import`][cli-import]: bring in a tmuxinator or teamocil file

```console
$ tmux-workspace import tmuxinator \
    --json \
    ./sample.yml
```

It prints the document the file describes, and refuses what a native workspace
cannot express. [`import teamocil`][cli-import-teamocil] takes the same
arguments as [`import tmuxinator`][cli-import-tmuxinator].

Remove what these commands built with `tmux -L project kill-server`. [The
contract][contract-commands] has the rest of each command.

## Packages

### [`workspace`](https://github.com/libtmux/libtmux-go/tree/master/workspace): the library

[`workspace.Parse`][workspace-parse], [`workspace.Build`][workspace-build], and
[`workspace.BuildInto`][workspace-buildinto] load and build a workspace.
Reference: [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/workspace)
and [libtmux.org][workspace-reference].

### [`tmux-workspace`](https://github.com/libtmux/libtmux-go/tree/master/workspace/cmd/tmux-workspace): the command

It loads, captures, converts, and imports workspace files from a shell.
Reference: [`CLI.md`](CLI.md) and the [command reference][cli-reference].

### [`build-workspace`](https://github.com/libtmux/libtmux-go/tree/master/workspace/examples/build-workspace): the quick start as a directory

The program above, with a page of its own, ready to run from a clone.

## Support and status

- **Go 1.26+ and tmux 3.3a or newer.** CI runs tmux 3.3a through 3.8 against
  this module.
- **Linux, including WSL, and macOS.** CI runs this module's tests on Linux
  across the tmux matrix. macOS is supported on a best-effort basis, and CI
  coverage for it is highly limited. On Windows, CI vets the module, and tmux
  does not run there.
- **A module of its own.** `go get` on the tmux library never pulls in its YAML
  parser.
- **Unsettled API.** Any release may change exported identifiers. Pin an exact
  version, tagged as `workspace/v0.1.N`.

## See also

| | |
| --- | --- |
| [Reference](https://libtmux.org/en/go/latest/workspace/reference/) | Every exported identifier, method, and field, one page each |
| [`docs/contracts.md`](docs/contracts.md) | What the library and the command promise beyond this page |
| [`CLI.md`](CLI.md) | The `tmux-workspace` command |
| [`examples/build-workspace`](examples/build-workspace) | The quick start, as a directory you can run |
| [`../tmux/`](../tmux/) | The library this builds with |
| [`../examples/planned-build`](../examples/planned-build) | A layout built in code rather than from a file |
| [Inspect a workspace through MCP][guide-inspect-with-mcp] | Read a loaded session from an agent |

[cli-convert]: https://libtmux.org/en/go/latest/workspace/cli/convert/
[cli-freeze]: https://libtmux.org/en/go/latest/workspace/cli/freeze/
[cli-import]: https://libtmux.org/en/go/latest/workspace/cli/import/
[cli-import-teamocil]: https://libtmux.org/en/go/latest/workspace/cli/import-teamocil/
[cli-import-tmuxinator]: https://libtmux.org/en/go/latest/workspace/cli/import-tmuxinator/
[cli-load]: https://libtmux.org/en/go/latest/workspace/cli/load/
[cli-ls]: https://libtmux.org/en/go/latest/workspace/cli/ls/
[cli-reference]: https://libtmux.org/en/go/latest/workspace/cli/
[cli-search]: https://libtmux.org/en/go/latest/workspace/cli/search/
[config-commands]: https://libtmux.org/en/go/latest/workspace/configuration/commands/
[config-environment]: https://libtmux.org/en/go/latest/workspace/configuration/environment/
[config-layouts]: https://libtmux.org/en/go/latest/workspace/configuration/layouts/
[config-panes]: https://libtmux.org/en/go/latest/workspace/configuration/panes/
[config-session]: https://libtmux.org/en/go/latest/workspace/configuration/session/
[config-windows]: https://libtmux.org/en/go/latest/workspace/configuration/windows/
[contract-building]: docs/contracts.md#building-a-workspace
[contract-commands]: docs/contracts.md#the-commands
[contract-directories]: docs/contracts.md#directories-that-are-not-there
[contract-fields]: docs/contracts.md#supported-fields
[contract-parsing]: docs/contracts.md#parsing-a-workspace
[contract-unsupported]: docs/contracts.md#what-is-not-supported
[guide-inspect-with-mcp]: https://libtmux.org/en/go/latest/workspace/guides/inspect-with-mcp/
[guide-installation]: https://libtmux.org/en/go/latest/workspace/guides/installation/
[tmux module]: ../tmux/
[tmux-session-searchwindows]: https://libtmux.org/en/go/latest/reference/tmux-session-searchwindows/
[workspace-bool]: https://libtmux.org/en/go/latest/workspace/reference/workspace-bool/
[workspace-build]: https://libtmux.org/en/go/latest/workspace/reference/workspace-build/
[workspace-buildinto]: https://libtmux.org/en/go/latest/workspace/reference/workspace-buildinto/
[workspace-errinvalidworkspace]: https://libtmux.org/en/go/latest/workspace/reference/workspace-errinvalidworkspace/
[workspace-parse]: https://libtmux.org/en/go/latest/workspace/reference/workspace-parse/
[workspace-reference]: https://libtmux.org/en/go/latest/workspace/reference/
[workspace-workspace-initialsessionrequest]: https://libtmux.org/en/go/latest/workspace/reference/workspace-workspace-initialsessionrequest/
[workspace-workspace-missingdirectories]: https://libtmux.org/en/go/latest/workspace/reference/workspace-workspace-missingdirectories/
