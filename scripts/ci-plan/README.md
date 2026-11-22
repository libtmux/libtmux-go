# ci-plan

Plans the tmux lanes of the `tests` workflow from
[`.github/ci-matrix.txt`](../../.github/ci-matrix.txt), and checks that every
job the plan put in scope passed.

The `plan` job runs it first and every tmux lane takes its matrix from the
output. The `required` job runs `verify` last, so a job the plan left out may be
skipped and any other must have succeeded.

| Event | Lanes |
| --- | --- |
| Push or pull request | The first and last supported tmux |
| Tag push, or dispatch with `scope=full` | The whole table, tmux master, and the refused releases |
| Dispatch with `scope=custom` | The `tmux-ref` list: versions, tags, branches or commits |

A dispatch reads `INPUT_SCOPE`, `INPUT_TMUX_REF`, `INPUT_GO_VERSION`, `INPUT_OS`
and `INPUT_TMATE`. A push or pull request ignores them.

Plan a pull request from the checked-in manifest:

```console
$ GITHUB_EVENT_NAME=pull_request uv run scripts/ci-plan/ci_plan.py
```

Check a `needs` result the way the `required` job does:

```console
$ NEEDS_JSON='{"plan":{"result":"success"}}' uv run scripts/ci-plan/ci_plan.py verify
```

Run its tests:

```console
$ uv run --project scripts pytest scripts/ci-plan
```
