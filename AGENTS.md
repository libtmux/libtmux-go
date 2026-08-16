# AGENTS.md

Rules for this repository: `libtmux` for Go, a port of the Python library of
the same name. Nothing here is Python — a convention you recognise from that
project (uv, ruff, mypy, pytest, doctests, NumPy docstrings) does not apply
unless this file says so.

## What is here

Four Go modules, each with its own `go.mod`:

| Path | Module | What it is |
| --- | --- | --- |
| `.` | the tmux module | Server, sessions, windows, panes, options, hooks, formats, filters, snapshots, engines, and plans. Zero runtime dependencies. |
| `workspace/` | consumer | Loads tmuxp-style YAML workspaces and builds them |
| `mcp/` | consumer | Serves one tmux server to Model Context Protocol clients |
| `benchmarks/` | tool | Prints what each way of reaching tmux costs |

Inside the tmux module, `tmuxq` holds model-free generics, `tmuxtest` gives
tests a real tmux server that cleans itself up, and `internal/` holds the
subprocess transport, the checked-in generators, and `internal/integration`.

### Where a test goes

`internal/integration` holds the tests that drive a real tmux through the public
API. Everything else stays beside the code it tests, and the split is not a
preference:

- Reaches an unexported identifier — stays, because only a test in the package
  can see it.
- Declares an `Example` — stays, because pkg.go.dev renders examples only from
  the package's own directory.
- Parses the package's source, or asserts the surface compiles — stays, because
  it is a structural gate on the package rather than a test of tmux.
- Starts a tmux server — goes to `internal/integration`.

The other modules are separate module graphs, so `./...` in the tmux module
reaches none of them. Each has to be named explicitly or it rots unchecked.
`examples/` is one of them: it is a module of its own so that an example
reaching for a dependency can never put that dependency in the tmux module's
go.mod, which is what keeps the core free of runtime dependencies.

## Gates

`go test ./...` stops at a module boundary, so every module is named one at a
time or it rots unchecked.

Format, lint, vet, and test the tmux module:

```console
$ gofumpt -w . && golangci-lint run ./... && go vet ./... && go test ./...
```

Then each of the other modules, from its own directory:

```console
$ for module in examples workspace mcp benchmarks; do (cd "$module" && gofumpt -w . && golangci-lint run ./... && go vet ./... && go test ./...) || break; done
```

Run them with `GOWORK=off`, which is how a consumer resolves them. A workspace
makes a module that cannot resolve on its own look healthy:

```console
$ GOWORK=off go test ./...
```

Coverage has to name the core module rather than defaulting to the package under
test, because most of what exercises the core lives in `internal/integration`:

```console
$ go test -coverpkg=./... -coverprofile=coverage.out ./...
```

Known vulnerabilities are checked per module, because each resolves its own
dependencies:

```console
$ for module in . examples workspace mcp benchmarks; do (cd "$module" && go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...) || break; done
```

One tmux is not enough, and the supported range is a claim the README makes.
`scripts/matrix.sh` runs every module against every supported release; it needs
a directory of tmux builds and skips with an explanation when there is none,
rather than reporting a pass it did not earn:

```console
$ bash scripts/matrix.sh
```

The Go in the markdown is generated from the examples, so a snippet cannot drift
from the program it was copied from. A region is named where it is written, with
`// docs:<name>` and `// docs:end`, and a markdown file asks for it between
`<!-- docs:<name> -->` and `<!-- docs:end -->`. An unmarked block is hand-written
and says so by not being marked:

```console
$ go generate ./... && git diff --exit-code
```

The race detector is not optional before anything ships:

```console
$ go test -race -count=1 ./...
```

Generated code is checked in. Regenerate it and confirm the tree is unchanged
rather than trusting that it is.

One tmux is not enough. The commands above run against whichever tmux is on
`PATH`; version-specific breakage is real and has shipped before, so run the
supported releases before anything ships. A directory of tmux builds with
`<version>/bin/tmux` inside it, put on `PATH` one at a time, is all it takes.

Some real-tmux tests are load-sensitive. A single failure is worth re-running in
isolation before blaming a change — and worth investigating rather than
shrugging at, since several have turned out to be real defects in the test
rather than flakiness. Three signs it is the machine and not the code: the
failing test moves between runs, the failure reads as `no server running` or
`WaitDelay expired` rather than a wrong value, and the file it is in is not one
the change touched.

### Keep this module's tmux to itself

Sibling checkouts run their own suites on the same machine. Give tmux a socket
directory named for this module and drop the inherited pane, or one suite ends
servers another is still using and reports it as a version-specific failure:

```console
$ export TMUX_TMPDIR=/tmp/libtmux-go-test && mkdir -p "$TMUX_TMPDIR" && unset TMUX TMUX_PANE
```

## Testing the MCP server

The Go tests drive the server over the SDK's in-memory transports. That misses
everything about the real process: JSON-RPC framing, schema validation on the
wire, argument coercion by a client, and the handshake itself. Test through a
real client as well.

### The MCP Inspector

Configure it with a client config file rather than argv. The Inspector's own
flags otherwise consume the server's, and a config file is also how a real
client is configured, so it tests the thing that will actually happen:

```console
$ npx -y @modelcontextprotocol/inspector --cli \
    --config ./mcp.json \
    --server libtmux \
    --method tools/list
```

Four things are worth knowing.

**Give the server the environment a client would.** An MCP client starts its
servers with a curated environment, not the shell's. Declaring only what the
server needs in the config's `env` is what a real client does, and it is what
found the control-connection hang: without a UTF-8 locale, tmux rewrites
control characters in format output, a tab separator came back as an
underscore, and the tmux module's client-registration poll never matched.
Testing with an inherited shell environment hides that entire class of bug.

**`libtmux-mcp: context canceled` is not a fault.** It is the server handling
the SIGTERM it gets when a client tears the transport down, including when the
Inspector's own 15-second connect timeout fires. Treat it as the symptom of a
client giving up, not as the cause.

**Ports do not matter in `--cli` mode.** The CLI builds a `StdioClientTransport`
and spawns the command directly, binding nothing. `CLIENT_PORT` and
`SERVER_PORT` only matter for the web UI — worth setting anyway when another
agent may be using the defaults (6274, 6277).

**Spawn the binary by path.** Giving the Inspector a command it has to resolve,
such as `npx -y some-server`, can end with a bare `sh` reading the JSON-RPC
stream as a shell script: `sh: 1: {method:initialize...}: not found`.

### Driving it by hand

A raw JSON-RPC driver over stdin and stdout is the cheapest way to exercise
every tool against the real binary. Hold stdin open while waiting for replies:
closing it ends the server before it answers, which reads as a server that
produced nothing.

Worth covering, because in-memory tests have missed each of them:

- Every tool, with arguments a client would actually send.
- A tool named with a value rather than the default path. A lookup that only
  ever ran without an explicit name has hidden a broken one.
- Arguments of the wrong type. A client may coerce `command=true` into a
  boolean, and the schema should reject it.
- The protocol versions a client may negotiate.
- A stripped environment, per the Inspector note above.
- The server started from inside a pane of the tmux server it drives, which is
  how it is usually run and the only way to exercise self-detection.

### Fixtures

A workspace pane running a command that exits, such as `true`, takes its
window, its session, and then the tmux server with it. A test asserting what
survived will race that teardown and report a safety guard failing. Give a
fixture a pane that outlives the assertions made about it.

## Documentation

`DESIGN.md` holds the conventions this package holds itself to and the bakeoff
decisions behind them. `PARITY.md` is how the surface is checked against the
Python library. `BENCHMARKS.md` is what each way of reaching tmux costs.

The package documentation is the reference, and is written to be read start to
finish rather than searched:

```console
$ go doc github.com/libtmux/libtmux-go
```

Every exported declaration carries a doc comment, and every switch a caller can
flip is discoverable there rather than only in the README.

## Comments earn their maintenance cost

Keep an implementation comment only when losing it would force a future
maintainer to rediscover a consequential, non-obvious fact that the code,
types, assertions, and tests do not already communicate. It states a durable
truth about the shipped system rather than the author's reasoning, and it does
not restate a value or a fact that can change without it — a comment that
duplicates either goes stale silently. Write it as tersely as a mature,
long-lived library would.

Delete comments that narrate, restate, speculate, excuse, or preserve
development history, and prefer deletion in the borderline case. What survives
is what a reader could not recover from the code.

Doc comments on exported declarations — the package documentation, parameter
descriptions, and the runnable examples — are judged on the other axis: what
they are worth to a caller, not whether they are non-obvious. They stay
precise, succinct, and maintainable.

## Git commits

Format commit messages as:

```
Scope(type[detail]): concise description

why: Explanation of necessity or impact.

what:
- Specific technical changes made
- Focused on a single topic
```

Keep the subject ≤50 chars (excluding any trailing `(#NN)` PR ref); wrap body
lines at ≤72 chars. Separate the `why:` and `what:` blocks with a blank line.

Common types: **feat**, **fix**, **refactor**, **docs**, **chore**, **test**,
**style**. Never create or push tags; the owner handles releases.

For multi-line commits, use a heredoc to preserve formatting:

```bash
git commit -F - <<'EOF'
Pane(feat[send_keys]): Add support for a literal flag

why: Send characters without tmux interpreting them.

what:
- Add a Literal field to SendKeysRequest
- Pass -l when it is set
EOF
```

## Slop prevention

Treat AI slop as review-hostile noise, not as proof that text or code is wrong.
The goal is to maximise information density.

- **AI signatures.** No "Generated by", no conversational filler, no
  unexplained emoji, no tool metadata.
- **Brittle references.** No hard-coded line numbers, fragile file counts, dated
  "as of" claims, bare SHAs, or local absolute paths — unless they are strict
  evidentiary artefacts such as a benchmark log.
- **Diff narration.** Do not restate what moved, was renamed, or was removed in
  anything the reader holds alongside the diff: code, doc comments, README, or
  a PR description. The diff and the commit message already carry it.
- **Branch-internal narrative.** Do not mention intermediate states, abandoned
  approaches, or "no longer" behaviour unless users of a published release
  actually experienced the old state.
- **Low-value scaffolding.** No ownerless TODOs, unused future-proofing, debug
  artefacts, or defensive wrappers around failure modes nothing can reach.
- **Prose inflation.** Replace *comprehensive, robust, seamless,
  production-ready, leverage, delve, best practices* with a concrete
  description of behaviour, constraints, or trade-offs.
- **Coded labels.** Write rules and findings as plain imperatives. No `[R1]`,
  `Option B`, or any index a reader has to decode.

Preserve the "why". Never delete a comment documenting an invariant, a protocol
constraint, a platform quirk, or an upstream workaround — those are the facts
"Comments earn their maintenance cost" keeps, and every other comment is judged
by it.

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

- Python library: https://libtmux.git-pull.com/
- tmux manual: http://man.openbsd.org/OpenBSD-current/man1/tmux.1
