# Ownership and discovery

Run the complete program, including its imports:

```console
$ go -C examples run ./lifecycle
```

The program uses a disposable endpoint because it accepts responsibility for
destroying a whole daemon. It adopts an existing server, session, window and
pane; finds or creates named resources; and scans the socket directory. The
ordinary [quickstart](../quickstart/main.go) uses constructor defaults and a
session owner.

`Owned.Close` has a five-second deadline independent of body cancellation.
`defer owner.CloseInto(&err)` joins body and cleanup errors in a named return.
A successful close is harmless to repeat. A failed close remains retryable.
`AcquisitionError` retains creation and rollback failures; `Cleanup` retains a
failed rollback for retry. An unknown initial result requires inspection of
the selected endpoint before another create.

Lookups and closed client connections leave remote objects alive. Adoption
accepts destruction responsibility by daemon identity and stable object ID.
Renames and moves do not redirect cleanup. Closing a window owner destroys all
its links and panes. Use `Unlink` when only one session link should disappear.
Adoption and owned creation initialize the reserved server option
`@libtmux_owner_generation` when absent. Valid values contain exactly 32 ASCII
hexadecimal characters and are reused; empty or malformed values fail. Do not
change, remove or shadow it. Creation captures the token and resource ID from
the same tmux connection. Cleanup checks the captured token inside its destructive
dispatch, including when a replacement has the same PID and start time.

`Found.Created` distinguishes creation from reuse. Reused handles have no
owner. Session names are exact and unique in tmux; window names match within
one session and can be ambiguous. Pane identity uses a caller-selected pane
user option, which creation sets before returning. Calls sharing the same
`Server` coordination serialize. Independent constructors and other tmux
clients can change those resources between commands.

Server find-or-create matches one endpoint. It keeps normal tmux configuration
loading and proves startup with a private child-environment marker in the
daemon's initial global environment. A competing starter remains borrowed.
Configuration that removes the marker prevents ownership proof. Discovery
scans explicit roots or the captured named-socket root and selected endpoint's
parent, with entry, probe and time bounds. It returns root and candidate errors,
duplicate diagnostics and truncation; its probes cannot start tmux. Explicit
root symlinks follow filesystem semantics. Candidate symlinks require opt-in.

The tests execute this source through `run` and exercise the ordinary quickstart
as an unchanged child program under an external environment harness.
