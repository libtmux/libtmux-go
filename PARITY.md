# Python parity contract

Feature parity means that every supported public Python behavior has either a
Go equivalent or an explicit language-level translation. Completion is proved
from a committed symbol manifest and behavior tests, not from file presence.

## Translation map

| Python source | Required behavior | Go home | Primary proof |
| --- | --- | --- | --- |
| `server.py` | Connection configuration, raw commands, lifecycle, sessions, clients, global options, environment, and server commands | `server.go` | Real-tmux server tests |
| `session.py` | Window traversal, attach/detach, lifecycle, rename, selection, movement, options, hooks, and environment | `model.go`, `lifecycle.go`, `session_navigation.go`, and `relations.go` | Real-tmux session tests |
| `window.py` | Pane traversal, linked-window resolution, splitting, layout, selection, movement, options, and hooks | `model.go`, `lifecycle.go`, `window_*.go`, and `relations.go` | Real-tmux window and resolution tests |
| `pane.py` | Capture, input, resize, process commands, popups, pipes, modes, options, and navigation | `model.go`, `pane_*.go`, and `window_pane_topology.go` | Real-tmux pane tests |
| `client.py` | Client state, live attached hierarchy, detach, suspension, refresh, and flags | `model.go`, `server_clients.go`, and `client_attachment.go` | Real-tmux client tests |
| `common.py` | Command results, targets, refresh, equality, resolution, and format decoding | `command.go`, `target.go`, `snapshot.go` | Unit and real-tmux tests |
| `formats.py` | Version-aware tmux token selection and typed parsing | Generated format metadata and `format.go` | Golden output and compatibility tests |
| `neo.py` | Typed fresh server/session/window/pane/client state | Canonical models and `Snapshot` | Snapshot graph tests |
| `options.py` | Scoped option discovery, typed access, mutation, unset, and sparse arrays | `option_generated.go`, `option_runtime.go`, and `complex_options.go` | Real-tmux option tests |
| `hooks.py` | Scoped hook discovery, arrays, mutation, unset, and command preservation | `option_generated.go`, `hook_runtime.go`, and `control_notification.go` | Real-tmux hook tests |
| `common.EnvironmentMixin` | Set, unset, remove, show, and update-environment semantics | `environment.go` | Real-tmux environment tests |
| `_internal/env.py` and `from_env` methods | Pure server discovery plus targeted current-pane, containing-window, and session discovery | `from_env.go` | Parsing and real-tmux discovery tests |
| `_internal/query_list.py` | Filtering, first, cardinality, composition, relations, and immutable query results | Generated filters and `tmuxq` | Generated contract and differential tests |
| `search_*` and filtered buffer APIs | Scoped pass-through tmux `-f` filtering | `TmuxFilter` search methods | Argv and real-tmux search tests |
| `_internal/sparse_array.py` | Indexed option and hook values with holes | `SparseArray[T]`; typed option replacement reports confirmed partial progress with `SetArrayResult` | Unit and real-tmux tests |
| `_internal/control_mode.py` | Attached `tmux -C` test client startup, registration, contextual stdout reads, identity, and teardown | `tmuxtest.ControlMode` | Real-tmux fixture tests |
| `_internal/dataclasses.py` and `_internal/types.py` | Concrete command, version, option, hook, and model value shapes | Focused concrete Go types | Compile-time and unit tests |
| `exc.py` | Target, command, version, control, and cardinality error classification | Concrete errors beside their owning APIs and `tmuxq` sentinels | `errors.Is` and `errors.As` tests |
| `constants.py` and `_internal/constants.py` | Public flags, modes, keys, and stable tmux literals | Typed constants near owning APIs | Compile-time and golden tests |
| `_compat.py` and vendored version support | tmux version parsing, suffix ordering, and feature gates | `version.go` | Cross-version table tests |
| `pytest_plugin.py` and `test/` | Isolated real-tmux fixtures, environment checks, retry, random names, temporary values, and cleanup | `tmuxtest` | Self-tests plus race stress |
| `__init__.py` and `__about__.py` | Package identity, supported public exports, and version metadata | Package docs, exports, and Go build metadata translation | Downstream compile and build-info tests |

## Language translations

- Python keyword arguments become request structs when a command has multiple
  optional fields. Small unambiguous operations use typed positional values.
- Python properties that can execute tmux become context-first methods.
  Snapshot-only fields remain direct values or nonblocking accessors.
- Python iterables and query-list operators become fresh slices and explicit
  `iter.Seq` helpers.
- Python control-mode fixture behavior remains a context-first, blocking
  `tmuxtest` helper with retryable contextual teardown. A future public protocol
  transport is outside the parity claim.
- Python exceptions become concrete errors supporting `errors.Is` or
  `errors.As`. Raw command nonzero exits remain result data.
- Python warnings become concrete values delivered to an optional warning
  handler; default behavior remains non-fatal and silent.
- Python magic methods become named Go methods or ordinary value semantics.
- Python `lower_snake_case` names become Go exported names under one
  module-wide convention, implemented once in `internal/goname` and shared by
  the format generator and the omission guard. Each underscore-separated word
  is capitalized, known initialisms take their fully capitalized Go form, and
  unseparated compounds are split: `get_by_id` becomes `GetByID`,
  `client_utf8` becomes `ClientUTF8`, and `client_readonly` becomes
  `ClientReadOnly`. A Go name that differs from its Python spelling only by
  casing, initialism form, compound split, or a dropped receiver prefix is a
  translation, not a parity gap.
- The separate experimental `neo` namespace is not ported. Its typed fresh-data
  behavior is part of the canonical snapshot model.

Client attachment properties become context-first live methods. One internal
refresh may resolve the attached session, window, and pane together; detached
clients return comma-ok false values.

Deprecated Python names with supported behavior remain represented unless they
are only Python syntax or a second name for an identical Go operation. APIs
removed before the Go module's baseline remain inventory rows under the closed
deprecated-Python omission and do not create Go compatibility symbols.

A closed omission reserves a Go name as well as declaring no Go destination.
The manifest gate rejects an omission entry that declares Go symbols, and the
omission guard converts each omitted Python name with the naming convention
above and rejects that spelling as a production Go symbol. The two checks cover
different mistakes: the first stops an omitted API from acquiring a mapping,
the second stops it from returning unmapped under the name a Go author would
reach for.

The reserved spelling is receiver-scoped, so an unrelated operation on another
receiver stays legal: omitting `libtmux.session.Session.kill_session` reserves
`Session.KillSession` and never implicates the module's own
`Server.KillSession`. Dunder members reserve nothing, having no Go spelling. A
Go-native API that genuinely needs a reserved spelling records the collision
and its reason in the guard's exception map rather than weakening the rule.

## Checkable inventory

Before a component is marked complete, the parity manifest must contain entries
for each public class, method, property, function, exception, constant, option
field, hook field, format field, and behavior-changing parameter branch in its
Python sources. Each entry records:

- the Python symbol and supported-version range;
- the Go symbol or named language translation;
- whether it is hand-written or generated;
- its unit, real-tmux, compatibility, or compile-time evidence;
- its tmux version policy and warning behavior;
- cross-scope projection where applicable;
- any deliberate semantic difference, including standardized list leniency.

Imports and private implementation details are not Go API obligations. A
separate semantic source-digest ledger covers public modules and explicitly
selected internal files, detecting changes without requiring invented Go
symbols or proofs. Private behavior becomes a mapping obligation only when
`selected_internal` names it explicitly; vendored semantic dependencies use
whole-file snapshots.

The manifest has no free-form exclusion status. Every entry is mapped directly
or uses one member of a closed translation set: context-first property,
keyword-argument request, iterator helper, blocking control fixture, concrete Go
error, named magic-method behavior, deprecated alias, deprecated Python
omission, warning handler, query-addendum replacement, package build metadata,
Python package-metadata omission, or `neo` consolidation. Closed omissions have
no Go destination; every other translation requires one. All entries require
evidence.

The manifest gate rejects missing Go symbols, duplicate mappings, unproved
entries, and unknown translation codes. Generated surface counts are evidence
in the manifest, not promises embedded in user documentation.

## Semantic invariants

- List accessors are empty on tmux failures by default; strict errors are opt-in,
  and each Python behavior difference is named in the manifest.
- Context cancellation is never mistaken for an empty server.
- Session, window, and pane targets retain explicit parent relationships.
- Linked windows may appear more than once and keep distinct winlink state.
- Snapshot duplicate lookup reports ambiguity; live ID resolution lets tmux
  select the canonical winlink.
- Format availability follows the tmux version that produced the snapshot, and
  every token that may expand empty remains optional.
- Optional snapshot tokens use generated comma-ok accessors and never shared
  pointers.
- Cross-scope active and attached projections are retained in materialized rows.
- Unknown tmux options and hooks remain accessible through raw APIs.
- Filter JSON contains only stable criteria fields; compiled state is private.
- Raw `TmuxFilter` searches remain live and distinct from local criteria.
- `Where` and snapshot accessors never alias their input slices.
- Multi-command snapshots are observational views, not atomic transactions.
- No snapshot filter, equality check, or string formatting operation executes
  tmux.
