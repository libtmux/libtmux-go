package tmux

import (
	"context"
)

var serverKeysVersion37 = Version{raw: "3.7", major: 3, minor: 7}

// BindKeyRequest configures one tmux key binding. Its zero value is invalid
// because Key is required; nil KeyTable and Note omit their flags while
// pointers to empty strings are explicit.
type BindKeyRequest struct {
	// Key is the required tmux key notation to bind.
	Key string
	// Command is the tmux command; an empty command is a no-op binding on tmux 3.3+.
	Command string
	// KeyTable selects a table, or nil for tmux's default table.
	KeyTable *string
	// Note supplies tmux's optional binding note.
	Note *string
	// Repeat makes the binding repeatable.
	Repeat bool
}

// UnbindKeyRequest selects one key or an entire key table to unbind. Exactly
// one of Key and AllKeys must be set.
type UnbindKeyRequest struct {
	// Key selects one binding; it is mutually exclusive with AllKeys.
	Key *string
	// KeyTable limits removal to one table; nil selects tmux's default table.
	KeyTable *string
	// AllKeys removes every binding in KeyTable and is mutually exclusive with Key.
	AllKeys bool
	// Quiet suppresses tmux's missing-binding diagnostic.
	Quiet bool
}

// ListKeysRequest configures raw key-binding output. Its zero value selects
// tmux's default table and format; nil fields omit flags while explicit empty
// pointers are passed to tmux.
type ListKeysRequest struct {
	// KeyTable limits output to one key table.
	KeyTable *string
	// Format selects output format; tmux before 3.7 refuses it; see UnsupportedPolicy.
	Format *string
}

// ListCommandsRequest optionally limits output to one tmux command or alias.
// Nil CommandName lists all commands; a pointer to an empty name is explicit.
type ListCommandsRequest struct {
	// CommandName optionally selects one command or alias.
	CommandName *string
}

// ShowMessagesRequest selects the target and server information to print.
// Terminals and Jobs are independent and may both be enabled.
type ShowMessagesRequest struct {
	// TargetClient selects a stable client; zero selects tmux's current client.
	TargetClient ClientName
	// Terminals includes terminal capability information.
	Terminals bool
	// Jobs includes job status information.
	Jobs bool
}

// BindKey binds Key to Command in a tmux key table. An empty Command creates
// a no-op binding on tmux 3.3 or newer; tmux 3.2a reports its upstream error.
// It mutates the table; cancellation does not prove that tmux did not bind Key.
func (s Server) BindKey(ctx context.Context, request BindKeyRequest) error {
	keyTable, hasKeyTable := serverKeyOptionalString(request.KeyTable)
	note, hasNote := serverKeyOptionalString(request.Note)
	if err := validateServerKeyArguments(
		"bind-key",
		serverCommandArgument{field: "Key", value: request.Key},
		serverCommandArgument{field: "Command", value: request.Command},
		serverCommandArgument{field: "KeyTable", value: keyTable},
		serverCommandArgument{field: "Note", value: note},
	); err != nil {
		return err
	}
	if request.Key == "" {
		return invalidServerCommandRequest("bind-key", "Key", "", "must not be empty")
	}

	arguments := []string{"bind-key"}
	if request.Repeat {
		arguments = append(arguments, "-r")
	}
	if hasNote {
		arguments = append(arguments, "-N", note)
	}
	if hasKeyTable {
		arguments = append(arguments, "-T", keyTable)
	}
	arguments = append(arguments, request.Key, request.Command)
	return runServerKeyCommand(ctx, s, "bind-key", arguments)
}

// UnbindKey removes one key binding or every binding in a key table. Key and
// AllKeys must select exactly one target; cancellation does not prove removal
// did not occur.
func (s Server) UnbindKey(ctx context.Context, request UnbindKeyRequest) error {
	key, hasKey := serverKeyOptionalString(request.Key)
	keyTable, hasKeyTable := serverKeyOptionalString(request.KeyTable)
	if err := validateServerKeyArguments(
		"unbind-key",
		serverCommandArgument{field: "Key", value: key},
		serverCommandArgument{field: "KeyTable", value: keyTable},
	); err != nil {
		return err
	}
	if hasKey == request.AllKeys {
		return invalidServerCommandRequest(
			"unbind-key",
			"Key",
			key,
			"must select exactly one of Key or AllKeys",
		)
	}
	if hasKey && key == "" {
		return invalidServerCommandRequest("unbind-key", "Key", "", "must not be empty")
	}

	arguments := []string{"unbind-key"}
	if request.AllKeys {
		arguments = append(arguments, "-a")
	}
	if request.Quiet {
		arguments = append(arguments, "-q")
	}
	if hasKeyTable {
		arguments = append(arguments, "-T", keyTable)
	}
	if hasKey {
		arguments = append(arguments, key)
	}
	return runServerKeyCommand(ctx, s, "unbind-key", arguments)
}

// ListKeys returns an owned snapshot of raw tmux key-binding lines. Format
// requires tmux 3.7 or newer; older versions synchronously deliver a warning to
// the caller-goroutine [WarningHandler] before running the reduced command with
// tmux's default format.
// tmux 3.7, 3.7a, 3.7b, and 3.7c redirect a table's sole matching binding to a
// client status message, leaving stdout empty; this method preserves that
// upstream and Python behavior. Development tmux has corrected the issue.
// A list failure is returned rather than answered with no rows.
//
// It answers on a socket holding no server, unlike every other list here,
// because tmux runs list-keys against a server it starts for the purpose. That
// server holds no sessions and exits at once, leaving its socket file behind.
func (s Server) ListKeys(
	ctx context.Context,
	request ListKeysRequest,
) ([]string, error) {
	keyTable, hasKeyTable := serverKeyOptionalString(request.KeyTable)
	format, hasFormat := serverKeyOptionalString(request.Format)
	if err := validateServerKeyArguments(
		"list-keys",
		serverCommandArgument{field: "KeyTable", value: keyTable},
		serverCommandArgument{field: "Format", value: format},
	); err != nil {
		return nil, err
	}

	arguments := []string{"list-keys"}
	if hasKeyTable {
		arguments = append(arguments, "-T", keyTable)
	}
	if hasFormat {
		current, err := s.Version(ctx)
		if err != nil {
			return nil, err
		}
		if current.AtLeast(serverKeysVersion37) {
			arguments = append(arguments, "-F", format)
		} else if err := s.unsupportedFeature(
			"list-keys",
			"format",
			current,
			serverKeysVersion37,
		); err != nil {
			return nil, err
		}
	}
	return runServerListCommand(ctx, s, arguments)
}

// ListCommands returns an owned snapshot of raw tmux command-description lines.
// A list failure is returned rather than answered with no rows. Like
// [Server.ListKeys], it answers on a socket holding no server.
func (s Server) ListCommands(
	ctx context.Context,
	request ListCommandsRequest,
) ([]string, error) {
	commandName, hasCommandName := serverKeyOptionalString(request.CommandName)
	if err := validateServerKeyArguments(
		"list-commands",
		serverCommandArgument{field: "CommandName", value: commandName},
	); err != nil {
		return nil, err
	}
	arguments := []string{"list-commands"}
	if hasCommandName {
		arguments = append(arguments, commandName)
	}
	return runServerListCommand(ctx, s, arguments)
}

// ListClients returns an owned snapshot of raw tmux client-description lines.
// A list failure is returned rather than answered with no rows.
func (s Server) ListClients(ctx context.Context) ([]string, error) {
	return runServerListCommand(ctx, s, []string{"list-clients"})
}

// ShowMessages returns an owned snapshot of the tmux message log, terminal capabilities, job
// summary, or both terminal and job summaries. A zero TargetClient selects tmux's
// current client; a list failure is returned rather than answered with no rows.
func (s Server) ShowMessages(
	ctx context.Context,
	request ShowMessagesRequest,
) ([]string, error) {
	targetClient, hasTargetClient := string(request.TargetClient), request.TargetClient != ""
	if err := validateServerKeyArguments(
		"show-messages",
		serverCommandArgument{field: "TargetClient", value: targetClient},
	); err != nil {
		return nil, err
	}
	if hasTargetClient {
		if err := validateTypedTarget(
			"show-messages", "TargetClient", "client", targetClient,
		); err != nil {
			return nil, err
		}
	}

	arguments := []string{"show-messages"}
	if request.Terminals {
		arguments = append(arguments, "-T")
	}
	if request.Jobs {
		arguments = append(arguments, "-J")
	}
	if hasTargetClient {
		arguments = append(arguments, "-t", targetClient)
	}
	return runServerListCommand(ctx, s, arguments)
}

func runServerKeyCommand(
	ctx context.Context,
	server Server,
	subcommand string,
	arguments []string,
) error {
	result, err := server.literalCmd(ctx, arguments...)
	return requireRedactedServerCommandNoStderr(subcommand, result, err)
}

func serverKeyOptionalString[T ~string](value *T) (string, bool) {
	if value == nil {
		return "", false
	}
	return string(*value), true
}

func validateServerKeyArguments(
	subcommand string,
	arguments ...serverCommandArgument,
) error {
	return validateServerCommandArguments(subcommand, arguments...)
}
