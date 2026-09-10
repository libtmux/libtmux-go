package tmux

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RunOptions configures [Session.Run]. Its zero value runs the command in a
// window tmux names, in the session's default directory and environment, and
// removes that window once the command has finished.
type RunOptions struct {
	// WindowName names the window the command runs in; empty lets tmux choose.
	WindowName string
	// StartDirectory expands ~ and ~/... for the current user; empty inherits
	// the session's default.
	StartDirectory string
	// Environment is added to the command's environment. The map is not
	// retained.
	Environment map[string]string
	// Keep leaves the finished window in place, showing what the command
	// printed, instead of removing it.
	Keep bool
}

// RunResult reports how a command finished and what its terminal showed.
type RunResult struct {
	// Status is the command's exit status. It is zero when Signal is set.
	Status int
	// Signal names the signal that ended the command, when one did.
	Signal string
	// Lines is what the terminal showed when the command finished, without
	// the blank lines tmux pads a screen with.
	Lines []string
	// Pane is the pane the command ran in. It is live only when the run kept
	// its window.
	Pane PaneID
}

// Run starts command in a new window of this session, waits for it to finish,
// and reports its exit status and what its terminal showed. It is [os/exec]
// for a program that needs a terminal: the command runs with a tty, so it
// sees isatty, colour, and a width, and what comes back is the screen tmux
// rendered rather than the bytes the program wrote.
//
// A nonzero status is a result, not an error; the error reports what tmux
// could not do. The wait is tmux's own, not a poll: it needs no tmux binary on
// the command's PATH and returns as the process exits. A command that never
// exits holds Run until ctx ends. [Server.RunShell] runs a command with no
// pane at all.
func (s Session) Run(ctx context.Context, command string, options RunOptions) (result RunResult, err error) {
	if command == "" {
		return RunResult{}, invalidServerCommandRequest("respawn-pane", "Command", "", "is required")
	}
	// The command's own exit would destroy the window before remain-on-exit
	// could be set on it, so the window starts with cat, which holds its
	// terminal until it is replaced once the pane is armed.
	request := NewWindowRequest{Command: "cat"}
	if options.WindowName != "" {
		request.Name = &options.WindowName
	}
	window, err := s.NewWindow(ctx, request)
	if err != nil {
		return RunResult{}, fmt.Errorf("start window: %w", err)
	}
	defer func() {
		if err == nil && options.Keep {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if killErr := window.Kill(cleanupCtx); killErr != nil && err == nil {
			err = fmt.Errorf("remove window: %w", killErr)
		}
	}()
	pane, ok, err := window.ResolveActivePane(ctx)
	if err != nil {
		return RunResult{}, err
	}
	if !ok {
		return RunResult{}, errors.New("tmux: new window reported no pane")
	}
	if err := pane.SetRemainOnExit(ctx, RemainOnExitOn); err != nil {
		return RunResult{}, err
	}
	// tmux writes a dead-pane notice into the screen itself. From 3.3 the
	// notice is an option and an empty one writes nothing; before that its
	// text is fixed and is removed from the capture instead.
	version, err := s.server.Version(ctx)
	if err != nil {
		return RunResult{}, err
	}
	fixedNotice := !version.AtLeast(remainOnExitFormatVersion33)
	if !fixedNotice {
		if err := pane.SetRemainOnExitFormat(ctx, ""); err != nil {
			return RunResult{}, err
		}
	}
	// Channels are server-global, and tmux keeps a signal nobody is waiting
	// for until the next waiter takes it, so each run owns a fresh name.
	channel := "libtmux-go-run-" + rand.Text()
	if err := pane.SetHook(ctx, "pane-died", "wait-for -S "+channel); err != nil {
		return RunResult{}, err
	}
	respawn := RespawnRequest{Command: &command, Kill: true, Environment: options.Environment}
	if options.StartDirectory != "" {
		respawn.StartDirectory = &options.StartDirectory
	}
	if _, err := pane.Respawn(ctx, respawn); err != nil {
		return RunResult{}, fmt.Errorf("start command: %w", err)
	}
	if err := s.server.WaitFor(ctx, WaitForRequest{Channel: channel}); err != nil {
		return RunResult{}, fmt.Errorf("wait for command: %w", err)
	}
	finished, err := pane.Refresh(ctx)
	if err != nil {
		return RunResult{}, err
	}
	result.Pane = finished.ID()
	// A signal-killed pane reports pane_dead_signal and no pane_dead_status, so
	// a missing status is the documented Signal case rather than an error.
	result.Status, _ = finished.DeadStatus()
	result.Signal, _ = finished.DeadSignal()
	lines, err := finished.Capture(ctx, CapturePaneRequest{Start: CaptureBoundary, End: CaptureBoundary})
	if err != nil {
		return RunResult{}, fmt.Errorf("read screen: %w", err)
	}
	result.Lines = trimScreen(lines, fixedNotice)
	return result, nil
}

var remainOnExitFormatVersion33 = Version{raw: "3.3", major: 3, minor: 3}

// deadPaneNotice is the fixed text tmux 3.2a writes when a remain-on-exit
// pane's process ends. Later releases render remain-on-exit-format instead.
const deadPaneNotice = "Pane is dead (status "

// trimScreen drops the blank lines tmux pads a screen to its height with and,
// when tmux wrote its fixed dead-pane notice, that notice, so lines hold only
// what the command showed.
func trimScreen(lines []string, fixedNotice bool) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	// tmux 3.2a writes the notice on the screen's bottom row, below whatever
	// blank rows separate it from the command's last line.
	if fixedNotice && end > 0 {
		if at := strings.Index(lines[end-1], deadPaneNotice); at >= 0 {
			lines[end-1] = strings.TrimRight(lines[end-1][:at], " ")
		}
	}
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}
