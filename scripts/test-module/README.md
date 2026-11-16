# test-module

Runs one module's `go test`, leaving out the `Example` functions that print a
feature the tmux release lacks. An `Example` has no `t.Skip`, so
`go test -skip` names it instead.

The `tests` workflow runs it for each module and each tmux lane, and
[`matrix`](../matrix) runs it for each build in a matrix. The tmux that decides
is `$LIBTMUX_TMUX`, else `tmux` on `PATH`.

Modules are the directories in the `use` block of `go.work`. A tmux older than
3.7 omits the control examples, and the comparison is by release number, so
`3.5a` is older and `3.10` is not. Flags after the module go to `go test`.

Test the `workspace` module with the race detector:

```console
$ uv run scripts/test-module/run_tests.py workspace -race -count=1
```

The script it replaced is `git show 0c3212b3~1:scripts/test-module.sh`.
The comparison loop and its result (112 of 112 combinations of release, module and flags agree) are in
[issue 172](https://github.com/libtmux/libtmux-go/issues/172#issuecomment-6906832709).

Run its tests:

```console
$ uv run pytest scripts/test-module
```
