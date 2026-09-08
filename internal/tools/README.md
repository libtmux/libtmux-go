# Developer tools

This module keeps repository-only Go commands out of the modules released to
library and MCP users. It is part of `go.work` and the normal test and lint
gates, but it has no release tag or install path.

| Command | Purpose |
| --- | --- |
| [`mcp-swap`](mcp-swap/) | Point supported agent clients at one MCP build and restore their prior configuration. |
