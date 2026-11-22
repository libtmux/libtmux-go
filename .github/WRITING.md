# Writing

This file adds Go, `go doc`, and multi-module rules to the shared organization
writing guide in
[`libtmux/.github/WRITING.md`](https://github.com/libtmux/.github/blob/main/WRITING.md),
which governs voice, plain CommonMark, `console` code blocks,
[README shape](https://github.com/libtmux/central/blob/master/docs/decisions/0005-readme-shape.md),
the three-gate source-comment ceiling, slop prevention, and commit and
pull-request format. [CONTRIBUTING.md](CONTRIBUTING.md) governs development
workflow.

## README

The README explains the project; [pkg.go.dev](https://pkg.go.dev/) explains the
API. Follow the shared README shape, with these Go conventions:

- A package page keeps one order: install command, release status, `Contents`,
  task sections, and `See also` (whose first row is the reference), with one
  paste-and-run example and what it prints.
- Head a call section with that call in a code span linked to its `libtmux.org`
  reference page, then a colon and a short title:

```text
### [`Session.Rename`][tmux-session-rename]: a change returns a new record
```

- Functions and types carry their package (`tmux.NewServer`); methods and fields
  carry their type (`Session.Rename`). Expression pages may head a section with
  the expression (`tmux.PaneActiveIs(true)`). Link an API name in prose the
  first time a section uses it.
- State actionable floors (`Go 1.26 or newer`, `tmux 3.3a or newer`) and keep
  packages in a table followed by one `###` heading per package linking its
  source directory, reference, and changelog.
- Go blocks between `docs:` markers are generated from compiled, CI-executed
  files. Edit the `.go` source and run `go generate ./...` rather than editing
  Markdown fences by hand; see
  [CONTRIBUTING.md](CONTRIBUTING.md#generated-documentation). A README holds no
  Go outside them.

## Doc comments

Every exported declaration has a comment. Begin with its name, make the first
sentence stand alone, and end it with a period. Use `//`, not block comments.

Document what a caller may rely on: zero and nil behavior, ownership, mutation,
ordering, blocking, concurrency, errors, and memory aliasing. Omit internal
mechanism unless it affects that contract. Package documentation should orient
a new caller and explain package-wide rules; it is not a second README or an
exhaustive tour of every feature.

Use Go doc syntax, not Markdown. It supports paragraphs, `# ` headings, lists,
indented code, and links. It does not support bold, italics, or inline code.
Bracket only resolvable identifiers such as `[Run]` and `[tmux.Server]`.

Break lines at sentence or clause boundaries to avoid paragraph-wide reflow.
Put package comments in `doc.go`, beginning with `Package <name>`.

Give `// Deprecated:` its own paragraph and name the replacement. Prefer a
compiled `Example` to another usage paragraph; examples with `// Output:` run
under `go test` and render beside the declaration.

## Source comments

Apply the three-gate source-comment policy (`Loss`, standard-library ceiling,
`Upkeep`) in
[`libtmux/.github/WRITING.md`](https://github.com/libtmux/.github/blob/main/WRITING.md#source-comments).

Tool directives are exempt and must not be reformatted: `//go:build`,
`//go:generate`, and `//nolint:`. Generated-doc and example markers are also
exempt: `// docs:<name>`, `// docs:end`, and `// Output:`.

## Error messages

An error string names the immediate failure in lowercase, without a trailing
period. Wrap with `%w`; each frame adds the attempted operation:

```text
start server: load config: decode relay.yaml: unknown field "lisetn"
```

Do not add empty frames such as `an error occurred`. A binary may prefix the
chain with its name and add an actionable suggestion, file, line, or flag.

## Command-line help

Every flag has a lowercase description and an explicit default, including what
an empty value means. Do not restate the flag name.

```text
-socket-name string
    tmux socket name; empty uses tmux's default socket
```

## Changelog

The changelog is a scanned ledger. Put one observable change in each bullet,
grouped by affected component rather than change type:

```markdown
### mcp

- `run_shell_command` no longer returns its own sourcing line as output.
- Add `onError` to the three batch tools. It chooses whether a batch stops at
  the first failure; stopping remains the default.
```

Lead with a literal identifier and a concrete verb: add, fix, remove,
deprecate, support, require, `now`, or `no longer`. Use one to three sentences.
State changed defaults and incompatibilities explicitly, with the migration in
the same bullet. Omit invisible refactors; add entries under `## Unreleased` and
let the maintainer assign versions.

Each published module keeps its own `CHANGELOG.md` beside its `go.mod`, and an
entry goes in the file of the module it changes: `workspace/CHANGELOG.md` for
`workspace`, `mcp/CHANGELOG.md` for `mcp`, and the root `CHANGELOG.md` for the
tmux module (`tmux`, `tmux/tmuxtest`, and `tmuxq`). A change that spans modules,
such as documentation, CI, release tooling, `examples`, or `internal/tools`,
goes in the root file. The modules are tagged together, so a release heading
names the tags of its release, and a release with nothing for a module says
``No entries for `workspace`.`` under it.

## Release notes

Release notes brief an upgrader; they do not repeat the ledger. Lead with the
two or three changes worth noticing, stating each change, its implication, and
any required action (or when none is required).

A retracted release gets a `retract` directive in `go.mod` and a same-day note.
A security fix names its GHSA or CVE and is filed with the Go vulnerability
database. [Go's release notes](https://go.dev/doc/go1.24) are the model.

## Commits

Follow `Scope(type[detail]): concise description` (50-character subject,
72-character body lines, `why:` and `what:` separated by a blank line) from
[`libtmux/.github/WRITING.md`](https://github.com/libtmux/.github/blob/main/WRITING.md#commits-and-pull-requests).
Common Go scopes and types include `Pane(feat[send_keys])`, `go(deps)`,
`go(deps[dev])`, and `ai(rules[AGENTS])`. Release commits use `Tag v<version>`
without the scoped prefix; never create or push tags except as the maintainer.
