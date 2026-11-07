module github.com/libtmux/libtmux-go/mcp

go 1.26.0

require (
	github.com/libtmux/libtmux-go v0.1.2
	github.com/modelcontextprotocol/go-sdk v1.8.0
)

require github.com/google/jsonschema-go v0.4.3

require (
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

// This module ships a command, and go install rejects a module whose go.mod
// carries a replace directive. Local development resolves the modules beside
// it through go.work instead.

// v0.0.1-alpha.1 carried those replace directives, so
// go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@v0.0.1-alpha.1
// refuses to build.
retract v0.0.1-alpha.1
