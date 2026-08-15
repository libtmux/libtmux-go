module github.com/libtmux/libtmux-go/mcp

go 1.25.0

require (
	github.com/libtmux/libtmux-go v0.0.0
	github.com/libtmux/libtmux-go/workspace v0.0.0
	// Held below v1.7.0 by choice, not by fault. That version negotiates
	// protocol 2026-07-28, where SEP-2575 makes ServerSession.Log carry only
	// when the client opted in on that very request, through an optional
	// _meta key rather than through logging/setLevel. The SDK's own client
	// does not send it, and is documented not to: the key is optional and
	// SEP-2577 deprecates the logging feature outright, functional only for a
	// deprecation window. So this server's explanation of why a wait ended
	// with nothing would reach such a client only if it asked per call.
	//
	// Raising this means deciding what this server should say, and how, once
	// logging is on its way out of the protocol -- not waiting for a fix.
	github.com/modelcontextprotocol/go-sdk v1.6.1
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/libtmux/libtmux-go => ../

replace github.com/libtmux/libtmux-go/workspace => ../workspace
