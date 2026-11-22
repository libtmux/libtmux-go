# fuzz-targets

Fuzzes one target of a Go package, and fails first when the target does not
exist. The `fuzz` job of the `tests` workflow runs it once for each target.

`go test -fuzz` exits zero with only a warning when its pattern matches no
target, so a renamed target would turn the job into a step that passes without
fuzzing. The script runs `go test PACKAGE -list '^TARGET$'` and requires the
first line it prints to be the target, then runs `go test PACKAGE -run '^$' -fuzz
'^TARGET$' -fuzztime Ns`. It runs from the repository root and echoes the
engine's output as it arrives.

Fuzz the escaped option value target for 30 seconds:

```console
$ uv run scripts/fuzz-targets/fuzz_targets.py \
    ./tmux \
    FuzzDecodeArgsEscapedValueRoundTrip \
    --seconds 30
```

`go test -list` stops after 120 s (`LIST_TIMEOUT`) and a fuzz run after its
fuzzing time plus 120 s (`FUZZ_SLACK`), with everything either started. The
slowest CI run of a 30 s fuzz step took 52 s, 22 s over its fuzzing time, and the
slowest list step took 14 s ([measurement](../lib/README.md)).

Run its tests:

```console
$ uv run --project scripts pytest scripts/fuzz-targets
```
