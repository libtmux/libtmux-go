# Writing

How this repository writes: `README.md`, doc comments, source comments, error
strings, command-line help, `CHANGELOG.md`, release notes, Markdown, and commit
messages. It governs every surface a reader reaches, and it applies to a
one-line flag description as much as to a release note.

[CONTRIBUTING.md](CONTRIBUTING.md) covers how we work — setting up, the gates,
where a test goes. This covers how we write.

## Voice

Many surfaces, one voice. A doc comment says what a caller may rely on; a
changelog entry says what changed; an error string names what failed; prose
says what happens. All of them are present tense, lead with the thing being
described, and stop. Why it was built
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
| "comprehensive" | name what is covered |
| "production-ready" | state the guarantee |
| "optimized" | give the magnitude |
| "various fixes" | name the components |
| "under the hood" | omit unless observable |
| "please note that" | state the fact |
| "leverage", "utilize" | "use" |
| "delve into" | "read", or omit |
| "best practices" | name the practice |
| "in order to" | "to" |

## README

The README is the door; pkg.go.dev is the manual. It answers what this is, why
you would reach for it, how to install it, what normal use looks like, and what
contract you are buying into — in that order, and then it stops.

Lead with the noun and what it does. A reader decides in one sentence whether
to keep reading, and a paragraph of context spends that sentence on nothing.

Get to code that runs. A library shows the smallest honest integration, and
honest means the errors are handled: an example that elides `if err != nil` is
not an example of Go. Every command has to succeed on a clean machine, because
a reader runs them literally, and so does an agent.

State what a reader would otherwise infer from the source:

- Defaults, including what an empty or zero value means. A default is part of
  the contract.
- Failure behaviour a caller has to plan around: exit status, retries, what is
  overwritten, what is assumed about the network or the filesystem.
- The support policy — which Go versions, which tmux versions — kept honest
  against the `go` directive.

Claims have to be falsifiable. "No runtime dependencies" is one. A performance
claim carries its number and the command that reproduces it, or it does not
appear. Three badges is plenty.

Say what the library is not for. A short list of non-goals is worth more to
someone evaluating it than another list of features, because it is the part
they cannot read off the API surface.

Do not restate pkg.go.dev. The README explains the project and the package
documentation explains the API; two descriptions of one API is one description
that will be wrong.

## Doc comments

The package documentation is the reference, and is written to be read start to
finish rather than searched:

```console
$ go doc github.com/libtmux/libtmux-go
```

Every exported declaration carries one, and every switch a caller can flip is
discoverable there rather than only in the README.

A doc comment begins with the name of the thing it describes — `go doc` and
pkg.go.dev use that to build the summary, so `// Start boots the server` works
and `// This function starts the server` does not. The first sentence stands
alone and ends with a period. Use `//` on every line, never `/* */`.

Document the contract, not the implementation: what a caller may rely on. Zero
values, what `nil` means, ownership, mutation, ordering, whether it blocks,
whether it is safe for concurrent use, which errors are returned, whether
returned memory aliases the input. Those are what a reader infers the API from.
A comment describing the mutex inside a type is not documentation.

Break long comment lines at sentence and clause boundaries rather than at a
column, so an edit shows as one changed line rather than a reflowed paragraph.
Indent a line to render it as a code block.

Doc comments are Go doc comment syntax, documented at
<https://go.dev/doc/comment#syntax>, and it is not Markdown. It has paragraphs,
`# ` headings, lists, indented preformatted blocks, and links. It has no bold,
no italics, and no inline backticks — those arrive at the reader as the
characters they are.

Square brackets are doc links, not emphasis. `[Run]` and `[tmux.Server]`
resolve; a bracketed unexported identifier resolves nowhere and renders as its
own brackets on pkg.go.dev, so write the thing in prose instead.

A package comment lives in `doc.go` and begins `Package <name>`. Its later
paragraphs are where what holds across the whole package goes, rather than
across any one declaration: whether it starts goroutines, whether it keeps
global state, what the zero value of the main type does, who controls
scheduling.

`// Deprecated:` is read by tooling, not only by people. Give it its own
paragraph, name the replacement, and leave the rest of the comment describing
what the declaration still does.

Prefer an `Example` to another paragraph. One ending in an `// Output:` comment
is compiled, run by `go test`, and rendered on pkg.go.dev beside the
declaration, so it cannot describe an API that no longer exists — which no
amount of prose can promise. What enforces the output comment is in
[CONTRIBUTING.md](CONTRIBUTING.md).

pkg.go.dev renders nothing for a module without a recognised `LICENSE`, and it
caches a tagged version permanently. Preview with a local `pkgsite` before
tagging, not after.

## Source comments

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

Minimal usage examples, and param, return, and error lines on public API are
exempt from the loss gate — they serve the caller, not the maintainer. They are
exempt from nothing else. Ceiling: a good man page entry.

Exported doc comments and `Example` functions fall under this exception — an
`Example` function is compiled and run.

## Error messages

An error string names the immediate failure, in lowercase, with no trailing
period. It is a fragment that will be wrapped, not a sentence that will be read
alone.

Wrap with `%w` and let context build outward, so the chain reads from the
outermost operation inward:

```text
start server: load config: decode relay.yaml: unknown field "lisetn"
```

Each frame adds the operation it was attempting, not a restatement of the frame
beneath it. `fmt.Errorf("connect a client: %w", err)` is a frame;
`fmt.Errorf("an error occurred: %w", err)` is not.

What a binary prints at the top is a different surface from the chain beneath
it, and may say more than the chain does — a suggestion, a file and line, the
flag that would fix it. Keep the chain machine-shaped and put the help beside
it.

## Command-line help

`--help` is a surface, not a dump of the flag set. Every flag carries a
lowercase description and an explicit default, including what an empty value
means, which is the part a reader cannot guess:

```text
-socket-name string
    tmux socket name; empty uses tmux's default socket
```

A description that restates the flag name is not a description.

## The changelog

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

## Release notes

The changelog is a ledger; a release page is a briefing. The ledger answers
"does this entry affect me", one line at a time. The briefing answers "should I
take this upgrade, and what will it cost me", so it leads with the two or three
things worth noticing and leaves the rest to the ledger.

Each item runs change, then implication, then whether the reader has to act:

> Routing tables are rebuilt incrementally. Configurations that took hundreds
> of milliseconds to reload should complete faster. No configuration change is
> required.

Say which kind of change each one is, because that is what an upgrade review
sorts by: behaviour, API, performance, operational, migration required, or no
action required. The last is worth writing down — a reader who has to derive it
derives it slowly.

A retracted release gets a `retract` directive in `go.mod` and a line saying so
the same day. A security fix carries its GHSA or CVE identifier and is filed to
the Go vulnerability database, so `govulncheck` reports it to the people
running the old version.

[Go's release notes](https://go.dev/doc/go1.24) are the model.

## Markdown

Keep to what a plain CommonMark renderer understands. No renderer-specific
extension, and in particular no GitHub alert blocks — `> [!NOTE]`,
`> [!WARNING]` and the rest are literal text everywhere but GitHub, which is
where the fewest readers of a Go module are. State the thing in a sentence:
prose that renders everywhere beats a callout that renders once.

Tables, badges, and links are fine. They are CommonMark or near enough, and
every renderer that matters handles them.

Directive comments are exempt from all of the above and must not be reformatted
to suit it. `//go:build`, `//go:generate`, `//nolint:`, `//libtmux:real-tmux`
and `//libtmux:parity` are instructions to a tool, not prose for a reader.

Markdown files in this repository wrap at 80 columns. A pull request or issue
body does not: GitHub renders a single newline as a space in a file and as a
line break in a comment, so a wrapped comment body arrives as ragged stubs.

## Code blocks

Code blocks are paste-and-run units: pasting one block runs exactly one
intended action. Executed examples are exempt — the test suite runs them,
nobody pastes them.

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

## Commits

```
Scope(type[detail]): concise description

why: Explanation of necessity or impact.

what:
- Specific technical changes made
- Focused on a single topic
```

Keep the subject to 50 characters or fewer, excluding any trailing `(#NN)` pull
request reference, and wrap body lines at 72. Separate the `why:` and `what:`
blocks with a blank line.

The subject completes "this change modifies X to ___", which is why it is a
verb phrase rather than a noun. "Add a literal flag" completes it; "Literal
flag changes" does not.

The body carries the reasoning that [Source comments](#source-comments) keeps
out of the code: why this shape and not the obvious one, what was ruled out,
which failure prompted it. It is what a maintainer reaches through `git blame`
years later, and the diff never contains it.

One logical change per commit. A branch that will be reviewed carries no `wip`
and no "address review" — those name states of the work, not changes to it.

Common types:

- **feat**: New features or enhancements
- **fix**: Bug fixes
- **refactor**: Code restructuring without functional change
- **docs**: Documentation updates
- **chore**: Maintenance (dependencies, tooling, config)
- **test**: Test-related updates
- **style**: Code style and formatting
- **go(deps)**: Dependencies
- **go(deps[dev])**: Dev dependencies
- **ai(rules[AGENTS])**: AI rule updates

Example:

```
Pane(feat[send_keys]): Add support for a literal flag

why: Send characters without tmux interpreting them.

what:
- Add a Literal field to SendKeysRequest
- Pass -l when it is set
```

Use a heredoc so the formatting survives the shell:

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

Never create tags. Never push tags. The owner handles tagging and tag pushes,
because a tag triggers the publish workflow.

A release commit subject is plain and short: `Tag v<version>`. The detailed
why and what go in the body. Do not use the `Scope(type[detail]):` format for a
release — it buries the lede.

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
  a pull request description. The diff and the commit message already carry it.
- **Branch-internal narrative.** Do not mention intermediate states, abandoned
  approaches, or "no longer" behaviour unless users of a published release
  actually experienced the old state.
- **Low-value scaffolding.** No ownerless TODOs, unused future-proofing, debug
  artefacts, or defensive wrappers around failure modes nothing can reach.
- **Prose inflation.** The diction table under [Voice](#voice) governs; replace
  an inflated word with a concrete description of behaviour, constraints, or
  trade-offs.
- **Coded labels.** Write rules and findings as plain imperatives. No `[R1]`,
  `Option B`, or any index a reader has to decode.

Preserve the "why". Never delete a comment documenting an invariant, a protocol
constraint, a platform quirk, or an upstream workaround — those are the facts
[Source comments](#source-comments) keeps, and every other comment is judged by
it.
