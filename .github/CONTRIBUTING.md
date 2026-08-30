# Contributing

Thanks for looking. This repository holds five Go modules, and the gates below
are what a change has to pass.

This file is how we work. For how we write — doc comments, `CHANGELOG.md`,
release notes, commit messages, Markdown, source comments — follow
[WRITING.md](WRITING.md). For anything under `mcp/`, read
[`mcp/AGENTS.md`](../mcp/AGENTS.md) as well; testing that server needs a real
client, which the Go tests do not provide.

## Getting set up

You need Go 1.26 or newer and tmux 3.2a or newer on `PATH`. Nothing else — the
core module has no runtime dependencies.

Give tmux a socket directory of its own before running anything, and drop the
inherited pane. Sibling checkouts run their suites on the same machine, and a
shared socket directory is one suite ending servers another is still using —
which surfaces as a version-specific failure that has nothing to do with the
version:

```console
$ export TMUX_TMPDIR=/tmp/libtmux-go-test && mkdir -p "$TMUX_TMPDIR" && unset TMUX TMUX_PANE
```

## The gates

`go test ./...` stops at a module boundary, so every module is named one at a
time or it rots unchecked.

The linter is pinned in the tests workflow and a local install is commonly
older. `golangci-lint` typechecks against the toolchain's own export data, so
the two disagree in both directions: an older binary reports a newer Go's
packages as typecheck failures, and a newer one reports lints an older one has
no analyzer for. Run the pinned release before believing a clean local one:

```console
$ go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1 run ./...
```

Format, lint, vet, and test the tmux module:

```console
$ gofumpt -w . && golangci-lint run ./... && go vet ./... && go test ./...
```

Then each of the others, from its own directory:

```console
$ for module in examples workspace mcp benchmarks; do (cd "$module" && gofumpt -w . && golangci-lint run ./... && go vet ./... && go test ./...) || break; done
```

Run them with the workspace on, which is the only way they see the working
tree. `mcp` carries no `replace` directive — `go install` refuses a module that
does — so `GOWORK=off` swaps this repository's core for whatever release its
`require` names. Compiling current consumer source in that mode during ordinary
development confuses source compatibility with whether sibling releases have
already been published.

The repository keeps those claims separate. Workspace-on builds prove the
current source graph. `go mod tidy -diff` with the workspace off proves each
module's released dependency graph is standalone and tidy:

```console
$ go test \
    -run TestEveryModuleMetadataResolvesWithoutAWorkspace \
    ./tmux/internal/integration/
```

The install gate runs the documented version-suffixed command, so it tests the
latest published MCP artifact rather than compiling current source against old
sibling releases:

```console
$ go test ./tmux/internal/integration/ -run TestLatestPublishedServerInstalls
```

A `require` naming a module of this repository is a copy of a tag, so it is
checked against the tag list rather than against the other copies. Four of them
agreeing on one stale version is the failure that check exists for, and it is
the failure that happened:

```console
$ go test ./tmux/internal/integration/ -run TestEveryRequirementNamesTheNewestRelease
```

Release sibling modules in dependency order: core, workspace, then MCP. After
publishing core, update workspace's core requirement and test it with
`GOWORK=off` before publishing workspace. Then update both requirements in MCP,
run its complete suite with `GOWORK=off`, publish MCP, and install that explicit
MCP version. An ordinary feature branch cannot satisfy that release-candidate
gate because the versions it needs do not exist yet; do not add temporary tags,
pseudo-versions, compatibility shims, or a checked-in replacement to pretend
they do.

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

It reports the standard library as well as dependencies, so it fails on a
toolchain behind the latest patch even when nothing here has changed. CI sets
up `stable` and does not see it. `mcp` is where it lands first, because the MCP
SDK's transports are what reach `crypto/tls` and `net/url`. Upgrade the
toolchain rather than suppressing the finding.

Generated code is checked in, so regenerate it and confirm the tree is
unchanged rather than trusting that it is. The Go in the Markdown is generated
too, quoted from programs that compile and run, so a snippet cannot drift from
the code it came from. The markers, and what happens when one of them is wrong,
are in [examples/README.md](../examples/README.md):

Run generators in every module that owns them:

```console
$ for module in . mcp; do (cd "$module" && go generate ./...) || break; done
```

Then confirm that regeneration changed nothing:

```console
$ git diff --exit-code
```

The race detector is not optional before anything ships:

```console
$ go test -race -count=1 ./...
```

One tmux is not enough. The supported range is a claim the README makes, and
version-specific breakage is real: tmux 3.4 stopped accepting `split-window`'s
`-p` flag that 3.3a and 3.5 both take. Everything above runs against whichever
tmux is on `PATH`, so run the supported releases before anything ships.
`scripts/matrix.sh` runs every compatible module suite, given a directory of
tmux builds with `<version>/bin/tmux` inside it. Every module runs across the
whole 3.2a-through-3.7c range. With no matrix directory, the script skips with
an explanation rather than reporting a pass it did not earn. Set
`LIBTMUX_MATRIX_REQUIRED=1` for a release gate, where absence must fail. With
no explicit `LIBTMUX_MATRIX_VERSIONS`, required mode checks all nine supported
builds; setting it intentionally narrows the run:

```console
$ LIBTMUX_MATRIX_REQUIRED=1 bash scripts/matrix.sh
```

### The language floor

The floor tracks upstream's support window, so it moves. Five places state it
and all five have to agree: the `go` directive in each module's `go.mod`, the
one in `go.work`, `run.go` in every module's `.golangci.yml`, the version matrix
in the tests workflow, and the claim README.md makes. `go build` will not catch
a disagreement — the `go` directive does not gate standard library APIs.
`go vet` does, reporting `X requires goN.M or later`, which is why vet runs
ahead of the tests in CI.

Raising the floor unlocks syntax, and two tools find it. Neither alone is
enough.

`golangci-lint`'s modernize linter reports what is available at the version
`run.go` names, and gates it, so the older forms cannot come back. It does not
carry every analyzer `go fix` has — `errorsastype` is one it lacks — so a clean
lint run is not proof there is nothing left.

`go fix` has the full set, but a whole-package `go fix ./...` silently drops
fixes that conflict within a package and then reports nothing on a second pass,
so it reads as converged while leaving rewrites behind. Applying one analyzer
at a time with `go fix -<analyzer>` does not.

## Where a test goes

`tmux/internal/integration` holds the tests that drive a real tmux through the
public API. Everything else stays beside the code it tests, and the split is not
a preference:

- Reaches an unexported identifier — stays, because only a test in the package
  can see it.
- Declares an `Example` — stays, because pkg.go.dev renders examples only from
  the package's own directory.
- Parses the package's source, or asserts the surface compiles — stays, because
  it is a structural gate on the package rather than a test of tmux.
- Starts a tmux server — goes to `internal/integration`.

An `Example` must end in an output comment. Without one Go compiles it and never
runs it, so it renders on the package page exactly like a working example while
being free to be wrong. `TestEveryExampleRuns` fails on any that does not, and
its allowlist is for examples that genuinely cannot assert — not a place to park
one nobody has converted.

The other modules are separate module graphs, so `./...` in the tmux module
reaches none of them. Each has to be named explicitly or it rots unchecked.
`examples/` is one of them: it is a module of its own so that an example
reaching for a dependency can never put that dependency in the tmux module's
`go.mod`, which is what keeps the core free of runtime dependencies.

## Flaky, or broken?

Some real-tmux tests are load-sensitive. A single failure is worth re-running in
isolation before blaming a change, and worth investigating rather than shrugging
at: several have turned out to be real defects in the test. Three signs it is
the machine and not the code — the failing test moves between runs, the failure
reads as `no server running` or `WaitDelay expired` rather than a wrong value,
and the file it is in is not one the change touched.

## Done means

Every gate above passes, in every module rather than only the one that changed,
and:

- New exported behaviour carries an `Example` ending in an output comment.
- Anything a caller can observe has an entry under `## Unreleased` in
  `CHANGELOG.md`.

The second is the one nothing enforces. A change that reaches a caller and
leaves no changelog entry is not finished.

## Pull requests

One subject per pull request. Unrelated cleanup found along the way belongs in
its own commit, and usually in its own pull request.

Commit format is in [WRITING.md](WRITING.md). The constraints every change is
held to — scope, reuse, and the rule that a new test is paired with a
deliberate break proving it bites — are in [`AGENTS.md`](../AGENTS.md).
