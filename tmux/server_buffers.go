package tmux

import (
	"context"
	"slices"
	"strings"
)

// TmuxFilter is a raw tmux -f format expression. It is distinct from the
// generated local filter structs and is evaluated only by tmux. A nil filter
// omits -f; a nonnil empty filter sends an explicit empty expression.
//
// The expression is tmux's own, so it is written the way tmux's FORMATS
// section describes rather than built from Go values:
//
//	active := tmux.TmuxFilter("#{==:#{window_active},1}")
//	windows, err := session.SearchWindows(ctx, &active)
//
// tmux evaluates this while listing. Generated model filters run in Go after
// materialization.
//
//revive:disable-next-line:exported TmuxFilter preserves tmux vocabulary beside local model filters.
type TmuxFilter string

// SetBufferRequest configures storing data in a tmux paste buffer. Its zero
// value writes an empty most-recent buffer; nil Name selects that buffer while
// a pointer to an empty name is explicit.
type SetBufferRequest struct {
	// Data is the exact buffer data to store or append.
	Data string
	// Name selects a named buffer, or nil for tmux's most-recent buffer.
	Name *string
	// Append appends Data instead of replacing the selected buffer.
	Append bool
}

// SaveBufferRequest configures writing a tmux paste buffer to a file. Its zero
// value is invalid because Path is required; nil Name selects the most-recent
// buffer and a pointer to an empty name is explicit.
type SaveBufferRequest struct {
	// Path is the required output file path.
	Path string
	// Name selects a named buffer, or nil for tmux's most-recent buffer.
	Name *string
	// Append appends buffer data to Path instead of replacing its contents.
	Append bool
}

// LoadBufferRequest configures loading a file into a tmux paste buffer. Its
// zero value is invalid because Path is required; nil Name lets tmux allocate
// a buffer and a pointer to an empty name is explicit.
type LoadBufferRequest struct {
	// Path is the required input file path.
	Path string
	// Name selects the destination buffer, or nil for tmux's allocation behavior.
	Name *string
}

// ListBuffersRequest configures tmux's live paste-buffer listing. Its zero
// value requests tmux's default output; nil Format and Filter omit their flags,
// whereas pointers to empty strings are explicit expressions.
type ListBuffersRequest struct {
	// Format selects tmux's output format.
	Format *string
	// Filter is a raw tmux filter expression evaluated by tmux.
	Filter *TmuxFilter
}

// SetBuffer stores or appends exact string data without refreshing models. It
// mutates the selected paste buffer; completed stderr is reported as a
// redacted command error because Data may be secret. Cancellation does not
// prove that tmux did not store data.
func (s Server) SetBuffer(ctx context.Context, request SetBufferRequest) error {
	if err := validateBufferName("set-buffer", request.Name); err != nil {
		return err
	}
	if err := validateServerCommandArgument("set-buffer", "Data", request.Data, true); err != nil {
		return err
	}
	arguments := []string{"set-buffer"}
	if request.Append {
		arguments = append(arguments, "-a")
	}
	if request.Name != nil {
		arguments = append(arguments, "-b", *request.Name)
	}
	arguments = append(arguments, "--", request.Data)
	result, err := s.literalCmd(ctx, arguments...)
	if err != nil {
		return err
	}
	if len(result.Stderr) != 0 {
		return newRedactedCommandError("set-buffer", result)
	}
	return nil
}

// ShowBuffer returns one buffer using Python-compatible line reconstruction.
// The returned string is owned by the caller; nil Name selects tmux's most
// recent buffer. Completed stderr is an error.
func (s Server) ShowBuffer(ctx context.Context, name *string) (string, error) {
	result, err := s.showBuffer(ctx, name)
	if err != nil {
		return "", err
	}
	return strings.Join(result.Stdout, "\n"), nil
}

// ShowBufferBytes returns one buffer as exact caller-owned tmux stdout bytes.
// Unlike [Server.ShowBuffer], it preserves invalid UTF-8, delimiters, and
// trailing newlines. Nil Name selects tmux's most recent buffer. Completed
// stderr returns a redacted command error and a nil byte slice.
func (s Server) ShowBufferBytes(ctx context.Context, name *string) ([]byte, error) {
	result, err := s.showBuffer(ctx, name)
	if err != nil {
		return nil, err
	}
	return result.RawStdout, nil
}

func (s Server) showBuffer(ctx context.Context, name *string) (CommandResult, error) {
	if err := validateBufferName("show-buffer", name); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	arguments := []string{"show-buffer"}
	if name != nil {
		arguments = append(arguments, "-b", *name)
	}
	// show-buffer's result is tmux's own stdout, which [Server.ShowBufferBytes]
	// returns byte for byte, so this read requires a tmux process.
	result, err := s.requireProcess().literalCmd(ctx, arguments...)
	if err := requireRedactedServerCommandNoStderr("show-buffer", result, err); err != nil {
		return result, err
	}
	return result, nil
}

// DeleteBuffer deletes a named buffer, or the most recent buffer when name is nil.
// Cancellation does not prove that tmux did not delete the buffer.
func (s Server) DeleteBuffer(ctx context.Context, name *string) error {
	if err := validateBufferName("delete-buffer", name); err != nil {
		return err
	}
	arguments := []string{"delete-buffer"}
	if name != nil {
		arguments = append(arguments, "-b", *name)
	}
	result, err := s.literalCmd(ctx, arguments...)
	return requireServerCommandNoStderr("delete-buffer", result, err)
}

// SaveBuffer writes a named or most-recent buffer to a file. Path follows
// [Server.SourceFile]'s current-user expansion and lexical normalization. It changes
// that file; exact "-" is rejected because the runner exposes no process stdio.
func (s Server) SaveBuffer(ctx context.Context, request SaveBufferRequest) error {
	if err := validateBufferName("save-buffer", request.Name); err != nil {
		return err
	}
	path, err := expandBufferPath("save-buffer", request.Path)
	if err != nil {
		return err
	}
	arguments := []string{"save-buffer"}
	if request.Append {
		arguments = append(arguments, "-a")
	}
	if request.Name != nil {
		arguments = append(arguments, "-b", *request.Name)
	}
	arguments = append(arguments, "--", path)
	result, err := s.literalCmd(ctx, arguments...)
	return requireServerCommandNoStderr("save-buffer", result, err)
}

// LoadBuffer reads a file into a named or newly allocated buffer. Path follows
// [Server.SourceFile]'s current-user expansion and lexical normalization. It mutates
// tmux's destination buffer and rejects exact "-" because no process stdio is exposed.
func (s Server) LoadBuffer(ctx context.Context, request LoadBufferRequest) error {
	if err := validateBufferName("load-buffer", request.Name); err != nil {
		return err
	}
	path, err := expandBufferPath("load-buffer", request.Path)
	if err != nil {
		return err
	}
	arguments := []string{"load-buffer"}
	if request.Name != nil {
		arguments = append(arguments, "-b", *request.Name)
	}
	arguments = append(arguments, "--", path)
	result, err := s.literalCmd(ctx, arguments...)
	return requireServerCommandNoStderr("load-buffer", result, err)
}

// ListBuffers returns an owned snapshot of live tmux buffer rows. A command or
// transport failure is returned rather than answered with no rows; context
// cancellation still returns its context error. The result is not a live
// collection.
func (s Server) ListBuffers(
	ctx context.Context,
	request ListBuffersRequest,
) ([]string, error) {
	if request.Format != nil {
		if err := validateServerCommandArgument(
			"list-buffers", "Format", *request.Format, true,
		); err != nil {
			return nil, err
		}
	}
	if request.Filter != nil {
		if err := validateServerCommandArgument(
			"list-buffers", "Filter", string(*request.Filter), true,
		); err != nil {
			return nil, err
		}
	}
	arguments := []string{"list-buffers"}
	if request.Format != nil {
		arguments = append(arguments, "-F", *request.Format)
	}
	if request.Filter != nil {
		arguments = append(arguments, "-f", string(*request.Filter))
	}
	return runRedactedServerListCommand(ctx, s, arguments)
}

func validateBufferName(subcommand string, name *string) error {
	if name == nil {
		return nil
	}
	return validateServerCommandArgument(subcommand, "Name", *name, true)
}

func expandBufferPath(subcommand string, path string) (string, error) {
	if path == "" {
		return "", invalidServerCommandRequest(
			subcommand,
			"Path",
			path,
			"must not be empty",
		)
	}
	if path == "-" {
		return "", invalidServerCommandRequest(
			subcommand,
			"Path",
			path,
			"requires process stdio, which this runner does not expose",
		)
	}
	expanded, err := expandCommandPath(subcommand, path)
	if err != nil {
		return "", err
	}
	return expanded, nil
}

func runServerListCommand(
	ctx context.Context,
	server Server,
	arguments []string,
) ([]string, error) {
	return runServerListCommandWithError(ctx, server, arguments, newCommandError)
}

func runRedactedServerListCommand(
	ctx context.Context,
	server Server,
	arguments []string,
) ([]string, error) {
	return runServerListCommandWithError(ctx, server, arguments, newRedactedCommandError)
}

func runServerListCommandWithError(
	ctx context.Context,
	server Server,
	arguments []string,
	newError func(string, CommandResult) *CommandError,
) ([]string, error) {
	result, err := server.literalCmd(ctx, arguments...)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		return nil, newError(arguments[0], result)
	}
	return slices.Clone(result.Stdout), nil
}
