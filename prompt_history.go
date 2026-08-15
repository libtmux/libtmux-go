package tmux

import (
	"context"
	"strconv"
)

var promptHistoryVersion33 = Version{raw: "3.3", major: 3, minor: 3}

// PromptType selects one closed tmux prompt-history class. The zero value
// selects all prompt types.
type PromptType uint8

// Supported tmux prompt-history classes.
const (
	// PromptTypeAll selects every prompt-history class and omits -T.
	PromptTypeAll PromptType = iota
	// PromptTypeCommand selects command-prompt history.
	PromptTypeCommand
	// PromptTypeSearch selects search-prompt history.
	PromptTypeSearch
	// PromptTypeTarget selects target-prompt history.
	PromptTypeTarget
	// PromptTypeWindowTarget selects window-target-prompt history.
	PromptTypeWindowTarget
)

// PromptHistoryRequest selects one prompt-history class. A zero request
// selects every class.
type PromptHistoryRequest struct {
	// Type selects a prompt-history class; zero selects all classes.
	Type PromptType
}

// ShowPromptHistory returns an owned snapshot of tmux prompt-history lines.
// It requires tmux 3.3 or later and returns [VersionTooLowError] below that
// floor. Tmux command and transport failures follow the server's lenient list
// policy unless [Server.WithStrictErrors] is enabled.
func (s Server) ShowPromptHistory(
	ctx context.Context,
	request PromptHistoryRequest,
) ([]string, error) {
	promptType, hasPromptType, err := promptHistoryType("show-prompt-history", request.Type)
	if err != nil {
		return nil, err
	}
	current, err := s.Version(ctx)
	if err != nil {
		return normalizeServerListVersionFailure(ctx, s, err)
	}
	if !current.AtLeast(promptHistoryVersion33) {
		return nil, &VersionTooLowError{Current: current, Minimum: promptHistoryVersion33}
	}

	arguments := []string{"show-prompt-history"}
	if hasPromptType {
		arguments = append(arguments, "-T", promptType)
	}
	return runServerListCommand(ctx, s, arguments)
}

// ClearPromptHistory clears all tmux prompt history or one selected class.
// Version, transport, and completed-stderr failures are returned. It requires
// tmux 3.3 or later and returns [VersionTooLowError] below that floor.
func (s Server) ClearPromptHistory(
	ctx context.Context,
	request PromptHistoryRequest,
) error {
	promptType, hasPromptType, err := promptHistoryType("clear-prompt-history", request.Type)
	if err != nil {
		return err
	}
	current, err := s.Version(ctx)
	if err != nil {
		return err
	}
	if !current.AtLeast(promptHistoryVersion33) {
		return &VersionTooLowError{Current: current, Minimum: promptHistoryVersion33}
	}

	arguments := []string{"clear-prompt-history"}
	if hasPromptType {
		arguments = append(arguments, "-T", promptType)
	}
	result, err := s.literalCmd(ctx, arguments...)
	return requireServerCommandNoStderr("clear-prompt-history", result, err)
}

func promptHistoryType(subcommand string, promptType PromptType) (string, bool, error) {
	switch promptType {
	case PromptTypeAll:
		return "", false, nil
	case PromptTypeCommand:
		return "command", true, nil
	case PromptTypeSearch:
		return "search", true, nil
	case PromptTypeTarget:
		return "target", true, nil
	case PromptTypeWindowTarget:
		return "window-target", true, nil
	default:
		return "", false, invalidServerCommandRequest(
			subcommand,
			"Type",
			strconv.FormatUint(uint64(promptType), 10),
			"is unsupported",
		)
	}
}
