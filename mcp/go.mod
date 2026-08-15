module github.com/libtmux/libtmux-go/mcp

go 1.23.0

require (
	github.com/libtmux/libtmux-go v0.0.0
	github.com/libtmux/libtmux-go/workspace v0.0.0
	github.com/modelcontextprotocol/go-sdk v1.2.0
)

require (
	github.com/google/jsonschema-go v0.3.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.30.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/libtmux/libtmux-go => ../

replace github.com/libtmux/libtmux-go/workspace => ../workspace
