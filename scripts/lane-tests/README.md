# lane-tests

Runs the test steps of a tmux lane in the `tests` workflow. The workflow calls
one command per step; the version tables and the checks live here.

| Command | What it runs |
| --- | --- |
| `suite --prefix P --expect GLOB` | Every module's full suite, after checking that the tmux in `P` reports a release matching `GLOB` |
| `exact --prefix P --version V` | The tests that skip on every release but `V` |
| `older-daemon --prefix P --older-prefix O` | The tests that start a 3.3a daemon from `O` beside the client in `P` |
| `rejected --version V --floor-prefix F --rejected-prefix R [--newer-prefix N]` | The tests that a release below the floor is refused, on the 3.3a build in `F` |

A prefix is an install directory with `bin/tmux`, the `prefix` output of the
[build-tmux action](https://github.com/libtmux/.github/tree/30f81842b1e639ea4e25f93f1f2543504178e228/actions/build-tmux).
`TMUX_TMPDIR` names the socket directory, which is created.

The last three run named tests and fail unless each prints `--- PASS:`. A test
that skips on every other release would otherwise let a lane that built the
wrong tmux pass without it. Each module run goes through
[`test-module`](../test-module) and is stopped after 300 s (`RUN_TIMEOUT`),
together with every process it started, such as the test binary below `go test`.
The slowest CI run of the `suite` step, which runs every module, took 248 s
([measurement](../lib/README.md)). `tmux -V` has the 10 s `PROBE` limit.

Run the exact-release tests for a tmux 3.7 built under `/opt/tmux-3.7`:

```console
$ TMUX_TMPDIR=/tmp/libtmux-go-ci uv run scripts/lane-tests/lane_tests.py exact \
    --prefix /opt/tmux-3.7 \
    --version 3.7
```

A test that the tables name must exist: the tests fail when a name is not a
`Test` function in the repository. Run them:

```console
$ uv run pytest scripts/lane-tests
```
