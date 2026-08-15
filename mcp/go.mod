module github.com/tmux-python/libtmux/golang/mcp

go 1.23.0

require (
	github.com/modelcontextprotocol/go-sdk v1.2.0
	github.com/tmux-python/libtmux/golang v0.0.0
	github.com/tmux-python/libtmux/golang/workspace v0.0.0
)

require (
	github.com/google/jsonschema-go v0.3.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/tmux-python/libtmux/golang => ../

replace github.com/tmux-python/libtmux/golang/workspace => ../workspace
