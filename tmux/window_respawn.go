package tmux

import (
	"context"
	"sort"
)

// RespawnRequest configures restarting a window's or pane's process on tmux
// 3.2a or later. Its zero value reuses the stored command and directory and
// requires the target process to be inactive. Nil pointer fields omit their
// options; nonnil pointers are explicit, including empty strings. There are no
// mutually exclusive fields. Arguments and environment entries are validated
// before execution.
//
// Respawn methods read pointer and map values during argument construction and
// retain none of that caller-owned storage. Callers must not mutate those
// values concurrently; this request does not provide a broader goroutine-safety
// guarantee.
type RespawnRequest struct {
	// Command is omitted when nil so tmux reuses the stored command. A
	// nonnil empty string remains an explicit operand.
	Command *string
	// StartDirectory expands ~ and ~/... for the current user. Nil reuses the
	// stored directory; a nonnil empty string selects the current directory.
	StartDirectory *string
	// Environment is emitted in lexically sorted key order; nil and an empty map
	// both add no environment entries.
	Environment map[string]string
	// Kill terminates an active target process before restarting it.
	Kill bool
}

// Respawn restarts the stable window process through the receiver's exact
// winlink. It does not select the window or promise any client focus. Respawn
// returns a canonical freshly materialized [Window], which may use another
// linked session for the same WindowID. If the command succeeds but refresh
// fails, it returns the receiver with that error; validation or transport
// failure returns a zero Window. A completed command is treated as an error
// only when tmux writes stderr, in which case Respawn returns a [CommandError]
// and a zero Window. A nonzero exit without stderr is ignored and refresh
// proceeds. A transport or context error can be delivery-ambiguous and no
// rollback is attempted.
func (w Window) Respawn(ctx context.Context, request RespawnRequest) (Window, error) {
	target, err := exactWindowTarget(w)
	if err != nil {
		return Window{}, err
	}
	options, err := respawnRequestArguments("respawn-window", request)
	if err != nil {
		return Window{}, err
	}
	arguments := append([]string{"respawn-window", "-t", target}, options...)
	result, err := w.server.literalCmd(ctx, arguments...)
	if err != nil {
		return Window{}, err
	}
	if len(result.Stderr) != 0 {
		return Window{}, newRedactedCommandError("respawn-window", result)
	}
	refreshed, err := w.Refresh(ctx)
	if err != nil {
		return w, err
	}
	return refreshed, nil
}

func respawnRequestArguments(subcommand string, request RespawnRequest) ([]string, error) {
	if request.Command != nil {
		if err := validateServerCommandArgument(subcommand, "Command", *request.Command, true); err != nil {
			return nil, err
		}
	}

	startDirectory := ""
	if request.StartDirectory != nil {
		if err := validateServerCommandArgument(
			subcommand, "StartDirectory", *request.StartDirectory, true,
		); err != nil {
			return nil, err
		}
		startDirectory = *request.StartDirectory
		if startDirectory == "" {
			startDirectory = "."
		}
		var err error
		startDirectory, err = expandLifecycleDirectory(startDirectory)
		if err != nil {
			return nil, err
		}
	}

	keys := make([]string, 0, len(request.Environment))
	for key := range request.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := validateEnvironmentName(key); err != nil {
			return nil, err
		}
		if err := validateEnvironmentValue(request.Environment[key]); err != nil {
			return nil, err
		}
	}

	arguments := make([]string, 0, 2+len(keys))
	if request.Kill {
		arguments = append(arguments, "-k")
	}
	if request.StartDirectory != nil {
		arguments = append(arguments, "-c"+startDirectory)
	}
	for _, key := range keys {
		arguments = append(arguments, "-e"+key+"="+request.Environment[key])
	}
	if request.Command != nil {
		arguments = append(arguments, *request.Command)
	}
	return arguments, nil
}
