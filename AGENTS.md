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

### The language floor, and what enforces it

The floor tracks upstream's support window, so it moves. Five places state it
and all five have to agree: the `go` directive in each module's `go.mod`, the
one in `go.work`, `run.go` in `.golangci.yml`, the version matrix in the tests
workflow, and the claim README.md makes. `go build` will not catch a
disagreement — the `go` directive does not gate standard library APIs. `go vet`
does, reporting `X requires goN.M or later`, which is why vet runs ahead of the
tests in CI.

Raising the floor unlocks syntax. `golangci-lint` reports what is available at
the version `run.go` names, so the modernize linter finds it once the floor
moves. Prefer that to `go fix` for finding the work: a whole-package
`go fix ./...` silently drops fixes that conflict within a package, reports
nothing on a second pass, and so reads as converged while leaving rewrites
behind. Applying one analyzer at a time with `go fix -<analyzer>` does not.

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

**`libtmux-mcp: terminated signal received` is not a fault.** It is the server
handling the SIGTERM it gets when a client tears the transport down, including
when the Inspector's own 15-second connect timeout fires. Treat it as the
symptom of a client giving up, not as the cause. It used to read `context
canceled`, which named the mechanism and left the reason to be guessed at.

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

### Write what every renderer can render

Doc comments are Go doc comment syntax, which is documented at
<https://go.dev/doc/comment#syntax> and is not Markdown. It has paragraphs,
`# ` headings, lists, indented preformatted blocks, and links. It has no bold,
no italics, and no inline backticks: those arrive at the reader as the
characters they are.

Square brackets are doc links, not emphasis. `[Run]` and `[tmux.Server]`
resolve; a bracketed unexported identifier resolves nowhere and renders as its
own brackets on pkg.go.dev, so write the thing in prose instead.

Markdown files keep to what a plain CommonMark renderer understands. No
renderer-specific extension, and in particular no GitHub alert blocks —
`> [!NOTE]`, `> [!WARNING]` and the rest are literal text everywhere but
GitHub, which is where the fewest readers of a Go module are. State the thing
in a sentence: prose that renders everywhere beats a callout that renders once.

Three things this does not restrict:

- **Code blocks.** Fenced blocks in Markdown are how this repository shows
  commands, and the rules in [Code Blocks](#code-blocks) govern them.
- **Tables, badges, and links** in Markdown. They are CommonMark or near
  enough, and every renderer that matters handles them.
- **Directive comments.** `//go:build`, `//go:generate`, `//nolint:`,
  `//libtmux:real-tmux` and `//libtmux:parity` are instructions to a tool, not
  prose for a reader. They are exempt from all of the above and must not be
  reformatted to suit it.

## Comments earn their maintenance cost

A comment ships only if it passes all three gates. Fail any: delete or rewrite.
Borderline: delete — borderline means the information is reconstructible, which
is what makes deletion cheap.

**Loss.** Three years from now, would losing this cost a maintainer real time
rediscovering intent, an invariant, a constraint, or a failure mode the code and
tests do not already make obvious?

**Elite.** Would SQLite, Redis, the Go standard library, or CPython write this
comment, at this length? Those projects state the constraint and stop. They do
not argue with an imagined objector.

**Upkeep.** Will it stay true without maintenance? A comment that hand-syncs a
value the code owns — a count, an offset, a line reference, a duplicated
constant — is false the first time that value moves.

### Ceiling

One or two lines. A comment reaching four is either carrying several facts, in
which case split it, or arguing, in which case cut it to the fact.

Rationale, alternatives weighed, and the story of how the code got here belong
in the commit message: timestamped, attached to the exact diff, and free to
maintain.

A comment often holds both a constraint and the deliberation that found it. Keep
the constraint, cut the deliberation. "Runs at most once per second" survives;
"this is the right trade for now" does not.

### Keep

- Why over how: upstream quirks, protocol and compatibility constraints,
  performance tradeoffs still part of the contract.
- Invariants, preconditions, ordering, lifetime, and concurrency requirements
  that types and tests cannot express.
- Code that looks wrong but is not, so a later cleanup does not reintroduce the
  bug.
- A high-level sketch of an algorithm whose local operations do not reveal the
  whole.

### Delete

- Narration of the next lines; code translated into English.
- Restated names, types, defaults, or control flow.
- Values duplicated from the code and hand-synced.
- Justification, hedging, or apology for a choice.
- Speculation about future requirements.
- History version control already holds, including commented-out code.
- Ticket and issue numbers. They say nothing to a reader without tracker access,
  and they rot when the tracker moves. Unfinished work goes in the tracker, not
  the source.
- Transient observations — "currently", "for now", "the latest release" —
  that go stale with no nearby edit.

### The upkeep gate in practice

It reaches values that track our own code. It does not reach frozen external
facts.

Bad (Delete):

```go
// There are 321 tests to complete for servers.
```

Good (Keep):

```go
// tmux < 3.2 reports the pane ID only after the command completes,
// so this query must stay separate.
```

### Documentation exception

Doctests, minimal usage examples, and param, return, and raises lines on public
API are exempt from the loss gate — they serve the caller, not the maintainer.
They are exempt from nothing else. Ceiling: a good man page entry.

Exported doc comments and `Example` functions fall under this exception — an
`Example` function is compiled and run.

## Git Commit Standards

Format commit messages as:
```
Scope(type[detail]): concise description

why: Explanation of necessity or impact.

what:
- Specific technical changes made
- Focused on a single topic
```

Keep the subject ≤50 chars (excluding any trailing `(#NN)` PR ref); wrap
body lines at ≤72 chars. Separate the `why:` and `what:` blocks with a
blank line.

Common commit types:
- **feat**: New features or enhancements
- **fix**: Bug fixes
- **refactor**: Code restructuring without functional change
- **docs**: Documentation updates
- **chore**: Maintenance (dependencies, tooling, config)
- **test**: Test-related updates
- **style**: Code style and formatting
- **go(deps)**: Dependencies
- **go(deps[dev])**: Dev Dependencies
- **ai(rules[AGENTS])**: AI rule updates

Example:
```
Pane(feat[send_keys]): Add support for a literal flag

why: Send characters without tmux interpreting them.

what:
- Add a Literal field to SendKeysRequest
- Pass -l when it is set
```

### Release commits

Never create tags. Never push tags. The user handles tagging and tag
pushes (tags trigger the CI publish workflow).

Release commit subjects are plain and short: `Tag v<version>`. Put
the detailed why/what in the commit body. Don't use the
`Scope(type[detail]):` format for releases — don't bury the lede.

For multi-line commits, use heredoc to preserve formatting:
```bash
git commit -m "$(cat <<'EOF'
Scope(feat[detail]): Concise description

why: Explanation of the change.

what:
- First change
- Second change
EOF
)"
```

## Code Blocks

Code blocks are paste-and-run units: pasting one block runs exactly one
intended action. Doctests and other executed examples are exempt — the test
suite runs them, nobody pastes them.

- **One command per block.** Multiple steps may share a block only when
  explicitly chained with `&&`, `;`, or `\` continuations — the chain is
  then one logical command.
- **Explanations go in prose above the block**, never as `#` comments inside it.
- **Command menus are per-command blocks with prose lead-ins**, not tables.
- **Shell commands use the `console` tag with a `$ ` prefix.** This separates
  interactive commands from scripts and enables prompt-aware copy.
- **Split long commands with `\`** — one flag or flag+value pair per indented
  continuation line, positional arguments last.

Good:

Show the last ten commits as a graph:

```console
$ git log \
    --max-count=10 \
    --graph \
    --oneline
```

Bad:

```console
# Show the last ten commits as a graph
$ git log --max-count=10 --graph --oneline
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
