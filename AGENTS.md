# AGENTS.md

Rules for this repository: `libtmux` for Go, a port of the Python library of
the same name. Nothing here is Python — a convention you recognise from that
project (uv, ruff, mypy, pytest, doctests, NumPy docstrings) does not apply
unless a file here says so.

Follow the conventions already in the tree, and keep a change scoped to what
was asked for.

## What is here

Five Go modules, each with its own `go.mod`:

| Path | Module | What it is |
| --- | --- | --- |
| `.` | the tmux module | Server, sessions, windows, panes, options, hooks, formats, filters, snapshots, connections, and plans. Zero runtime dependencies. |
| `workspace/` | consumer | Loads tmuxp-style YAML workspaces and builds them |
| `mcp/` | consumer | Serves one tmux server to Model Context Protocol clients |
| `examples/` | consumer | Compiles the examples quoted in generated documentation |
| `benchmarks/` | tool | Prints what each way of reaching tmux costs |

Inside the tmux module, `tmuxq` holds model-free generics, `tmuxtest` gives
tests a real tmux server that cleans itself up, and `internal/` holds the
subprocess transport, the checked-in generators, and `internal/integration`.
`examples/` is a module of its own.

## Which policy applies

- Documentation, user-facing text, `CHANGELOG.md`, release notes, commit
  messages, doc comments, and source comments:
  [.github/WRITING.md](.github/WRITING.md)
- Building, testing, linting, the Go floor, the tmux matrix, and pull requests:
  [.github/CONTRIBUTING.md](.github/CONTRIBUTING.md)
- Anything under `mcp/`: [mcp/AGENTS.md](mcp/AGENTS.md)
- Reporting or assessing a vulnerability: [SECURITY.md](SECURITY.md)

Each of those is the single home for its subject. Where a rule seems to be
stated twice, the file listed above is the one that governs.

## Change discipline

- Make the smallest coherent change that solves the verified problem; keep
  unrelated cleanup out of it.
- Reuse an existing file, helper, API, or test before adding a new one.
- Keep new APIs unexported until a caller outside the package needs them.
- Add a file only for a durable boundary — a distinct responsibility,
  independent reuse, or splitting an oversized module — not for a single-use
  helper or a one-line re-export.
- A passing gate is evidence only once it has been shown capable of failing.
  Pair a new test with a deliberate break that proves it bites.

## References

- [DESIGN.md](DESIGN.md) — the conventions this package holds itself to, and
  the bakeoff decisions behind them
- [PARITY.md](PARITY.md) — how the surface is checked against the Python
  library
- [BENCHMARKS.md](BENCHMARKS.md) — what each way of reaching tmux costs
- Go doc comments: https://go.dev/doc/comment
- Go release notes, the model for the changelog: https://go.dev/doc/go1.24
- Python library: https://libtmux.git-pull.com/
- tmux manual: http://man.openbsd.org/OpenBSD-current/man1/tmux.1
