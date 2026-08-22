# Contributing

Thanks for looking. This repository holds four Go modules, and the gates below
are what a change has to pass. `AGENTS.md` covers the same ground in more
detail and is the authority where the two disagree, except on writing: the
conventions under [Writing](#writing) govern doc comments, Markdown, and the
changelog, and `AGENTS.md` points here for them.

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

Three surfaces, one voice. A doc comment says what a caller may rely on; a
changelog entry says what changed; prose says what happens. All three are
present tense, lead with the thing being described, and stop. Why it was built
that way belongs in the commit message, which is timestamped and attached to
the diff.

The most useful editing operation is deleting the introductory sentence.

| Instead of | Prefer |
| --- | --- |
| "We added…" | "`Foo` now supports…" |
| "New and improved" | "`Foo` now…" |
| "powerful", "seamless" | state the capability |
| "easily", "simply" | omit |
| "robust" | name the failure that is handled |
| "optimized" | give the magnitude |
| "various fixes" | name the components |
| "under the hood" | omit unless observable |
| "please note that" | state the fact |
| "in order to" | "to" |
| "utilize" | "use" |

### Doc comments

Every exported declaration has one, and it begins with the name of the thing it
describes — `go doc` and pkg.go.dev use that to build the summary, so
`// Start boots the server` works and `// This function starts the server` does
not. The first sentence stands alone and ends with a period. Use `//` on every
line, never `/* */`.

Document the contract, not the implementation: what a caller may rely on. Zero
values, what `nil` means, ownership, mutation, ordering, whether it blocks,
whether it is safe for concurrent use, which errors are returned, whether
returned memory aliases the input. Those are what a reader infers the API from.
A comment describing the mutex inside a type is not documentation.

Break long comment lines at sentence and clause boundaries rather than at a
column, so an edit shows as one changed line rather than a reflowed paragraph.
Indent a line to render it as a code block.

Doc comments are Go doc comment syntax, not Markdown. It has paragraphs, `# `
headings, lists, indented preformatted blocks, and links. It has no bold, no
italics, and no inline backticks — those arrive at the reader as the characters
they are. Square brackets are doc links: `[Run]` and `[tmux.Server]` resolve,
and a bracketed unexported identifier renders as its own brackets, so write it
in prose instead.

### Implementation comments

Keep one only when losing it would force a future maintainer to rediscover a
consequential, non-obvious fact that the code, types, assertions, and tests do
not already communicate: an upstream quirk, an invariant a type cannot express,
code that looks wrong and is not. Never narrate the next line. `AGENTS.md`
states the rule in full, with its keep and delete lists.

### The changelog

A ledger, not a narrative. It is scanned, and the question a reader is asking
is whether an entry affects them, so one change gets one bullet:

```markdown
### mcp

- `run_command` no longer returns its own sourcing line as output.
- Add `onError` to the three batch tools, choosing between stopping at the
  first failure and running the calls after it. Stopping remains the default.
```

Group by the component affected, not by whether something is a feature or a
fix; a reader arrives knowing which part they use. A component with more than a
handful of entries takes `####` headings for its areas.

Lead with the identifier and a concrete verb — add, fix, remove, deprecate,
support, requires, `now`, `no longer`. Name identifiers literally: `Client.Do`,
`--client`, `LIBTMUX_SAFETY`, `tmux://panes/{pane}`. One to three sentences.

State a changed default explicitly, and an incompatibility more explicitly
still, with the way forward in the same bullet:

```markdown
- `show_option`, `set_option` and `show_hooks` now reject `windowId` at pane
  scope rather than ignoring it. Pass `scope: window` to read at window scope.
```

Do not sell a fix: "no longer returns another command's reply", not "improves
reliability". Do not describe effort. Give the old behaviour only where it
explains a break, and mention mechanism only where a caller can observe it — a
refactor that changes nothing observable is not an entry.

Entries land under `## Unreleased`. The maintainer assigns the version when
cutting a release, so nothing here predicts one.

### Markdown

Keep to what a plain CommonMark renderer understands. No renderer-specific
extension, and in particular no GitHub alert blocks — `> [!NOTE]` and the rest
are literal text everywhere but GitHub, which is where the fewest readers of a
Go module are. Tables, badges, and links are fine.

Code blocks are paste-and-run units: one command per block, explanations in
prose above rather than as `#` comments inside, shell commands tagged `console`
with a `$ ` prefix, and a long command split with `\` one flag per line.


## Commits

```
Scope(type[detail]): concise description

why: Explanation of necessity or impact.

what:
- Specific technical changes made
- Focused on a single topic
```

Keep the subject to 50 characters or fewer and wrap body lines at 72. Common
types: **feat**, **fix**, **refactor**, **docs**, **chore**, **test**,
**style**. Never create or push tags; the owner handles releases.
