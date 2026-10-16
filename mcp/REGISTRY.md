# Publishing to the MCP registry

`server.json` is this server's entry for the [MCP registry], where clients look
for servers by name rather than by import path. It carries no `packages` block:
the registry knows npm, PyPI, NuGet, Cargo, OCI and prebuilt binaries, and `go
install` is none of them, so the entry points at this repository instead.

The name is `io.github.libtmux/tmux-mcp-go`, which the registry ties to the
GitHub organisation of the same name, and the `mcp-name:` comment at the top of
[README.md](README.md) is the marker it looks for. The language rather than the project,
because every server in that namespace drives tmux: `libtmux-mcp` would repeat
what `io.github.libtmux` already says and leave the one distinguishing thing
unsaid.

The publisher is a Go program, so the toolchain this repository already needs
builds it. It installs as `publisher`, though its own help calls it
`mcp-publisher`:

```console
$ go install github.com/modelcontextprotocol/registry/cmd/publisher@latest
```

Homebrew ships it under the second name, as does the release tarball the
[registry quickstart] links:

```console
$ brew install mcp-publisher
```

Checking the entry against the live registry needs no credentials, and is worth
doing before a release rather than after:

```console
$ publisher validate
```

Publishing needs the organisation's:

```console
$ publisher login github
```

```console
$ publisher publish
```

[registry quickstart]: https://modelcontextprotocol.io/registry/quickstart

[MCP registry]: https://registry.modelcontextprotocol.io/
