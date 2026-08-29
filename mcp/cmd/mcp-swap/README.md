# mcp-swap

Points the agent CLIs on this machine at a local build of this server, and puts
them back.

An MCP server cannot be exercised without a client, so the development loop is:
rewrite every client's configuration to run the working tree, try it, restore
what was there. Doing that by hand across half a dozen config files is why it
does not get done.

## Seeing what is configured now

`status` only reads:

```console
$ go -C mcp run ./cmd/mcp-swap status
```

```
claude       uvx --no-config libtmux-mcp==0.1.0a19
cursor       uvx --no-config libtmux-mcp==0.1.0a19
codex        uvx --no-config libtmux-mcp==0.1.0a19
```

## Pointing them at this checkout

Look before you leap — `--dry-run` parses and validates the selected
configurations, prints what would change, and leaves the configurations and
cached server binary unchanged:

```console
$ go -C mcp run ./cmd/mcp-swap use-local --dry-run
```

```console
$ go -C mcp run ./cmd/mcp-swap use-local --mode build
```

Put everything back:

```console
$ go -C mcp run ./cmd/mcp-swap revert
```

## Which build `--mode` selects

| Mode | What the clients run | Why |
| --- | --- | --- |
| `dev` | the working tree | an edit is live on the next call, nothing to rebuild |
| `build` | a compiled binary | one build, then a plain exec |
| `installed` | whatever `libtmux-mcp` is on `PATH` | testing what a user installed |
| `released` | a published version from the module cache | the only mode not involving this checkout |

## Worth knowing

**It starts the chosen build before writing anything.** The server is run once
and asked to report itself, so a configuration that could never have worked is
rejected before it replaces one that did.

**This edits real configuration files.** `status` and `--dry-run` do not. Run
one of those first.
