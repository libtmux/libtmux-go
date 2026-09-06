package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// runCommandInput runs one command in a pane and waits for it to finish.
type runCommandInput struct {
	// PaneID is the tmux pane id, such as %1. Empty runs in the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to run the command in; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to run in when paneId is empty"`
	// Command is the POSIX-compatible shell command to run.
	Command string `json:"command" jsonschema:"the POSIX-compatible shell command to run"`
	// TimeoutSeconds bounds the non-detached operation. Zero uses a default.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty" jsonschema:"how long to wait before giving up"`
	// SuppressHistory keeps the command out of the shell's history, as it does
	// for send_keys. It covers the wrapper too, which is this package's own
	// bookkeeping and has no business in a person's history either.
	SuppressHistory bool `json:"suppressHistory,omitempty" jsonschema:"keep the command out of the shell's history by prefixing a space"`
	// MaxLines caps the returned output, keeping the last lines.
	MaxLines int `json:"maxLines,omitempty" jsonschema:"how many lines of output to return at most, keeping the last ones"`
	// MaxBytes caps the returned output's size, keeping the last lines.
	MaxBytes int `json:"maxBytes,omitempty" jsonschema:"how many bytes of output to return at most, keeping the last lines"`
}

// runCommandOutput reports how the command ended and what it wrote.
type runCommandOutput struct {
	// PaneID is the pane the command ran in.
	PaneID string `json:"paneId"`
	// ResolvedPaneIDs is the sorted configured preflight singleton.
	ResolvedPaneIDs []string `json:"resolvedPaneIds"`
	// ExitStatus is the command's exit status, absent when the command did not
	// finish. It is a pointer because zero is what a command reports when it
	// succeeded, so a timeout reported as zero would read as success to
	// anything branching on it.
	ExitStatus *int `json:"exitStatus,omitempty"`
	// TimedOut reports that the wait ended before the command did.
	TimedOut bool `json:"timedOut"`
	// Running is what the pane reported after dispatch, returned on a timeout.
	// A non-shell can mean the accepted command is running or that the process
	// changed after the final check and consumed the payload as input.
	Running string `json:"running,omitempty"`
	// Output is pane-rendered output between the command's cursor marks. Wrapped
	// terminal rows are rejoined and screen painting remains. Before tmux 3.6,
	// tabs are irreversibly expanded to spaces.
	Output []string `json:"output,omitempty"`
	// OutputUnavailable says why Output is missing, and is absent when the
	// command simply printed nothing. The status is the answer either way, so
	// failing to read the pane does not fail the call, but a caller branching
	// on empty output needs to know which of the two it has.
	OutputUnavailable string `json:"outputUnavailable,omitempty"`
	// LinesMissed reports that part of the output is gone rather than
	// truncated: the command erased tmux's scrollback, which renumbers the grid
	// the marks are recorded against, so whatever it printed before that cannot
	// be found. What it printed afterwards is still here. capture_since uses
	// the same word for the same thing.
	LinesMissed bool `json:"linesMissed,omitempty"`
	// EffectiveTimeoutSeconds is the budget this call actually used, which is
	// what timeoutSeconds asked for unless the server's ceiling was lower.
	EffectiveTimeoutSeconds int `json:"effectiveTimeoutSeconds,omitempty"`
	// TimeoutClamped reports that the ceiling shortened the wait, so a caller
	// that asked for longer learns the policy from a reply rather than from a
	// failed call.
	TimeoutClamped bool `json:"timeoutClamped,omitempty"`
	// truncation reports what the bounds dropped from Output.
	truncation
}

// runCommand types a command at a shell prompt and waits for its commit record.
// A shared filesystem carries its exit status and cursor marks bound its output.
// Both input checkpoints require a shell; timeout results report what the pane
// was running after dispatch.
func (t *tools) runCommand(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input runCommandInput,
) (*mcp.CallToolResult, runCommandOutput, error) {
	output := runCommandOutput{ResolvedPaneIDs: []string{}}
	if strings.TrimSpace(input.Command) == "" {
		return nil, output, errors.New("command is required")
	}
	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, output, err
	}
	timeout, clamped := t.resolveWaitTimeout(input.TimeoutSeconds)
	output.EffectiveTimeoutSeconds = int(timeout.Seconds())
	output.TimeoutClamped = clamped
	initial, err := t.preflightPaneInput(
		ctx, input.PaneID, input.SessionName, paneInputConfigured, "run_shell_command",
	)
	if err != nil {
		return nil, output, err
	}
	output.PaneID = initial.Source.ID().String()
	output.ResolvedPaneIDs = append([]string{}, initial.ConfiguredIDs...)
	runCtx, runCancel := context.WithTimeout(ctx, timeout)
	defer runCancel()
	started, err := t.startCommand(runCtx, request, input, initial)
	if started != nil {
		output.PaneID = started.paneID.String()
		output.ResolvedPaneIDs = append([]string{}, started.configuredIDs...)
	}
	if err != nil {
		if started != nil && started.dispatched {
			t.reapCommandRun(*started)
		}
		if ctx.Err() != nil {
			return nil, output, ctx.Err()
		}
		if isOwnWaitDeadline(ctx, runCtx, err) {
			output.TimedOut = true
			output.OutputUnavailable = "the effective timeout ended during command setup, " +
				"before pane output was collected"
			return nil, output, nil
		}
		return nil, output, err
	}
	reporter := newProgressReporter(
		runCtx, request, timeout, "waiting for the command to finish")
	defer reporter.stop()
	completed := false
	defer func() {
		if completed {
			t.finishCommandRun(*started)
		} else {
			t.reapCommandRun(*started)
		}
	}()

	pane := started.pane
	running, _ := pane.Formats().PaneCurrentCommand()
	result, output, err := t.awaitCommand(ctx, runCtx, awaiting{
		pane:       pane,
		statusPath: started.statusAt,
		openedPath: started.openedAt,
		closedPath: started.closedAt,
		limits:     limits,
		running:    running,
		output:     output,
	})
	completed = output.ExitStatus != nil
	return result, output, err
}

// startCommand types a command into a pane and returns its private completion
// record. The public operation always waits for that record.
func (t *tools) startCommand(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input runCommandInput,
	initial paneInputPreflight,
) (*commandRun, error) {
	started := &commandRun{
		pane: initial.Source, paneID: initial.Source.ID(),
		configuredIDs: append([]string{}, initial.ConfiguredIDs...),
	}
	if len(initial.ConfiguredIDs) != 1 {
		return started, fmt.Errorf(
			"run_shell_command refuses configured pane membership %v: one completion, output, and exit status require a configured singleton",
			initial.ConfiguredIDs,
		)
	}
	lease, err := processPaneInputs.acquire(
		initial.Identities(), paneInputReservationRun, "run_shell_command",
	)
	if err != nil {
		return started, err
	}
	started.lease = lease
	started.identity = lease.identities[0]
	dispatched := false
	defer func() {
		if !dispatched {
			processPaneInputs.release(lease)
		}
	}()
	if shell := incompatibleRunCommandShell(initial.Source); shell != "" {
		return started, fmt.Errorf(
			"run_shell_command requires a POSIX-compatible pane shell; pane %s is running %s; "+
				"use send_keys or respawn_pane with a compatible shell",
			initial.Source.ID(), shell,
		)
	}
	if err := t.confirmCallerInputPreflight(ctx, request, initial, "running a command"); err != nil {
		return started, err
	}
	server := t.tmux(ctx)
	route, err := resolveRunCommandRoute(ctx, server, initial.Source.ID().String())
	if err != nil {
		return started, err
	}
	sameEndpoint, err := samePaneInputEndpoint(route.socketPath, initial.Identity.endpoint)
	if err != nil {
		return started, fmt.Errorf("run_shell_command resolve pinned tmux endpoint: %w", err)
	}
	if !sameEndpoint {
		return started, errors.New(
			"run_shell_command refused: the selected and reported tmux endpoints differ",
		)
	}

	directory, err := os.MkdirTemp("", "libtmux-mcp-run")
	if err != nil {
		return started, err
	}

	statusPath := filepath.Join(directory, "status")
	openedPath := filepath.Join(directory, "opened")
	closedPath := filepath.Join(directory, "closed")
	commandPath := filepath.Join(directory, "command")
	trapPath := filepath.Join(directory, "traps")

	// In-pane marks exclude shell echo; the closing column distinguishes a
	// newline from output ending mid-row. Files hide markers from the pane, and
	// sourcing the caller's script inside a subshell keeps its syntax and an
	// `exit` command from changing the bookkeeping wrapper's structure.
	mark := fmt.Sprintf(
		"%s -S %s display-message -p -t %s "+
			"'#{history_size} #{cursor_y} #{cursor_x} #{pane_width} #{pane_height}'",
		shellQuote(route.executable),
		shellQuote(route.socketPath),
		shellQuote(route.paneID),
	)
	nonce := fmt.Sprintf("%x", sha256.Sum256([]byte(directory)))[:16]
	script := wrapperScript(
		mark, openedPath, commandPath, trapPath, statusPath, closedPath, nonce,
	)

	if err := os.WriteFile(commandPath, []byte(input.Command+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return started, err
	}
	// Source the wrapper so tabs and control bytes bypass the shell's line
	// editor; only this package-controlled path is typed into the pane.
	scriptPath := filepath.Join(directory, "script")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return started, err
	}
	started.directory = directory
	started.openedAt = openedPath
	started.closedAt = closedPath
	started.statusAt = statusPath
	sourceScript := ". " + shellQuote(scriptPath)
	if input.SuppressHistory {
		sourceScript = " " + sourceScript
	}
	dispatch := tmux.SendKeySequenceRequest{Keys: []string{sourceScript, "Enter"}}
	if err := t.runtime.deps.beforeRunDispatch(ctx); err != nil {
		_ = os.RemoveAll(directory)
		return started, fmt.Errorf(
			"run_shell_command pre-dispatch setup failed for configured pane membership %v: %w",
			started.configuredIDs, err,
		)
	}
	second, err := t.preflightPaneInput(
		ctx, initial.Source.ID().String(), "", paneInputConfigured, "run_shell_command",
	)
	if err != nil {
		_ = os.RemoveAll(directory)
		return started, err
	}
	started.configuredIDs = append([]string{}, second.ConfiguredIDs...)
	if len(second.ConfiguredIDs) != 1 {
		_ = os.RemoveAll(directory)
		return started, fmt.Errorf(
			"run_shell_command refuses configured pane membership %v at dispatch: one completion, output, and exit status require a configured singleton",
			second.ConfiguredIDs,
		)
	}
	if shell := incompatibleRunCommandShell(second.Source); shell != "" {
		_ = os.RemoveAll(directory)
		return started, fmt.Errorf(
			"run_shell_command refuses configured pane membership %v at dispatch: pane %s is running incompatible shell %s; use send_keys or respawn_pane with a compatible shell",
			second.ConfiguredIDs, second.Source.ID(), shell,
		)
	}
	if initial.Caller != second.Caller {
		_ = os.RemoveAll(directory)
		return started, errors.New(
			"run_shell_command refused: pane input caller changed before dispatch",
		)
	}
	if initial.Identity != second.Identity ||
		!slices.Equal(initial.ConfiguredIDs, second.ConfiguredIDs) {
		_ = os.RemoveAll(directory)
		return started, fmt.Errorf(
			"run_shell_command refused for pane %s: its route or configured membership changed before dispatch",
			second.Source.ID(),
		)
	}
	if initial.Signature.Source != second.Signature.Source ||
		!slices.Equal(initial.Signature.Members, second.Signature.Members) {
		_ = os.RemoveAll(directory)
		return started, fmt.Errorf(
			"run_shell_command refused for pane %s: its state or placement changed before dispatch",
			second.Source.ID(),
		)
	}
	if !processPaneInputs.owns(lease, second.Identities()) {
		_ = os.RemoveAll(directory)
		return started, fmt.Errorf(
			"run_shell_command refused for pane %s: its active run reservation changed before dispatch",
			second.Source.ID(),
		)
	}
	if route.paneID != second.Source.ID().String() {
		_ = os.RemoveAll(directory)
		return started, errors.New("run_shell_command refused: the tmux pane route changed before dispatch")
	}
	started.pane = second.Source
	started.paneID = second.Source.ID()
	if err := t.runtime.deps.sendKeySequence(ctx, second.Source, dispatch); err != nil {
		if errors.Is(err, tmux.ErrOutcomeUnknown) {
			dispatched = true
			started.dispatched = true
			return started, err
		}
		_ = os.RemoveAll(directory)
		return started, err
	}
	dispatched = true
	started.dispatched = true
	return started, nil
}

type runCommandRoute struct {
	executable string
	socketPath string
	paneID     string
}

func resolveRunCommandRoute(
	ctx context.Context,
	server tmux.Server,
	paneID string,
) (runCommandRoute, error) {
	configured := runCommandRoute{
		executable: server.Executable(), socketPath: server.SocketPath(), paneID: paneID,
	}
	if err := validateRunCommandRoute(configured); err != nil {
		return runCommandRoute{}, err
	}
	result, err := server.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil {
		return runCommandRoute{}, err
	}
	if result.ExitCode != 0 {
		return runCommandRoute{}, fmt.Errorf(
			"run_shell_command could not resolve its tmux socket (exit %d)", result.ExitCode,
		)
	}
	rawSocket := bytes.TrimSuffix(result.RawStdout, []byte{'\n'})
	if len(rawSocket) == 0 {
		return runCommandRoute{}, errors.New("tmux did not report its socket path")
	}
	route := runCommandRoute{
		executable: server.Executable(), socketPath: string(rawSocket), paneID: paneID,
	}
	if err := validateRunCommandRoute(route); err != nil {
		return runCommandRoute{}, err
	}
	return route, nil
}

func validateRunCommandRoute(route runCommandRoute) error {
	for _, component := range []struct {
		name, value string
	}{
		{name: "tmux executable", value: route.executable},
		{name: "tmux socket", value: route.socketPath},
		{name: "tmux pane", value: route.paneID},
	} {
		if hasASCIIControl(component.value) {
			return fmt.Errorf(
				"run_shell_command refuses ASCII control bytes in its %s route", component.name,
			)
		}
	}
	if !filepath.IsAbs(route.executable) || !filepath.IsAbs(route.socketPath) {
		return errors.New("run_shell_command requires absolute tmux executable and socket routes")
	}
	if !canonicalPaneID(route.paneID) {
		return errors.New("run_shell_command requires a canonical tmux pane route")
	}
	return nil
}

func hasASCIIControl(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 0x20 || value[index] == 0x7f {
			return true
		}
	}
	return false
}

// Only a known POSIX shell may receive the bookkeeping wrapper. An absent or
// unknown foreground command is unsafe because it may consume the payload as
// ordinary input instead of interpreting it as shell syntax.
func incompatibleRunCommandShell(pane tmux.Pane) string {
	command, ok := pane.Formats().PaneCurrentCommand()
	if !ok || command == "" {
		return "unknown foreground command"
	}
	if !slices.Contains(posixShells, shellName(command)) {
		return command
	}
	return ""
}

// nonPOSIXShells reject the bookkeeping wrapper's syntax; posixShells run it
// once they are reading keys. Both are shells, so both explain a pane that has
// not started a command yet rather than one that swallowed it.
var (
	nonPOSIXShells = []string{
		"csh", "elvish", "fish", "nu", "nushell", "powershell", "pwsh", "tcsh",
	}
	posixShells = []string{
		"ash", "bash", "dash", "ksh", "ksh93", "mksh", "pdksh", "sh", "zsh",
	}
)

// shellName reduces a pane's current command to a comparable shell name,
// dropping the login shell's leading hyphen.
func shellName(command string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Base(command), "-"))
}

// runsAShell reports whether a pane's current command reads typed keys once it
// is ready. Anything else has taken them as its own input.
func runsAShell(running string) bool {
	name := shellName(running)
	return slices.Contains(posixShells, name) || slices.Contains(nonPOSIXShells, name)
}

// awaiting is what a wait for one command needs to know, gathered so that the
// wait reads as one step rather than eight parameters.
type awaiting struct {
	pane                               tmux.Pane
	statusPath, openedPath, closedPath string
	limits                             bounds
	running                            string
	output                             runCommandOutput
}

type commandRun struct {
	pane          tmux.Pane
	paneID        tmux.PaneID
	identity      paneInputIdentity
	directory     string
	openedAt      string
	closedAt      string
	statusAt      string
	configuredIDs []string
	lease         paneInputLease
	dispatched    bool
}

func (t *tools) finishCommandRun(run commandRun) {
	processPaneInputs.release(run.lease)
	_ = os.RemoveAll(run.directory)
}

func (t *tools) reapCommandRun(run commandRun) {
	go func() {
		defer t.finishCommandRun(run)
		completion := time.NewTicker(commandCompletionPollInterval)
		presence := time.NewTicker(commandPresencePollInterval)
		defer completion.Stop()
		defer presence.Stop()
		for {
			_, ready, err := readCompletedCommand(run)
			if err == nil && ready {
				return
			}
			select {
			case <-completion.C:
			case <-presence.C:
				probeCtx, cancel := context.WithTimeout(
					context.Background(), commandPresenceProbeTimeout,
				)
				present, probeErr := commandRunPresent(probeCtx, run.pane, run.identity)
				cancel()
				if probeErr == nil && !present {
					return
				}
			}
		}
	}()
}

const (
	commandCompletionPollInterval = 50 * time.Millisecond
	commandPresencePollInterval   = 500 * time.Millisecond
	commandPresenceProbeTimeout   = 2 * time.Second
)

func commandRunPresent(
	ctx context.Context,
	pane tmux.Pane,
	identity paneInputIdentity,
) (bool, error) {
	_, err := pane.Refresh(ctx)
	if err == nil {
		return true, nil
	}
	if commandRunDisappeared(err) {
		return false, nil
	}
	if errors.Is(err, tmux.ErrNoServer) {
		present, processErr := commandRunProcessPresent(identity.serverPID)
		if processErr == nil {
			return present, nil
		}
		return true, errors.Join(err, processErr)
	}
	return true, err
}

func commandRunDisappeared(err error) bool {
	return errors.Is(err, tmux.ErrSnapshotNotFound) ||
		errors.Is(err, tmux.ErrDaemonReplaced)
}

func commandRunProcessPresent(pid uint64) (bool, error) {
	if pid == 0 || pid > uint64(^uint(0)>>1) {
		return true, errors.New("run_shell_command retained daemon pid is invalid")
	}
	process, err := os.FindProcess(int(pid))
	if err != nil {
		return true, err
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil || errors.Is(err, os.ErrPermission) {
		return true, nil
	}
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return true, err
}

// awaitCommand blocks until a started command publishes its commit record, and
// reads back its status and the rows it wrote.
func (t *tools) awaitCommand(
	ctx context.Context,
	waitCtx context.Context,
	waiting awaiting,
) (*mcp.CallToolResult, runCommandOutput, error) {
	output := waiting.output

	completion := commandRun{
		openedAt: waiting.openedPath,
		statusAt: waiting.statusPath,
		closedAt: waiting.closedPath,
	}
	status, ready, err := waitForCompletedCommand(waitCtx, completion)
	if err == nil && ready {
		return t.finishAwaitedCommand(ctx, waitCtx, waiting, output, status)
	}
	if ctx.Err() != nil {
		return nil, output, ctx.Err()
	}
	if !isOwnWaitDeadline(ctx, waitCtx, err) {
		return nil, output, err
	}
	return finishRunCommandDeadline(completion, output, waiting.running)
}

func (t *tools) finishAwaitedCommand(
	ctx context.Context,
	waitCtx context.Context,
	waiting awaiting,
	output runCommandOutput,
	status int,
) (*mcp.CallToolResult, runCommandOutput, error) {
	output.ExitStatus = &status
	if outputErr := t.attachCommandOutput(waitCtx, waiting.pane,
		waiting.openedPath, waiting.closedPath, waiting.limits, &output); outputErr != nil {
		if isOwnWaitDeadline(ctx, waitCtx, outputErr) {
			output.Output = nil
			output.OutputUnavailable = "the effective timeout ended before pane output was collected"
			return nil, output, nil
		}
		return nil, output, outputErr
	}
	return nil, output, nil
}

func finishRunCommandDeadline(
	completion commandRun,
	output runCommandOutput,
	running string,
) (*mcp.CallToolResult, runCommandOutput, error) {
	status, ready, err := readCompletedCommand(completion)
	if err != nil {
		return nil, output, err
	}
	if ready {
		output.ExitStatus = &status
	} else {
		output.TimedOut = true
		output.Running = running
	}
	output.OutputUnavailable = "the effective timeout ended before pane output was collected"
	if reason := unstartedReason(completion.openedAt, running); reason != "" {
		output.OutputUnavailable = reason
	}
	return nil, output, nil
}

func waitForCompletedCommand(
	waitCtx context.Context,
	entry commandRun,
) (status int, ready bool, err error) {
	ticker := time.NewTicker(commandCompletionPollInterval)
	defer ticker.Stop()
	for {
		status, ready, err = readCompletedCommand(entry)
		if err != nil || ready {
			return status, ready, err
		}
		select {
		case <-ticker.C:
		case <-waitCtx.Done():
			status, ready, err = readCompletedCommand(entry)
			if err != nil || ready {
				return status, ready, err
			}
			return 0, false, waitCtx.Err()
		}
	}
}

// readCompletedCommand treats the closing cursor mark as the commit record.
func readCompletedCommand(entry commandRun) (int, bool, error) {
	recorded, err := os.ReadFile(entry.statusAt)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read command status: %w", err)
	}
	if _, err := readMark(entry.closedAt); errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	} else if err != nil {
		return 0, false, err
	}
	status, err := strconv.Atoi(strings.TrimSpace(string(recorded)))
	if err != nil {
		return 0, false, fmt.Errorf("unreadable exit status %q", recorded)
	}
	return status, true, nil
}
