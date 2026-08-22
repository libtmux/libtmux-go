# Contributing

Thanks for looking. This repository holds four Go modules, and the gates below
are what a change has to pass. `AGENTS.md` covers the same ground in more
detail and is the authority where the two disagree. For how to write a doc
comment, a changelog entry, or a commit message, follow
[WRITING.md](WRITING.md).

## Getting set up

You need Go 1.26 or newer and tmux 3.2a or newer on `PATH`. Nothing else — the
core module has no runtime dependencies.

Give tmux a socket directory of its own before running anything. Sibling
checkouts run their suites on the same machine, and a shared socket directory is
one suite ending servers another is still using:

```console
$ export TMUX_TMPDIR=/tmp/libtmux-go-test && mkdir -p "$TMUX_TMPDIR" && unset TMUX TMUX_PANE
```

## The gates

`go test ./...` stops at a module boundary, so each module is named in turn.
Format, lint, vet, and test the tmux module:

```console
$ gofumpt -w . && golangci-lint run ./... && go vet ./... && go test ./...
```

Then the others, each from its own directory:

```console
$ for module in examples workspace mcp benchmarks; do (cd "$module" && gofumpt -w . && golangci-lint run ./... && go vet ./... && go test ./...) || break; done
```

Run them with the workspace on, which is the only way they see the working
tree. `mcp` carries no `replace` directive — `go install` refuses a module that
does — so `GOWORK=off` swaps this repository's core for whatever release its
`require` names. Whether each module resolves without a workspace is its own
gate, `TestEveryModuleResolvesWithoutAWorkspace`, rather than a setting draped
over every run.

Generated code is checked in. Regenerate it and confirm the tree is unchanged
rather than trusting that it is:

```console
$ go generate ./... && git diff --exit-code
```

The race detector is not optional before anything ships:

```console
$ go test -race -count=1 ./...
```

One tmux is not enough. The supported range is a claim the README makes, and
version-specific breakage is real: tmux 3.4 stopped accepting `split-window`'s
`-p` flag that 3.3a and 3.5 both take. `scripts/matrix.sh` runs every module
against every supported release, and skips with an explanation when it has no
directory of tmux builds to use:

```console
$ bash scripts/matrix.sh
```

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

## Flaky, or broken?

Some real-tmux tests are load-sensitive. A single failure is worth re-running in
isolation before blaming a change, and worth investigating rather than shrugging
at: several have turned out to be real defects in the test. Three signs it is
the machine and not the code — the failing test moves between runs, the failure
reads as `no server running` or `WaitDelay expired` rather than a wrong value,
and the file it is in is not one the change touched.

## Writing

For doc comments, `CHANGELOG.md`, release notes, commit messages, Markdown, and
source comments, follow [WRITING.md](WRITING.md).
