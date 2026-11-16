# lib

Modules the scripts share, so each exists once. A script imports them after
putting `scripts/lib` on `sys.path`; the tests find it through `pythonpath` in
`pyproject.toml`.

## bounded

Runs external commands under a time limit. When the limit passes, or the script
is interrupted, it kills the command's whole process group, so a test binary
below `go test` or a tool below `go run` cannot outlive the limit.
`subprocess.run(timeout=)` kills only the direct child. A test fails when a
script starts a process without it.

| Name | What it does |
| --- | --- |
| `run(args, timeout=...)` | Like `subprocess.run`, in a new process group. |
| `stream(args, timeout=..., on_line=...)` | Hands each output line over as it arrives; returns the status and the lines. |
| `stop_group(pid)` | Kills the group a bounded command leads; an asyncio caller starts its child with `start_new_session=True` and uses this. |
| `PROBE` | 10 s, for a command that only prints a version. |

`tmux -V` takes 1.0 ms (`hyperfine -N --warmup 3 'tmux -V'`), so `PROBE` is four
orders of magnitude above it.

Each script sets its own limits and names them in its README. They sit above
the slowest CI step that runs the same commands, among the 25 most recent
successful `tests` runs on `master` (measured 2026-11-16):

| Step | Seconds | Run |
| --- | --- | --- |
| Test compatible module coverage (`lane-tests suite`, every module) | 248 | 43731414053 |
| Fuzz escaped option values (30 s of fuzzing) | 52 | 43136332249 |
| Report the exported API difference (`api-diff`, three modules) | 26 | 43136332249 |
| Verify fuzz targets | 14 | 43136332249 |

Rerun the measurement to refresh the table:

```console
$ gh run list \
    --repo libtmux/libtmux-go \
    --workflow tests.yml \
    --branch master \
    --status success \
    --limit 25 \
    --json databaseId \
    --jq '.[].databaseId' \
  | xargs -I{} gh run view {} \
    --repo libtmux/libtmux-go \
    --json databaseId,jobs \
    --jq '.databaseId as $run | .jobs[] | .steps[] | select(.name | test("compatible module coverage|exported API difference|Fuzz|Verify fuzz")) | [((.completedAt | fromdate) - (.startedAt | fromdate)), $run, .name] | @tsv' \
  | sort -rn \
  | awk -F'\t' '!seen[$3]++'
```

Run the tests, which start a command that starts a child and check that both die:

```console
$ uv run --locked --offline pytest scripts/lib
```
