package tmux

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"
)

// RunOptions configures [Session.Run] and [Session.Start]. Its zero value
// runs the command in a window tmux names, in the session's default directory
// and environment, and removes that window once the command has finished.
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

// clone returns result with an independently owned Lines, so a cached result
// handed to more than one caller cannot be corrupted by one of them.
func (result RunResult) clone() RunResult {
	result.Lines = slices.Clone(result.Lines)
	return result
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
// exits holds Run until ctx ends. On error, Run removes its window regardless
// of [RunOptions.Keep], since Run gives the caller no other chance to retry or
// clean up; a bare [Session.Start] followed by [Running.Wait] leaves that
// choice to the caller instead. [Server.RunShell] runs a command with no pane
// at all.
func (s Session) Run(ctx context.Context, command string, options RunOptions) (RunResult, error) {
	running, err := s.Start(ctx, command, options)
	if err != nil {
		return RunResult{}, err
	}
	result, err := running.Wait(ctx)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = running.window.Kill(cleanupCtx)
	}
	return result, err
}

// Start runs command in a new window of this session and returns once it is
// under way, without waiting for it to finish. The returned [Running] reports
// the pane it runs in and blocks on demand: [Running.Wait] waits for the exit
// status and screen [Session.Run] would have returned, and [Running.Kill]
// stops the command. Options are exactly Run's.
//
// If Wait is never called, the window Start created is never removed.
func (s Session) Start(
	ctx context.Context,
	command string,
	options RunOptions,
) (running *Running, err error) {
	if command == "" {
		return nil, invalidServerCommandRequest("respawn-pane", "Command", "", "is required")
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
		return nil, fmt.Errorf("start window: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = window.Kill(cleanupCtx)
	}()
	pane, ok, err := window.ResolveActivePane(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("tmux: new window reported no pane")
	}
	if err := pane.SetRemainOnExit(ctx, RemainOnExitOn); err != nil {
		return nil, err
	}
	// tmux writes a dead-pane notice into the screen itself. From 3.3 the
	// notice is an option and an empty one writes nothing; before that its
	// text is fixed and is removed from the capture instead.
	version, err := s.server.Version(ctx)
	if err != nil {
		return nil, err
	}
	fixedNotice := !version.AtLeast(remainOnExitFormatVersion33)
	if !fixedNotice {
		if err := pane.SetRemainOnExitFormat(ctx, ""); err != nil {
			return nil, err
		}
	}
	// Channels are server-global, and tmux keeps a signal nobody is waiting
	// for until the next waiter takes it, so each run owns a fresh name.
	channel := "libtmux-go-run-" + rand.Text()
	if err := pane.SetHook(ctx, "pane-died", "wait-for -S "+channel); err != nil {
		return nil, err
	}
	respawn := RespawnRequest{Command: &command, Kill: true, Environment: options.Environment}
	if options.StartDirectory != "" {
		respawn.StartDirectory = &options.StartDirectory
	}
	respawned, err := pane.Respawn(ctx, respawn)
	if err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}
	return &Running{
		session:     s,
		window:      window,
		pane:        respawned,
		channel:     channel,
		fixedNotice: fixedNotice,
		keep:        options.Keep,
	}, nil
}

// Running is a command started by [Session.Start]: a real process, already
// under way, in a pane of its own. [Running.Pane] exposes that pane so its
// output can be streamed with [Pane.OpenObservation] or typed into with
// [Pane.Writer] while the command runs; [Running.Wait] and [Running.Kill]
// wait for and stop it. A line printed with nothing separating it from the
// command's own exit can race tmux's control-mode notification of it and
// never reach [PaneObservation.Reader]; [RunResult.Lines] from Wait, a plain
// screen capture rather than that notification stream, always has it.
type Running struct {
	session     Session
	window      Window
	pane        Pane
	channel     string
	fixedNotice bool
	keep        bool

	mu     sync.Mutex
	done   bool
	result RunResult
	err    error
}

// Pane returns the pane the command runs in, materialized immediately after
// Start returned. Like any materialized record it does not refresh itself; a
// call after Wait or Kill may no longer describe a live pane.
func (r *Running) Pane() Pane {
	return r.pane
}

// Wait blocks until the command exits, using tmux's own wait-for rather than
// polling, and reports its exit status and what its terminal showed. It
// removes the window Start created unless [RunOptions.Keep] was set.
//
// Wait may be called more than once and concurrently; every call observes the
// same completion. Only the first call to reach tmux drives the underlying
// wait; if its context ends first, that error is the outcome of that call
// only, and a later call starts a fresh wait, since tmux's one-shot signal is
// never delivered to a waiter that never received it. A Wait call that ends
// this way does not remove the window: a canceled Wait can always be retried,
// so only a Wait that actually observes the command's end does cleanup.
func (r *Running) Wait(ctx context.Context) (RunResult, error) {
	r.mu.Lock()
	if r.done {
		result, err := r.result.clone(), r.err
		r.mu.Unlock()
		return result, err
	}
	r.mu.Unlock()

	if err := r.session.server.WaitFor(ctx, WaitForRequest{Channel: r.channel}); err != nil {
		return RunResult{}, fmt.Errorf("wait for command: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return r.result.clone(), r.err
	}
	r.result, r.err = r.finish(ctx)
	r.done = true
	return r.result.clone(), r.err
}

// finish reads the command's outcome after tmux has signaled its pane-died
// hook and removes the window unless Keep was set.
func (r *Running) finish(ctx context.Context) (result RunResult, err error) {
	defer func() {
		if err == nil && r.keep {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if killErr := r.window.Kill(cleanupCtx); killErr != nil && err == nil {
			err = fmt.Errorf("remove window: %w", killErr)
		}
	}()
	finished, err := r.pane.Refresh(ctx)
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
	result.Lines = trimScreen(lines, r.fixedNotice)
	return result, nil
}

// StreamTo copies what the command prints into destination as it prints it and
// then reports how it ended, so following a command and collecting its outcome
// are one blocking call rather than a goroutine the caller writes. It returns
// exactly what [Running.Wait] returns and, like Wait, removes the window unless
// [RunOptions.Keep] was set. destination is written only by this call, so it
// needs no synchronization of its own, and [Running.Kill] may be called from
// another goroutine while it runs.
//
// The stream is what tmux pushed, which is not quite everything: output the
// command printed before StreamTo opened its observation may or may not be in
// it, and tmux can drop a line printed with nothing between it and the
// command's exit. [RunResult.Lines] is a screen capture rather than a
// notification stream and remains the authoritative record of what the command
// showed. Streaming needs a control client, which a [Connection]-bound value
// does not have, so it returns [ErrConnectionRequiresProcess] there; Wait alone
// does not.
func (r *Running) StreamTo(
	ctx context.Context,
	destination io.Writer,
) (RunResult, error) {
	if destination == nil {
		return RunResult{}, invalidServerCommandRequest(
			"capture-pane", "destination", "", "must not be nil",
		)
	}
	observation, err := r.pane.OpenObservation(ctx)
	if err != nil {
		return RunResult{}, fmt.Errorf("observe command: %w", err)
	}
	defer func() { _ = observation.Close() }()

	streamCtx, stopStreaming := context.WithCancel(ctx)
	defer stopStreaming()
	copied := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(destination, observation.Reader(streamCtx))
		copied <- copyErr
	}()

	result, waitErr := r.Wait(ctx)
	// The command has ended, so nothing more is coming; ending the read is what
	// lets the copy return at all, because a pane's output stream has no end of
	// its own.
	stopStreaming()
	copyErr := <-copied
	switch {
	case waitErr != nil:
		return result, waitErr
	case copyErr != nil && !errors.Is(copyErr, context.Canceled) &&
		!errors.Is(copyErr, ErrPaneObservationLost):
		return result, fmt.Errorf("stream command: %w", copyErr)
	default:
		return result, nil
	}
}

// Kill asks the tmux server to send SIGKILL to the process group running the
// command. It does not wait for the process to stop; call Wait to observe the
// resulting status or signal.
//
// Kill runs through [Server.RunShell] with the pane as its target, so tmux
// resolves the process group id itself, at the moment it runs, on whatever
// host the server is on, rather than trusting a process id this program read
// earlier. A pane that no longer exists makes that id resolve empty and Kill
// a no-op; Kill reports only whether tmux could dispatch the request, not
// whether a process still existed to receive it.
func (r *Running) Kill(ctx context.Context) error {
	if _, err := r.session.server.RunShell(ctx, RunShellRequest{
		TargetPane: r.pane.ID(),
		Command:    "kill -s KILL -- -#{pane_pid}",
	}); err != nil {
		return fmt.Errorf("kill command: %w", err)
	}
	return nil
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
