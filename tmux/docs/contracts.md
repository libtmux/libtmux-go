# tmux library contracts

What the `tmux` package promises beyond what the [README](../../README.md)
needs to get a first program running. Each section is the full text behind a
short bullet there. The package documentation, `go doc
github.com/libtmux/libtmux-go/tmux`, is the reference for every call.

## The tmux version check

The library needs Go 1.26 or newer and tmux 3.3a or newer, on a Unix host.
Before an operation reaches a daemon older than 3.3a, including unpatched 3.3,
it returns [`tmux.ErrVersionTooLow`][tmux-errversiontoolow] and sends nothing;
a plan leaves its operations skipped.

A tmux that names no release, such as OpenBSD's base tmux or a bare `master`
build, is refused too, and its error says the version is unrecognized rather
than too low. A tmux older than 3.2 cannot answer the check and is refused the
same way.

To fix it, upgrade tmux and restart its server, since a running daemon keeps
its version until it exits, or pin the previous libtmux-go release.

[`Server.Version`][tmux-server-version] and standalone `Cmd` calls with `-V` or
`-h` report the executable, not a daemon, and do not clear later operations.
The check relies on tmux's built-in commands, so a `command-alias` that
redefines one defeats it.

## Optional feature checks

Some operations need more than the 3.3a floor, or meet a defect in one release.
Each is refused with [`tmux.ErrVersionTooLow`][tmux-errversiontoolow] before
anything is sent, and only on the releases that need it:

- Named buffer deletion or rename needs 3.4.
- Raw `split-window -p` is refused on 3.4 alone.
- Raw `break-pane` is refused on exact 3.7. Typed `BreakPane` with a name
  repairs it there.
- A window or session name holding a colon or period is refused on exact 3.7.
- Raw `select-layout` with an unparseable layout, an empty one, or `-o` is
  refused on 3.3a. A preset or a unique prefix of one, a classic layout string,
  and `-n`, `-p` and `-E` are sent.
- Raw `refresh-client` with `-l`, which asks a client for its clipboard, is
  refused below 3.6a. 3.3a exits the server for a client that has a terminal,
  and 3.4 through 3.6 exit it when that client leaves before its terminal
  answers. The typed `RefreshClient` request withholds the flag there instead.
- Owned control clients, connections and observations need 3.7.
- `NewPane` needs 3.7 and is refused as a whole below it.

The checks read the literal command, and resolve its name the way tmux does: an
alias such as `breakp` or `new`, or a unique prefix such as `break-p`, in any
item of a `;` list. They apply to `Server.Cmd`, `Connection.Call` and
`ControlClient.Call` alike.

tmux command strings nested in another command are not checked. That covers an
`if-shell` or `run-shell -C` command, and the body of a brace block. A command
there reaches tmux as written, so do not nest one this library would refuse.
Shell programs and the contents of configured aliases are not checked either.

The bullets above are examples, not the full set. The capability manifest
records every gate that names a tmux command: a command or whole API that needs
a release, and a command a release mishandles. The generated capabilities page
lists them.

Other gates stay out of the manifest. A flag that only one request field
needs, such as a popup, search or `display-message` option, is documented on
that field and follows [`UnsupportedPolicy`][tmux-unsupportedpolicy] where the
request is built: it warns and omits the flag, or fails with
`ErrVersionTooLow`. The minimum supported release, which every API shares, is
a floor and not a capability.

[tmux-errversiontoolow]: https://libtmux.org/en/go/latest/reference/tmux-errversiontoolow/
[tmux-server-version]: https://libtmux.org/en/go/latest/reference/tmux-server-version/
[tmux-unsupportedpolicy]: https://libtmux.org/en/go/latest/reference/tmux-unsupportedpolicy/
