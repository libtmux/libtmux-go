# api-diff

Reports what each published module exports now compared with its newest tag, as
Markdown in the `api-diff` job's step summary. It runs `gorelease` in the root,
`workspace` and `mcp` modules. A module with no tag says so.

It exits 0 whatever `gorelease` reports. A `0.1.N` module breaks its API on
purpose, and a gate that forbids that would be turned off.

The newest tag is the highest semantic version with the module's prefix, so a
pre-release ranks below its release and `alpha.10` above `alpha.9`.
`GORELEASE` names the package and version to run, and the workflow pins it.
Each `gorelease` run stops, with the compiler and binary it started, after 120 s
(`RUN_TIMEOUT`). The slowest CI run of the whole step, which covers three
modules, took 26 s ([measurement](../lib/README.md)).

Print the report for the checkout:

```console
$ GORELEASE=golang.org/x/exp/cmd/gorelease@latest uv run scripts/api-diff/api_diff.py
```

Run its tests:

```console
$ uv run pytest scripts/api-diff
```
