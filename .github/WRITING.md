# Writing

This file governs user-facing prose, comments, errors, help, changelogs,
release notes, Markdown, and commit messages. [CONTRIBUTING.md](CONTRIBUTING.md)
governs development workflow.

## Voice

Use present tense. Lead with the subject and observable behavior, then stop.
Keep decision history and rejected alternatives in the commit message.

| Instead of | Prefer |
| --- | --- |
| "We added..." | "`Foo` now supports..." |
| "powerful", "seamless" | state the capability |
| "easy", "simple" | omit |
| "robust" | name the handled failure |
| "comprehensive" | name what is covered |
| "optimized" | give the measured change |
| "various fixes" | name the affected components |
| "please note", "under the hood" | state the observable fact |
| "leverage", "utilize" | "use" |
| "best practices" | name the practice |

Delete introductions that only announce the paragraph. Do not use promotional
claims, conversational filler, unexplained emoji, or tool metadata.

## README

The README explains the project; pkg.go.dev explains the API. Cover, in order:

1. What the project does and why someone would use it.
2. Installation.
3. The smallest honest example, including error handling.
4. Defaults, failure behavior, and support policy.
5. Important non-goals.

Commands must run literally on a clean machine. Claims must be falsifiable. A
performance claim includes a number and its reproduction command. Do not
duplicate package documentation or list features already clear from the API.

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

Keep a source comment only when all three answers are yes:

- **Loss:** Would deleting it make a maintainer rediscover an invariant,
  constraint, failure mode, or non-obvious intent?
- **Elite:** Would a mature standard-library project state it this directly and
  at this length, without arguing or narrating?
- **Upkeep:** Will it remain true without hand-syncing values owned by this
  code, such as counts, offsets, or paths?

Aim for one or two lines. Keep upstream quirks, protocol and compatibility
constraints, ordering and concurrency requirements, performance trade-offs,
and explanations for code that looks wrong but is required. A short algorithm
sketch is useful when local operations do not reveal the whole.

Delete comments that:

- translate the next lines into English;
- repeat names, types, defaults, or control flow;
- justify or apologize for a choice instead of stating its constraint;
- speculate about future work or preserve version-control history;
- cite tickets, line numbers, file counts, bare commits, or local paths;
- say "currently", "for now", or another fact likely to rot.

Public doc comments and examples may include minimal usage, parameter, return,
and error guidance. They still must be concise and maintainable. Preserve every
comment that records an invariant, protocol constraint, platform quirk, or
upstream workaround.

Tool directives are exempt and must not be reformatted:
`//go:build`, `//go:generate`, `//nolint:`, `//libtmux:real-tmux`, and
`//libtmux:parity`. Generated-doc and example markers are also exempt:
`// docs:<name>`, `// docs:end`, and `// Output:`.

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

- `run_command` no longer returns its own sourcing line as output.
- Add `onError` to the three batch tools. It chooses whether a batch stops at
  the first failure; stopping remains the default.
```

Lead with a literal identifier and a concrete verb: add, fix, remove,
deprecate, support, require, `now`, or `no longer`. Use one to three sentences.
State changed defaults and incompatibilities explicitly, with the migration in
the same bullet.

Do not sell a fix, describe effort, or include an invisible refactor. Mention
old behavior only when it explains a published break, and mechanism only when
callers can observe it. Add entries under `## Unreleased`; the maintainer
assigns versions.

## Release notes

Release notes brief an upgrader; they do not repeat the ledger. Lead with the
two or three changes worth noticing. For each, state the change, its
implication, and any required action. Identify API, behavior, performance,
operational, and migration effects; say when no action is required.

A retracted release gets a `retract` directive in `go.mod` and a same-day note.
A security fix names its GHSA or CVE and is filed with the Go vulnerability
database. [Go's release notes](https://go.dev/doc/go1.24) are the model.

## Markdown

Use plain CommonMark. Do not use GitHub alert blocks such as `> [!NOTE]`; state
the fact in prose. Tables, badges, and links are allowed.

Wrap repository Markdown at 80 columns. Do not hard-wrap pull-request or issue
bodies because GitHub renders those newlines as line breaks.

## Code blocks

A code block is one paste-and-run action. Executed examples are exempt.

- Put one command in each block. An explicit `&&`, `;`, or `\` continuation is
  one command.
- Put explanations in prose above the block, not shell comments inside it.
- Give each command in a menu its own block and prose lead-in.
- Mark shell commands as `console` and prefix them with `$ `.
- Split long commands with `\`, one flag or flag-value pair per line, with
  positional arguments last.

Show the last ten commits as a graph:

```console
$ git log \
    --max-count=10 \
    --graph \
    --oneline
```

Go blocks between `docs:` markers are generated. Edit their source program,
not the Markdown block; [the examples README](../examples/README.md) identifies
them. Unmarked blocks are handwritten.

## Commits

```text
Scope(type[detail]): concise description

why: Explanation of necessity or impact.

what:
- Specific technical changes made
- Focused on a single topic
```

Keep subjects to 50 characters, excluding a trailing `(#NN)`, and body lines
to 72. Use an imperative verb phrase. Separate `why:` and `what:` with a blank
line. Put rationale and rejected alternatives here rather than in source
comments.

Each commit contains one logical change. Do not use `wip` or "address review"
as subjects. Common subject forms include `feat`, `fix`, `refactor`, `docs`,
`chore`, `test`, `style`, `go(deps)`, `go(deps[dev])`, and
`ai(rules[AGENTS])`.

```text
Pane(feat[send_keys]): Add a literal flag

why: Send characters without tmux interpreting them.

what:
- Add a Literal field to SendKeysRequest
- Pass -l when it is set
```

Use a heredoc to preserve formatting:

```console
$ git commit -m "$(cat <<'EOF'
Scope(feat[detail]): Concise description

why: Explanation of the change.

what:
- First change
- Second change
EOF
)"
```

### Release commits

Never create or push tags; a tag triggers publication and belongs to the owner.
Use `Tag v<version>` as the release commit subject, without the normal scoped
format.

## Review for slop

Remove AI signatures, filler, diff narration, branch-internal history, ownerless
TODOs, unused future-proofing, debug artifacts, and coded labels such as `[R1]`
or `Option B`. Keep only concrete behavior, constraints, and trade-offs.
