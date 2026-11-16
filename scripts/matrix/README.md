# matrix

Runs every module's tests against each supported tmux release, one build at a
time. A maintainer runs it before a release; CI covers the same releases lane by
lane.

It needs a directory with `<version>/bin/tmux` for each release, at
`$LIBTMUX_TMUX_MATRIX` or `~/.local/share/libtmux-tmux-matrix`. Without one it
says so and exits 0, so a checkout with no builds still passes its other gates.

The releases are the supported entries of
[`.github/ci-matrix.txt`](../../.github/ci-matrix.txt), each run through
[`test-module`](../test-module).

| Variable | Effect |
| --- | --- |
| `LIBTMUX_MATRIX_REQUIRED=1` | A missing directory or build fails, and every supported release runs |
| `LIBTMUX_MATRIX_VERSIONS` | Space-separated releases to run |
| `LIBTMUX_MATRIX_MODULES` | Space-separated modules to run |

The sweep never reaches the tmux you use: it runs without `TMUX`, `TMUX_PANE`
and `LIBTMUX_SOCKET*`, in a socket directory made for the run and removed after
it.

A socket left in that directory keeps it and fails the sweep.

Run every module against every supported build found in the matrix:

```console
$ uv run scripts/matrix/matrix.py
```

Set `LIBTMUX_MATRIX_REQUIRED=1` in the environment of that command for a
release gate.

Run its tests:

```console
$ uv run pytest scripts/matrix
```
