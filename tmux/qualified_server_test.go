package tmux

import (
	"context"
	"slices"
	"strings"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

const qualifiedRendererPID = "891734"

// qualifiedCommandRunner supplies a version-checked daemon for tests of individual
// renderers. Version-check tests use raw executors, including malformed probes.
type qualifiedCommandRunner struct {
	inner   commandRunner
	version func(context.Context) (Version, error)
}

func (r *qualifiedCommandRunner) Run(ctx context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	arguments := exactArgvSubcommand(request.Arguments)
	fields := coreAdmissionIdentityFields()
	if slices.Equal(arguments, []string{"display-message", "-p", formatTemplate(fields)}) {
		version := "3.7a"
		if r.version != nil {
			current, err := r.version(ctx)
			if err != nil {
				return tmuxcmd.Result{}, err
			}
			version = current.String()
		}
		return tmuxcmd.Result{RawStdout: framedSnapshotRecord(fields, map[string]string{
			"version": version, "pid": qualifiedRendererPID, "start_time": "1789012345",
			"socket_path": "/tmp/libtmux-qualified-renderer.sock",
		})}, nil
	}
	if len(arguments) >= 7 && arguments[0] == "if-shell" && arguments[1] == "-F" &&
		strings.Contains(arguments[2], "#{==:#{pid},"+qualifiedRendererPID+"}") {
		prefix := request.Arguments[:len(request.Arguments)-len(arguments)]
		if len(prefix) != 0 && prefix[0] == "-N" {
			prefix = prefix[1:]
		}
		request.Arguments = append(slices.Clone(prefix), arguments[6:]...)
	}
	return r.inner.Run(ctx, request)
}
