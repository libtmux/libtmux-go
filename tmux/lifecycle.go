package tmux

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var (
	// ErrInvalidRequest identifies lifecycle options rejected before execution.
	ErrInvalidRequest = errors.New("tmux: invalid lifecycle request")
	// ErrInvalidCommandOutput identifies malformed identity output from tmux.
	ErrInvalidCommandOutput = errors.New("tmux: invalid lifecycle command output")
)

// PaneDirection selects where a tiled pane is created or moved on tmux 3.2a
// or later. Its zero value places the pane below the target.
type PaneDirection uint8

// Supported pane directions.
const (
	// PaneDirectionBelow places the pane below the target.
	PaneDirectionBelow PaneDirection = iota
	// PaneDirectionAbove places the pane above the target.
	PaneDirectionAbove
	// PaneDirectionRight places the pane to the right of the target.
	PaneDirectionRight
	// PaneDirectionLeft places the pane to the left of the target.
	PaneDirectionLeft
)

func copyOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func lifecycleEnvironmentArguments(environment map[string]string) ([]string, error) {
	keys := slices.Sorted(maps.Keys(environment))
	arguments := make([]string, 0, len(keys))
	for _, key := range keys {
		if err := validateEnvironmentName(key); err != nil {
			return nil, err
		}
		if err := validateEnvironmentValue(environment[key]); err != nil {
			return nil, err
		}
		arguments = append(arguments, "-e"+key+"="+environment[key])
	}
	return arguments, nil
}

func invalidLifecycleRequest(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, detail)
}

func expandLifecycleDirectory(path string) (string, error) {
	if path == "" || !strings.HasPrefix(path, "~") {
		return path, nil
	}
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return "", invalidLifecycleRequest("StartDirectory does not support named-user expansion")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: expand StartDirectory: %w", ErrInvalidRequest, err)
	}
	if home == "" {
		return "", invalidLifecycleRequest("current user home directory is empty")
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}

func requireLifecycleSuccess(
	subcommand string,
	result CommandResult,
	err error,
) (CommandResult, error) {
	if err != nil {
		return result, err
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		return result, newCommandError(subcommand, result)
	}
	return result, nil
}

func requireRedactedLifecycleSuccess(
	subcommand string,
	result CommandResult,
	err error,
) (CommandResult, error) {
	if err != nil {
		return result, err
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		return result, newRedactedCommandError(subcommand, result)
	}
	return result, nil
}

func lifecycleStableIdentity(object string, output []string) (string, error) {
	if len(output) != 1 {
		return "", fmt.Errorf(
			"%w: %s command printed %d identity lines",
			ErrInvalidCommandOutput,
			object,
			len(output),
		)
	}
	if err := validateStableTarget(object, output[0]); err != nil {
		return "", fmt.Errorf(
			"%w: %s command printed %q",
			ErrInvalidCommandOutput,
			object,
			output[0],
		)
	}
	return output[0], nil
}
