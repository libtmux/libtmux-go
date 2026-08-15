module github.com/libtmux/libtmux-go/mcp

go 1.25.0

require (
	github.com/libtmux/libtmux-go v0.0.0
	github.com/libtmux/libtmux-go/workspace v0.0.0
	// Held below v1.7.0. Under the 2026-07-28 protocol that version negotiates,
	// the server overwrites the session log level with the one carried in each
	// request's _meta, and the SDK's own client never sends that field. The
	// level a client sets with logging/setLevel is therefore wiped by its next
	// request, and every ServerSession.Log call after it is dropped without an
	// error. Raise this only once a client sends the level it asked for.
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
