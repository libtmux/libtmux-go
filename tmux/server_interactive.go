package tmux

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

var (
	serverInteractiveVersion33 = Version{raw: "3.3", major: 3, minor: 3}
	serverInteractiveVersion34 = Version{raw: "3.4", major: 3, minor: 4}
	serverInteractiveVersion35 = Version{raw: "3.5", major: 3, minor: 5}
	serverInteractiveVersion36 = Version{raw: "3.6", major: 3, minor: 6}
	serverInteractiveVersion37 = Version{raw: "3.7", major: 3, minor: 7}
)

// ConfirmBeforeRequest configures a background tmux confirmation prompt. Its
// zero value is invalid because Command is required. Nil pointer fields omit
// their flags while explicit empty strings are passed to tmux; ConfirmKey must
// be one printable ASCII character.
type ConfirmBeforeRequest struct {
	// Command is the required tmux command shown for confirmation.
	Command string
	// Prompt replaces tmux's prompt when nonnil.
	Prompt *string
	// ConfirmKey selects the affirmative key and requires tmux 3.4; see
	// UnsupportedPolicy.
	ConfirmKey *string
	// DefaultYes selects yes by default and requires tmux 3.4; see
	// UnsupportedPolicy.
	DefaultYes bool
	// TargetClient selects the stable client that displays the prompt.
	TargetClient ClientName
}

// CommandPromptRequest configures a background tmux command prompt. Its zero
// value is invalid because Template is required; nil pointer fields omit flags
// while explicit empty values are passed to tmux.
type CommandPromptRequest struct {
	// Template is the required tmux command template submitted by the prompt.
	Template string
	// Prompt replaces tmux's prompt text when nonnil.
	Prompt *string
	// Inputs supplies initial prompt inputs when nonnil.
	Inputs *string
	// TargetClient selects the stable client that displays the prompt.
	TargetClient ClientName
	// OneKey accepts one key rather than a line.
	OneKey bool
	// KeyOnly limits accepted input to keys.
	KeyOnly bool
	// OnInputChange runs Template after each input change.
	OnInputChange bool
	// Numeric restricts accepted input to a number.
	Numeric bool
	// Type selects the associated prompt-history class; zero omits -T.
	Type PromptType
	// ExpandFormat expands formats in the prompt result.
	ExpandFormat bool
	// Literal disables key-name parsing and requires tmux 3.6; see
	// UnsupportedPolicy.
	Literal bool
	// BackspaceExit exits on backspace and requires tmux 3.7; see
	// UnsupportedPolicy.
	BackspaceExit bool
	// NoFreeze leaves the pane unfrozen and requires tmux 3.7; see
	// UnsupportedPolicy.
	NoFreeze bool
}

// MenuItem is one display-menu entry. Its zero value is a separator. A named
// item always contributes its name, key, and command to tmux, including empty
// key or command strings.
type MenuItem struct {
	// Name is the visible item name; an empty name makes this item a separator.
	Name string
	// Key is the optional shortcut for a named item.
	Key string
	// Command is the tmux command for a named item.
	Command string
}

// DisplayMenuRequest configures one tmux popup menu. Position and style
// strings retain tmux's format and symbolic-position languages verbatim. Its
// zero value is invalid because Items is required. All pointer fields omit
// their flags when nil and retain explicit empty strings; DisplayMenu copies
// Items before calling tmux. TargetPane and TargetClient are independent but
// must be stable identities when present.
type DisplayMenuRequest struct {
	// Items are menu rows; a zero MenuItem is a separator.
	Items []MenuItem
	// Title is the optional menu title.
	Title *string
	// TargetPane supplies pane context for format expansion.
	TargetPane PaneID
	// TargetClient selects the client that renders the menu.
	TargetClient ClientName
	// X is tmux's horizontal position expression.
	X *string
	// Y is tmux's vertical position expression.
	Y *string
	// StartingChoice selects the initial item and requires tmux 3.4; see
	// UnsupportedPolicy.
	StartingChoice *string
	// BorderLines selects border glyphs and requires tmux 3.4; see
	// UnsupportedPolicy.
	BorderLines *string
	// Style selects menu style and requires tmux 3.4; see UnsupportedPolicy.
	Style *string
	// BorderStyle selects border style and requires tmux 3.4; see
	// UnsupportedPolicy.
	BorderStyle *string
	// SelectedStyle selects highlighted-item style and requires tmux 3.4; see
	// UnsupportedPolicy.
	SelectedStyle *string
	// Mouse enables mouse selection and requires tmux 3.5; see UnsupportedPolicy.
	Mouse bool
	// StayOpen keeps the menu visible after selecting an item.
	StayOpen bool
}

// AttachSessionOptions configures terminal ownership and attach behavior
// shared by server- and session-scoped attachment. Nil StartDirectory omits
// its flag and a nonnil empty string is explicit; ClientFlags is copied. The
// Stdin, Stdout, and Stderr pointers are retained for the attach call and are
// never owned or closed by this package.
type AttachSessionOptions struct {
	// DetachOthers disconnects other clients attached to the target session.
	DetachOthers bool
	// DetachParent detaches the invoking client from its parent session first.
	DetachParent bool
	// NoUpdateEnvironment preserves the invoking client's environment.
	NoUpdateEnvironment bool
	// ReadOnly attaches the client with read-only permissions.
	ReadOnly bool
	// StartDirectory selects the attached client's initial directory.
	StartDirectory *string
	// ClientFlags are comma-joined client flags copied before attachment starts.
	ClientFlags []string
	// Stdin is the attached tmux client's input stream; nil inherits process stdin.
	Stdin *os.File
	// Stdout is the attached tmux client's output stream; nil inherits process stdout.
	Stdout *os.File
	// Stderr is the attached tmux client's error stream; nil inherits process stderr.
	Stderr *os.File
}

// AttachSessionRequest selects a session name or tmux target pattern and
// configures a blocking terminal attachment. An empty Target lets tmux choose.
type AttachSessionRequest struct {
	// Target is a session name or tmux target pattern; empty leaves selection to tmux.
	Target string
	// AttachSessionOptions supplies terminal streams and attach flags.
	AttachSessionOptions
}

type confirmBeforeValues struct {
	command      string
	prompt       string
	confirmKey   string
	targetClient string
	defaultYes   bool
	hasPrompt    bool
	hasKey       bool
	hasClient    bool
}

type commandPromptValues struct {
	template      string
	prompt        string
	inputs        string
	targetClient  string
	promptType    string
	oneKey        bool
	keyOnly       bool
	onInputChange bool
	numeric       bool
	expandFormat  bool
	literal       bool
	backspaceExit bool
	noFreeze      bool
	hasPrompt     bool
	hasInputs     bool
	hasClient     bool
	hasPromptType bool
}

type displayMenuValues struct {
	items          []MenuItem
	title          string
	targetPane     string
	targetClient   string
	x              string
	y              string
	startingChoice string
	borderLines    string
	style          string
	borderStyle    string
	selectedStyle  string
	mouse          bool
	stayOpen       bool
	hasTitle       bool
	hasPane        bool
	hasClient      bool
	hasX           bool
	hasY           bool
	hasChoice      bool
	hasBorderLines bool
	hasStyle       bool
	hasBorderStyle bool
	hasSelected    bool
}

type attachSessionValues struct {
	target              string
	startDirectory      string
	clientFlags         []string
	stdin               *os.File
	stdout              *os.File
	stderr              *os.File
	detachOthers        bool
	detachParent        bool
	noUpdateEnvironment bool
	readOnly            bool
	hasStartDirectory   bool
}

// ConfirmBefore asks tmux to display a background confirmation prompt and
// returns after tmux accepts it, not after the user answers or Command runs.
// It requires tmux 3.3. ConfirmKey and DefaultYes require tmux 3.4 and follow
// [UnsupportedPolicy]. Completed stderr is an error; cancellation does not
// prove prompt delivery or later command execution did not occur.
func (s Server) ConfirmBefore(ctx context.Context, request ConfirmBeforeRequest) error {
	values, err := captureConfirmBeforeRequest(request)
	if err != nil {
		return err
	}
	current, err := s.Version(ctx)
	if err != nil {
		return err
	}
	if !current.AtLeast(serverInteractiveVersion33) {
		return &VersionTooLowError{Current: current, Minimum: serverInteractiveVersion33}
	}

	arguments := []string{"confirm-before", "-b"}
	if values.hasPrompt {
		arguments = append(arguments, "-p", values.prompt)
	}
	if values.hasKey {
		if current.AtLeast(serverInteractiveVersion34) {
			arguments = append(arguments, "-c", values.confirmKey)
		} else if err := s.unsupportedFeature(
			"confirm-before", "confirm_key", current, serverInteractiveVersion34,
		); err != nil {
			return err
		}
	}
	if values.defaultYes {
		if current.AtLeast(serverInteractiveVersion34) {
			arguments = append(arguments, "-y")
		} else if err := s.unsupportedFeature(
			"confirm-before", "default_yes", current, serverInteractiveVersion34,
		); err != nil {
			return err
		}
	}
	if values.hasClient {
		arguments = append(arguments, "-t", values.targetClient)
	}
	arguments = append(arguments, values.command)

	result, err := s.literalCmd(ctx, arguments...)
	return requireRedactedServerCommandNoStderr("confirm-before", result, err)
}

// CommandPrompt asks tmux to display a background command prompt and returns
// after tmux accepts it, not after input is submitted or Template runs. It
// requires tmux 3.3. Later flags follow [UnsupportedPolicy]. Completed stderr
// is an error; cancellation does not prove prompt delivery or later execution.
func (s Server) CommandPrompt(ctx context.Context, request CommandPromptRequest) error {
	values, err := captureCommandPromptRequest(request)
	if err != nil {
		return err
	}
	current, err := s.Version(ctx)
	if err != nil {
		return err
	}
	if !current.AtLeast(serverInteractiveVersion33) {
		return &VersionTooLowError{Current: current, Minimum: serverInteractiveVersion33}
	}

	arguments := []string{"command-prompt", "-b"}
	if values.oneKey {
		arguments = append(arguments, "-1")
	}
	if values.keyOnly {
		arguments = append(arguments, "-k")
	}
	if values.onInputChange {
		arguments = append(arguments, "-i")
	}
	if values.numeric {
		arguments = append(arguments, "-N")
	}
	if values.expandFormat {
		arguments = append(arguments, "-F")
	}
	if values.literal {
		if current.AtLeast(serverInteractiveVersion36) {
			arguments = append(arguments, "-l")
		} else if err := s.unsupportedFeature(
			"command-prompt", "literal", current, serverInteractiveVersion36,
		); err != nil {
			return err
		}
	}
	if values.backspaceExit {
		if current.AtLeast(serverInteractiveVersion37) {
			arguments = append(arguments, "-e")
		} else if err := s.unsupportedFeature(
			"command-prompt", "bspace_exit", current, serverInteractiveVersion37,
		); err != nil {
			return err
		}
	}
	if values.noFreeze {
		if current.AtLeast(serverInteractiveVersion37) {
			arguments = append(arguments, "-C")
		} else if err := s.unsupportedFeature(
			"command-prompt", "no_freeze", current, serverInteractiveVersion37,
		); err != nil {
			return err
		}
	}
	if values.hasPrompt {
		arguments = append(arguments, "-p", values.prompt)
	}
	if values.hasInputs {
		arguments = append(arguments, "-I", values.inputs)
	}
	if values.hasPromptType {
		arguments = append(arguments, "-T", values.promptType)
	}
	if values.hasClient {
		arguments = append(arguments, "-t", values.targetClient)
	}
	arguments = append(arguments, values.template)

	result, err := s.literalCmd(ctx, arguments...)
	return requireRedactedServerCommandNoStderr("command-prompt", result, err)
}

// DisplayMenu displays a menu and waits until tmux closes it. The target
// client must own a TTY; control-mode clients cannot render menu overlays.
// It returns no selected value and does not establish whether an item command
// ran. Unsupported optional flags follow [UnsupportedPolicy]. Completed stderr
// is an error, and cancellation does not prove menu delivery or a selected
// command's effect.
func (s Server) DisplayMenu(ctx context.Context, request DisplayMenuRequest) error {
	values, err := captureDisplayMenuRequest(request)
	if err != nil {
		return err
	}

	needsVersion := values.hasChoice || values.hasBorderLines || values.hasStyle ||
		values.hasBorderStyle || values.hasSelected || values.mouse
	var current Version
	if needsVersion {
		current, err = s.Version(ctx)
		if err != nil {
			return err
		}
	}

	arguments := []string{"display-menu"}
	if values.hasTitle {
		arguments = append(arguments, "-T", values.title)
	}
	if values.hasClient {
		arguments = append(arguments, "-c", values.targetClient)
	}
	if values.hasPane {
		arguments = append(arguments, "-t", values.targetPane)
	}
	if values.hasX {
		arguments = append(arguments, "-x", values.x)
	}
	if values.hasY {
		arguments = append(arguments, "-y", values.y)
	}
	if values.hasChoice {
		if current.AtLeast(serverInteractiveVersion34) {
			arguments = append(arguments, "-C", values.startingChoice)
		} else if err := s.unsupportedFeature(
			"display-menu", "starting_choice", current, serverInteractiveVersion34,
		); err != nil {
			return err
		}
	}
	if values.hasBorderLines {
		if current.AtLeast(serverInteractiveVersion34) {
			arguments = append(arguments, "-b", values.borderLines)
		} else if err := s.unsupportedFeature(
			"display-menu", "border_lines", current, serverInteractiveVersion34,
		); err != nil {
			return err
		}
	}
	if values.hasStyle {
		if current.AtLeast(serverInteractiveVersion34) {
			arguments = append(arguments, "-s", values.style)
		} else if err := s.unsupportedFeature(
			"display-menu", "style", current, serverInteractiveVersion34,
		); err != nil {
			return err
		}
	}
	if values.hasBorderStyle {
		if current.AtLeast(serverInteractiveVersion34) {
			arguments = append(arguments, "-S", values.borderStyle)
		} else if err := s.unsupportedFeature(
			"display-menu", "border_style", current, serverInteractiveVersion34,
		); err != nil {
			return err
		}
	}
	if values.hasSelected {
		if current.AtLeast(serverInteractiveVersion34) {
			arguments = append(arguments, "-H", values.selectedStyle)
		} else if err := s.unsupportedFeature(
			"display-menu", "selected_style", current, serverInteractiveVersion34,
		); err != nil {
			return err
		}
	}
	if values.mouse {
		if current.AtLeast(serverInteractiveVersion35) {
			arguments = append(arguments, "-M")
		} else if err := s.unsupportedFeature(
			"display-menu", "mouse", current, serverInteractiveVersion35,
		); err != nil {
			return err
		}
	}
	if values.stayOpen {
		arguments = append(arguments, "-O")
	}
	for _, menuItem := range values.items {
		arguments = append(arguments, menuItem.Name)
		if menuItem.Name != "" {
			arguments = append(arguments, menuItem.Key, menuItem.Command)
		}
	}

	result, err := s.literalCmd(ctx, arguments...)
	return requireRedactedServerCommandNoStderr("display-menu", result, err)
}

// Start starts the configured tmux server. It is idempotent when that
// server is already running. Completed stderr is an error; cancellation does
// not prove that a daemon was not started.
//
// With tmux's exit-empty default, a server holding no sessions may exit before
// the next command even though Start succeeded. Create a session or disable
// exit-empty in [ServerOptions.ConfigFile] before startup. Use [Server.IsAlive]
// or [Server.RaiseIfDead] to check it.
func (s Server) Start(ctx context.Context) error {
	result, err := s.literalCmd(ctx, "start-server")
	return requireServerCommandNoStderr("start-server", result, err)
}

// AttachSession attaches the caller's terminal to a matching session and
// blocks until detach or context cancellation. Nil standard streams inherit
// the process streams; each stream must be a concrete terminal descriptor.
// The call retains caller-supplied files while attached and never owns or
// closes them. A completed nonzero exit returns [CommandError]; cancellation
// can end waiting but does not prove that attachment or detachment did not occur.
func (s Server) AttachSession(ctx context.Context, request AttachSessionRequest) error {
	values, err := captureAttachSessionRequest(request.Target, request.AttachSessionOptions)
	if err != nil {
		return err
	}
	return s.attachSession(ctx, values)
}

// Attach attaches the caller's terminal to this session's stable identifier
// and blocks until detach or context cancellation. It validates the receiver's
// stable SessionID, retains but never closes caller-supplied streams, and
// returns [CommandError] for a completed nonzero exit. Cancellation does not
// prove attachment or detachment did not occur.
func (s Session) Attach(ctx context.Context, options AttachSessionOptions) error {
	target := s.sessionID.String()
	if err := validateTypedTarget(
		"attach-session", "Target", "session", target,
	); err != nil {
		return err
	}
	values, err := captureAttachSessionRequest(target, options)
	if err != nil {
		return err
	}
	return s.server.attachSession(ctx, values)
}

func (s Server) attachSession(ctx context.Context, values attachSessionValues) error {
	arguments := []string{"attach-session"}
	if values.detachOthers {
		arguments = append(arguments, "-d")
	}
	if values.detachParent {
		arguments = append(arguments, "-x")
	}
	if values.noUpdateEnvironment {
		arguments = append(arguments, "-E")
	}
	if values.readOnly {
		arguments = append(arguments, "-r")
	}
	if values.hasStartDirectory {
		arguments = append(arguments, "-c", values.startDirectory)
	}
	if len(values.clientFlags) != 0 {
		arguments = append(arguments, "-f", strings.Join(values.clientFlags, ","))
	}
	if values.target != "" {
		arguments = append(arguments, "-t", values.target)
	}
	result, err := s.streamingLiteralCmd(
		ctx,
		tmuxcmd.Stdio{Stdin: values.stdin, Stdout: values.stdout, Stderr: values.stderr},
		arguments...,
	)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return newRedactedCommandError("attach-session", result)
	}
	return nil
}

func (s Server) streamingLiteralCmd(
	ctx context.Context,
	stdio tmuxcmd.Stdio,
	args ...string,
) (CommandResult, error) {
	if err := validateLiteralCommandArguments(args); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	result, err := s.runCommand(ctx, commandProcess, args, &stdio, false)
	commandResult := CommandResult{
		Command:  slices.Clone(result.Command),
		ExitCode: result.ExitCode,
	}
	if err != nil {
		return commandResult, commandTransportFailure(err)
	}
	return commandResult, nil
}

func captureConfirmBeforeRequest(request ConfirmBeforeRequest) (confirmBeforeValues, error) {
	values := confirmBeforeValues{command: request.Command, defaultYes: request.DefaultYes}
	if values.command == "" {
		return confirmBeforeValues{}, invalidServerCommandRequest(
			"confirm-before", "Command", "", "must not be empty",
		)
	}
	if err := validateServerCommandArgument(
		"confirm-before", "Command", values.command, true,
	); err != nil {
		return confirmBeforeValues{}, err
	}
	if request.Prompt != nil {
		values.prompt = *request.Prompt
		values.hasPrompt = true
		if err := validateServerCommandArgument(
			"confirm-before", "Prompt", values.prompt, false,
		); err != nil {
			return confirmBeforeValues{}, err
		}
	}
	if request.ConfirmKey != nil {
		values.confirmKey = *request.ConfirmKey
		values.hasKey = true
		if len(values.confirmKey) != 1 || values.confirmKey[0] < 32 ||
			values.confirmKey[0] > 126 {
			return confirmBeforeValues{}, invalidServerCommandRequest(
				"confirm-before", "ConfirmKey", values.confirmKey,
				"must be one printable ASCII character",
			)
		}
	}
	if request.TargetClient != "" {
		values.targetClient = request.TargetClient.String()
		values.hasClient = true
		if err := validateServerCommandArgument(
			"confirm-before", "TargetClient", values.targetClient, true,
		); err != nil {
			return confirmBeforeValues{}, err
		}
		if err := validateTypedTarget(
			"confirm-before", "TargetClient", "client", values.targetClient,
		); err != nil {
			return confirmBeforeValues{}, err
		}
	}
	return values, nil
}

func captureCommandPromptRequest(request CommandPromptRequest) (commandPromptValues, error) {
	values := commandPromptValues{
		template:      request.Template,
		oneKey:        request.OneKey,
		keyOnly:       request.KeyOnly,
		onInputChange: request.OnInputChange,
		numeric:       request.Numeric,
		expandFormat:  request.ExpandFormat,
		literal:       request.Literal,
		backspaceExit: request.BackspaceExit,
		noFreeze:      request.NoFreeze,
	}
	if values.template == "" {
		return commandPromptValues{}, invalidServerCommandRequest(
			"command-prompt", "Template", "", "must not be empty",
		)
	}
	if err := validateServerCommandArgument(
		"command-prompt", "Template", values.template, true,
	); err != nil {
		return commandPromptValues{}, err
	}
	promptType, hasPromptType, err := promptHistoryType("command-prompt", request.Type)
	if err != nil {
		return commandPromptValues{}, err
	}
	values.promptType = promptType
	values.hasPromptType = hasPromptType
	if request.Prompt != nil {
		values.prompt = *request.Prompt
		values.hasPrompt = true
		if err := validateServerCommandArgument(
			"command-prompt", "Prompt", values.prompt, false,
		); err != nil {
			return commandPromptValues{}, err
		}
	}
	if request.Inputs != nil {
		values.inputs = *request.Inputs
		values.hasInputs = true
		if err := validateServerCommandArgument(
			"command-prompt", "Inputs", values.inputs, true,
		); err != nil {
			return commandPromptValues{}, err
		}
	}
	if request.TargetClient != "" {
		values.targetClient = request.TargetClient.String()
		values.hasClient = true
		if err := validateServerCommandArgument(
			"command-prompt", "TargetClient", values.targetClient, true,
		); err != nil {
			return commandPromptValues{}, err
		}
		if err := validateTypedTarget(
			"command-prompt", "TargetClient", "client", values.targetClient,
		); err != nil {
			return commandPromptValues{}, err
		}
	}
	return values, nil
}

func captureDisplayMenuRequest(request DisplayMenuRequest) (displayMenuValues, error) {
	if len(request.Items) == 0 {
		return displayMenuValues{}, invalidServerCommandRequest(
			"display-menu", "Items", "0", "must contain at least one entry",
		)
	}
	values := displayMenuValues{
		items:    make([]MenuItem, len(request.Items)),
		mouse:    request.Mouse,
		stayOpen: request.StayOpen,
	}
	copy(values.items, request.Items)
	for index, item := range values.items {
		if item.Name == "" && (item.Key != "" || item.Command != "") {
			return displayMenuValues{}, invalidServerCommandRequest(
				"display-menu", "Items["+strconv.Itoa(index)+"]", "",
				"a separator cannot have a key or command",
			)
		}
		if err := validateServerCommandArgument(
			"display-menu", "Items["+strconv.Itoa(index)+"].Name", item.Name, false,
		); err != nil {
			return displayMenuValues{}, err
		}
		if err := validateServerCommandArgument(
			"display-menu", "Items["+strconv.Itoa(index)+"].Key", item.Key, false,
		); err != nil {
			return displayMenuValues{}, err
		}
		if err := validateServerCommandArgument(
			"display-menu", "Items["+strconv.Itoa(index)+"].Command", item.Command, true,
		); err != nil {
			return displayMenuValues{}, err
		}
	}

	var err error
	if request.Title != nil {
		values.title, values.hasTitle = *request.Title, true
		err = validateServerCommandArgument("display-menu", "Title", values.title, false)
		if err != nil {
			return displayMenuValues{}, err
		}
	}
	if request.TargetPane != "" {
		values.targetPane, values.hasPane = request.TargetPane.String(), true
		if err = validateServerCommandArgument(
			"display-menu", "TargetPane", values.targetPane, true,
		); err != nil {
			return displayMenuValues{}, err
		}
		if err = validateTypedTarget(
			"display-menu", "TargetPane", "pane", values.targetPane,
		); err != nil {
			return displayMenuValues{}, err
		}
	}
	if request.TargetClient != "" {
		values.targetClient, values.hasClient = request.TargetClient.String(), true
		if err = validateServerCommandArgument(
			"display-menu", "TargetClient", values.targetClient, true,
		); err != nil {
			return displayMenuValues{}, err
		}
		if err = validateTypedTarget(
			"display-menu", "TargetClient", "client", values.targetClient,
		); err != nil {
			return displayMenuValues{}, err
		}
	}
	if request.X != nil {
		values.x, values.hasX = *request.X, true
		if err = validateServerCommandArgument("display-menu", "X", values.x, false); err != nil {
			return displayMenuValues{}, err
		}
	}
	if request.Y != nil {
		values.y, values.hasY = *request.Y, true
		if err = validateServerCommandArgument("display-menu", "Y", values.y, false); err != nil {
			return displayMenuValues{}, err
		}
	}
	if request.StartingChoice != nil {
		values.startingChoice, values.hasChoice = *request.StartingChoice, true
		if err = validateServerCommandArgument(
			"display-menu", "StartingChoice", values.startingChoice, false,
		); err != nil {
			return displayMenuValues{}, err
		}
	}
	if request.BorderLines != nil {
		values.borderLines, values.hasBorderLines = *request.BorderLines, true
		if err = validateServerCommandArgument(
			"display-menu", "BorderLines", values.borderLines, false,
		); err != nil {
			return displayMenuValues{}, err
		}
	}
	if request.Style != nil {
		values.style, values.hasStyle = *request.Style, true
		if err = validateServerCommandArgument("display-menu", "Style", values.style, false); err != nil {
			return displayMenuValues{}, err
		}
	}
	if request.BorderStyle != nil {
		values.borderStyle, values.hasBorderStyle = *request.BorderStyle, true
		if err = validateServerCommandArgument(
			"display-menu", "BorderStyle", values.borderStyle, false,
		); err != nil {
			return displayMenuValues{}, err
		}
	}
	if request.SelectedStyle != nil {
		values.selectedStyle, values.hasSelected = *request.SelectedStyle, true
		if err = validateServerCommandArgument(
			"display-menu", "SelectedStyle", values.selectedStyle, false,
		); err != nil {
			return displayMenuValues{}, err
		}
	}
	return values, nil
}

func captureAttachSessionRequest(
	target string,
	options AttachSessionOptions,
) (attachSessionValues, error) {
	values := attachSessionValues{
		target:              target,
		clientFlags:         slices.Clone(options.ClientFlags),
		stdin:               options.Stdin,
		stdout:              options.Stdout,
		stderr:              options.Stderr,
		detachOthers:        options.DetachOthers,
		detachParent:        options.DetachParent,
		noUpdateEnvironment: options.NoUpdateEnvironment,
		readOnly:            options.ReadOnly,
	}
	if err := validateServerCommandArgument(
		"attach-session", "Target", values.target, true,
	); err != nil {
		return attachSessionValues{}, err
	}
	if options.StartDirectory != nil {
		values.startDirectory = *options.StartDirectory
		values.hasStartDirectory = true
		if err := validateServerCommandArgument(
			"attach-session", "StartDirectory", values.startDirectory, true,
		); err != nil {
			return attachSessionValues{}, err
		}
	}
	for index, flag := range values.clientFlags {
		if err := validateServerCommandArgument(
			"attach-session",
			"ClientFlags["+strconv.Itoa(index)+"]",
			flag,
			true,
		); err != nil {
			return attachSessionValues{}, err
		}
	}
	return values, nil
}
