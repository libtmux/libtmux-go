module github.com/libtmux/libtmux-go/mcp

go 1.25.0

require (
	github.com/libtmux/libtmux-go v0.0.1-alpha.1
	github.com/libtmux/libtmux-go/workspace v0.0.1-alpha.1
	// Held below v1.7.0 until modelcontextprotocol/go-sdk#1168 is released.
	//
	// Under protocol 2026-07-28 a log message carries only when the client
	// opted in on that very request (SEP-2575), which is deliberate. How
	// v1.7.0 carries that opt-in is not: it writes the level into shared
	// session state, so two requests in flight at once clear or inherit each
	// other's threshold. Both directions are wrong -- a caller that asked
	// for logs loses them, and a caller that did not ask receives them,
	// which the spec requires be dropped. Concurrent tool calls are this
	// server's ordinary case; batch makes them its explicit one.
	//
	// There is nothing to work around it with: a server cannot decline a
	// protocol version, and the level cannot be set from outside the SDK.
	github.com/modelcontextprotocol/go-sdk v1.7.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// This module ships a command, and go install rejects a module whose go.mod
// carries a replace directive. Local development resolves the modules beside
// it through go.work instead.

// v0.0.1-alpha.1 carried those replace directives, so
// go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@v0.0.1-alpha.1
// refuses to build.
retract v0.0.1-alpha.1
