package tmux

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// paneExitDrainPipeVersion is the first release that drains a pane's pending
// terminal bytes on exit without an open pipe. It is a repair applied
// internally, not something a caller can request or be refused, so it has no
// capability record.
var paneExitDrainPipeVersion = Version{raw: "3.7", major: 3, minor: 7}

// RunOptions configures [Session.Run], [Session.Start], [Session.RunProgram]
// and [Session.StartProgram]. Its zero value
// runs the command in a window tmux names, in the session's default directory
// and environment, and removes that window once the command has finished.
type RunOptions struct {
	// WindowName names the window the command runs in; empty lets tmux choose.
	WindowName string
	// StartDirectory expands ~ and ~/... for the current user; empty inherits
	// the session's default.
	// A directory that does not exist is not an error: tmux starts the
	// process in the home directory instead, or in / without one.
	StartDirectory string
	// Environment is added to the command's environment. The map is not
	// retained.
	Environment map[string]string
	// Keep leaves the finished window in place, showing what the command
	// printed, instead of removing it.
	Keep bool
}

// ErrOutcomeUnrecorded reports that a command's pane is dead while tmux has
// not recorded how the command ended, which it does by reaping the command's
// process. tmux is known to leave a pane in that state indefinitely, so this
// is reported rather than waited out, and rather than passed off as a zero
// exit status that would call a failed command successful.
var ErrOutcomeUnrecorded = errors.New("tmux: command outcome was never recorded")

// ErrProgramReplaced reports a changed pane generation or loss of its lifetime
// witness. The owner refuses input and destruction without that evidence.
var ErrProgramReplaced = errors.New("tmux: program generation or placement was lost")

// RunResult reports how a command finished and what its terminal showed.
type RunResult struct {
	// Status is the command's exit status, or -1 when the command did not
	// exit on its own, as when a signal ended it, so zero always means it
	// succeeded. os.ProcessState.ExitCode reads the same way.
	Status int
	// Signal is the signal that ended the command, when one did, and nil
	// otherwise. Compare it with os.Kill or a syscall constant: tmux reports
	// a number on Linux and a name on macOS and the BSDs, and Signal holds
	// the same value on each.
	Signal os.Signal
	// Lines is what the terminal showed when the command finished, its
	// history included, without the blank lines tmux pads a screen with. A
	// line the window's width wrapped is joined back into one, and each line
	// ends at its last character that is not a space: a screen cannot tell
	// spaces a program wrote from the blank cells after them. tmux keeps at
	// most the session's history-limit lines above the screen, 2000 by
	// default, so the start of a longer output is gone.
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

// deadSignal turns tmux's pane_dead_signal into the signal it reports. A
// name this platform has no number for keeps tmux's spelling.
func deadSignal(reported string) os.Signal {
	if reported == "" {
		return nil
	}
	if signal, ok := platformSignal(reported); ok {
		return signal
	}
	return reportedSignal(reported)
}

// reportedSignal is a signal tmux named that this platform has no number for.
type reportedSignal string

func (s reportedSignal) String() string { return string(s) }

// Signal makes reportedSignal an [os.Signal].
func (reportedSignal) Signal() {}

// Run starts command in a new window of this session, waits for it to finish,
// and reports its exit status and what its terminal showed. It is [os/exec]
// for a program that needs a terminal: the command runs with a tty, so it
// sees isatty, colour, and a width, and what comes back is the screen tmux
// rendered rather than the bytes the program wrote. That screen holds a
// bounded history; see [RunResult.Lines]. For a large output, have the
// command write it to a file, or follow it with [Running.StreamTo].
//
// tmux runs command as default-shell -c command, so it is written in the
// language of the session's default-shell option, which tmux takes from
// $SHELL when it starts the server: under fish, echo $((1+2)) is an error.
// For one language on every machine, set default-shell, or write the
// command as sh -c '...'.
//
// A nonzero status is a result, not an error; the error reports what tmux
// could not do. The wait is tmux's own signal, backed by a liveness check that
// widens from a quarter second in case that signal is lost: it needs no tmux
// binary on the command's PATH and returns as the process exits. A command that never
// exits holds Run until ctx ends. On error, Run removes its window regardless
// of [RunOptions.Keep], since Run gives the caller no other chance to retry or
// clean up; a bare [Session.Start] followed by [Running.Wait] leaves that
// choice to the caller instead. [Server.RunShell] runs a command with no pane
// at all.
//
// On a session bound to a [Connection], Run works as it does elsewhere: the
// wait uses a control client of its own rather than a lane, so one lane is
// enough, and the final screen is read through a scratch tmux buffer and a
// temporary file that tmux writes and this process reads.
func (s Session) Run(ctx context.Context, command string, options RunOptions) (RunResult, error) {
	running, err := s.Start(ctx, command, options)
	return waitForRun(ctx, running, err)
}

func waitForRun(ctx context.Context, running *Running, err error) (RunResult, error) {
	if err != nil {
		if running != nil && running.lifetime != nil {
			return RunResult{Status: -1, Pane: running.pane.ID()}, err
		}
		return RunResult{}, err
	}
	result, err := running.Wait(ctx)
	if err != nil {
		if running.lifetime != nil && result.Pane == "" {
			result = RunResult{Status: -1, Pane: running.pane.ID()}
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		cleanupErr := running.remove(cleanupCtx)
		if running.lifetime != nil {
			err = errors.Join(err, cleanupErr)
		}
	}
	return result, err
}

// RunProgram runs an executable with literal arguments in a new terminal window.
// It requires the program-lifetime capability (tmux 3.8). Nonzero exits are
// results; terminal stdout and stderr are merged. Capture and stream limits
// are those of [RunResult] and [Running.StreamTo].
//
// Cancellation attempts bounded pane/PTY cleanup; an application that ignores
// terminal hangup may survive. Use [Session.StartProgram], [Running.Kill] and
// [Running.Wait] to observe the main process's exit. Cleanup refuses a changed
// generation with [ErrProgramReplaced], preserving the replacement.
// A partial result identifies the intended pane for inspection after failure.
func (s Session) RunProgram(ctx context.Context, executable string, arguments []string, options RunOptions) (RunResult, error) {
	running, err := s.StartProgram(ctx, executable, arguments, options)
	return waitForRun(ctx, running, err)
}

// StartProgram starts an executable with literal arguments and returns its owner.
// The executable resolves on the tmux server's PATH in the requested directory.
// Arguments are copied before I/O; NUL and an empty executable fail before
// creation. A singleton uses /bin/sh with a fixed positional exec script.
// Argument values never become shell source.
//
// The program-lifetime capability requires tmux 3.8, a filesystem that can
// hard-link the tmux socket, and capture files readable by this process and
// writable by the daemon. One control client stays open until Wait
// finishes or ownership is lost; Kill alone does not release that observation.
// A canceled Wait leaves the program available for another wait. Keep retains
// the completed pane; cleanup preserves other panes added to its window.
//
// Respawn, movement or observation loss makes owned input and destruction refuse
// with ErrProgramReplaced. On uncertain launch, a nonnil Running may accompany
// the error: inspect its Pane before choosing manual recovery. A dropped handle
// or a crashed controller supplies no automatic application cleanup.
// A failed final capture retains its private file and reports its path through
// [os.PathError]: an in-flight daemon write may still reach that file.
func (s Session) StartProgram(ctx context.Context, executable string, arguments []string, options RunOptions) (*Running, error) {
	operands, err := programArguments(executable, arguments)
	if err != nil {
		return nil, err
	}
	capability, err := s.server.Capability(ctx, "program-lifetime")
	if err != nil {
		return nil, err
	}
	if !capability.Supported {
		return nil, &VersionTooLowError{Current: capability.Current, Minimum: capability.Since, Subcommand: "respawn-pane", Feature: capability.ID}
	}
	return s.start(ctx, RespawnRequest{}, operands, options)
}

// The sentinel preserves filename newlines that command substitution trims.
const singletonProgramExec = `case "$1" in
-*)
    p=$(command -v -- "$1" && printf .) || exit 127
    p=${p%.}
    p=${p%?}
    case "$p" in -*) p=./$p;; esac
    shift
    exec "$p" "$@"
    ;;
*) exec "$@";;
esac`

func programArguments(executable string, arguments []string) ([]string, error) {
	if executable == "" {
		return nil, invalidServerCommandRequest("respawn-pane", "Executable", "", "is required")
	}
	operands := append([]string{executable}, arguments...)
	for index, operand := range operands {
		if err := validateServerCommandArgument("respawn-pane", fmt.Sprintf("Argv[%d]", index), operand, true); err != nil {
			return nil, err
		}
	}
	if len(operands) == 1 {
		return []string{"/bin/sh", "-c", singletonProgramExec, "libtmux-program", executable}, nil
	}
	return operands, nil
}

// Start runs command in a new window of this session and returns once it is
// under way, without waiting for it to finish. The returned [Running] reports
// the pane it runs in and blocks on demand: [Running.Wait] waits for the exit
// status and screen [Session.Run] would have returned, and [Running.Kill]
// stops the command. Options are exactly Run's.
//
// Before tmux 3.7, Start attempts to install an output pipe running cat
// unless the pane already has a pipe; a failed attempt only forfeits that
// compatibility help. [Running.Wait] stops a pipe Start installed once the
// outcome is read, whether or not Keep leaves the window itself in place.
//
// If Wait is never called, the window Start created is never removed.
//
// A session bound to a [Connection] can Start: [Running.Wait] holds a control
// client of its own, not a lane, so [Running.Kill] and every other command stay
// available while it waits.
func (s Session) Start(
	ctx context.Context,
	command string,
	options RunOptions,
) (running *Running, err error) {
	if command == "" {
		return nil, invalidServerCommandRequest("respawn-pane", "Command", "", "is required")
	}
	return s.start(ctx, RespawnRequest{Command: &command}, nil, options)
}

func (s Session) start(ctx context.Context, respawn RespawnRequest, operands []string, options RunOptions) (running *Running, err error) {
	if operands != nil {
		return s.startManagedProgram(ctx, operands, options)
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
	pane, err := window.ResolveActivePane(ctx)
	if err != nil {
		return nil, err
	}
	if err := pane.SetRemainOnExit(ctx, RemainOnExitOn); err != nil {
		return nil, err
	}
	// An empty notice keeps tmux's status text out of captured command output.
	if err := pane.SetRemainOnExitFormat(ctx, ""); err != nil {
		return nil, err
	}
	version, err := s.server.Version(ctx)
	if err != nil {
		return nil, err
	}
	// Before 3.7, tmux drains pending terminal bytes on exit only when a
	// pipe is open. Failing to install one only forfeits that compatibility
	// help, so it is tolerated rather than failing Start; installedPipe
	// records the attempt's own result so finish stops only the pipe it
	// opened, never one a caller's own hook installed.
	var installedPipe bool
	if piping, _ := pane.Piping(); !piping && !version.AtLeast(paneExitDrainPipeVersion) {
		installedPipe = pane.Pipe(ctx, PipePaneRequest{Command: new("exec cat")}) == nil
	}
	// Channels are server-global, and tmux keeps a signal nobody is waiting
	// for until the next waiter takes it, so each run owns a fresh name.
	channel := "libtmux-go-run-" + rand.Text()
	if err := pane.SetHook(ctx, "pane-died", "wait-for -S "+channel); err != nil {
		return nil, err
	}
	respawn.Kill = true
	respawn.Environment = options.Environment
	if options.StartDirectory != "" {
		respawn.StartDirectory = &options.StartDirectory
	}
	respawned, err := pane.Respawn(ctx, respawn)
	if err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}
	return &Running{session: s, window: window, pane: respawned, channel: channel, keep: options.Keep, installedPipe: installedPipe}, nil
}

// Running is a command started by [Session.Start] or [Session.StartProgram]:
// a real process in a pane of its own. [Running.Pane] exposes that pane for
// [Pane.OpenObservation]; [Running.Writer], [Running.SendKeys] and
// [Running.SendKeySequence] deliver input while the command runs.
// [Running.Wait] and [Running.Kill]
// wait for and stop it. A line printed with nothing separating it from the
// command's own exit can race tmux's control-mode notification of it and
// never reach [PaneObservation.Reader]. [RunResult.Lines] from Wait captures
// the final rendered screen and bounded history.
type Running struct {
	session       Session
	window        Window
	pane          Pane
	channel       string
	keep          bool
	installedPipe bool
	lifetime      *programLifetime
	// settle answers how long to let tmux catch up before reporting that it
	// never recorded an outcome. Nil uses settleLimit; a test supplies one to
	// drive waitForExit without waiting out a real deadline.
	settle func(context.Context) time.Duration

	// exited records that some Wait saw the command end; finishing reads the
	// outcome once after that, and finished closes when result and err hold it.
	exited    atomic.Bool
	finishing sync.Once
	mu        sync.Mutex
	finished  chan struct{}
	result    RunResult
	err       error
}

// Pane returns the pane the command runs in, materialized immediately after
// Start returned. Like any materialized record it does not refresh itself; a
// call after Wait or Kill may no longer describe a live pane.
// Pane methods operate on the raw tmux object without the program's generation
// admission. Use Running's input methods to preserve managed ownership.
func (r *Running) Pane() Pane {
	return r.pane
}

// Wait blocks until the command exits, on tmux's own wait-for signal backed by
// a widening liveness check, and reports its exit status and what its terminal
// showed. Unless [RunOptions.Keep] was set, Start removes its window and
// StartProgram removes its owned pane, preserving panes added by others.
// A command whose process tmux never reaps has no outcome to report, and
// Wait ends with [ErrOutcomeUnrecorded] rather than the zero status that would
// make a failed command look successful.
//
// Wait may be called more than once and concurrently; every call observes the
// same completion. The outcome is read once, as soon as any call sees the
// command end, on a context no caller's cancellation reaches, because from
// then on it belongs to every caller. A call whose context ends first reports
// that context and leaves the window alone; a later call gets the outcome.
//
// On a [Connection]-bound session the wait holds a control client of its own
// rather than a lane, and ending it closes that client. Closing the
// connection ends it with [ErrControlClosed].
func (r *Running) Wait(ctx context.Context) (RunResult, error) {
	finished := r.finishedSignal()
	if !r.exited.Load() {
		if err := r.waitForExit(ctx); err != nil {
			// Another Wait may have claimed the command's end while this one
			// was waiting: exited is stored before that claim's own tmux
			// round trips run, so seeing it true here means this call's
			// failure is that claim's cleanup making the pane look gone,
			// not a real outcome of its own. That completion is the answer
			// every caller shares once claimed, however long finishing it
			// takes, so this waits for it rather than reporting the
			// cleanup's side effect as this call's own failure.
			if r.exited.Load() {
				select {
				case <-finished:
					return r.outcome()
				case <-ctx.Done():
					return RunResult{}, fmt.Errorf("wait for command: %w", context.Cause(ctx))
				}
			}
			return RunResult{}, fmt.Errorf("wait for command: %w", err)
		}
	}
	r.finishing.Do(func() {
		r.exited.Store(true)
		go func() {
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishLimit)
			defer cancel()
			result, err := r.finish(finishCtx)
			r.mu.Lock()
			r.result, r.err = result, err
			r.mu.Unlock()
			close(finished)
		}()
	})
	select {
	case <-finished:
		return r.outcome()
	case <-ctx.Done():
		return RunResult{}, fmt.Errorf("wait for command: %w", context.Cause(ctx))
	}
}

// finishLimit bounds reading an ended command's outcome: a handful of tmux
// commands, which only a server that has stopped answering takes this long.
const finishLimit = 10 * time.Second

// finishedSignal returns the channel closed once the outcome is stored.
func (r *Running) finishedSignal() chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished == nil {
		r.finished = make(chan struct{})
	}
	return r.finished
}

// outcome returns the stored outcome as a copy the caller owns.
func (r *Running) outcome() (RunResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result.clone(), r.err
}

// livenessDelay bounds how long waitForExit trusts tmux's signal before it
// asks whether the pane is already dead, and how far that interval backs off.
// The first check is soon enough to rescue a short command whose signal was
// missed; the ceiling keeps a long command from being polled.
const (
	initialLivenessDelay = 250 * time.Millisecond
	maximumLivenessDelay = 30 * time.Second
)

// outcomeSettleDelay is how soon a pane found dead is re-read while tmux has
// yet to record how its command ended, and the first of a series that doubles
// from there; outcomeReapDelay is how long the settling may take before the
// server is asked to reap, and outcomeSettleLimit how long the whole wait is
// worth before reporting ErrOutcomeUnrecorded. Both are far past the moment
// tmux normally needs and are none of a healthy command's time.
// outcomeSettleLimit is a floor rather than a ceiling: a caller who gave ctx
// a longer deadline asked to wait that long, and a loaded machine is exactly
// where tmux takes more than five seconds to reap.
const (
	outcomeSettleDelay = 20 * time.Millisecond
	outcomeReapDelay   = 200 * time.Millisecond
	outcomeSettleLimit = 5 * time.Second
	// maximumSettleDelay caps the backoff, so a wait bounded by a long
	// deadline still asks often enough to answer soon after tmux catches up.
	maximumSettleDelay = time.Second
)

// signalHandoverDelay is how long the liveness check waits for tmux's own
// signal once it has found the command's outcome readable. tmux records the
// outcome as it reaps the command and signals afterwards, from the point where
// it has finished with the pane's terminal, so the screen the command printed
// arrives between the two.
const signalHandoverDelay = 500 * time.Millisecond

// waitForExit returns when tmux signals the pane's death, or when the pane is
// found already dead. The signal is edge-triggered and arrives once, so a wait
// that trusted it alone would hang for as long as ctx allowed if it never
// came; the liveness check is the second exit, and it backs off so a command
// that runs for an hour is asked about a handful of times.
func (r *Running) waitForExit(ctx context.Context) error {
	if err := r.checkProgram(ctx); err != nil {
		return err
	}
	signaled := make(chan error, 1)
	waitCtx, endWait := context.WithCancel(ctx)
	defer endWait()
	go func() {
		signaled <- r.session.server.WaitFor(waitCtx, WaitForRequest{Channel: r.channel})
	}()

	// Read once. Recomputing it each pass would compare a limit shrinking with
	// the deadline against a total growing toward it, and the two would meet
	// at half the time the caller allowed.
	limit := r.settleFor(ctx)
	delay := initialLivenessDelay
	// settleDelay doubles from half a step, so the first settle waits one.
	settleDelay := outcomeSettleDelay / 2
	var settling time.Duration
	var asked bool
	for {
		var changed <-chan struct{}
		if r.lifetime != nil {
			var err error
			changed, err = r.lifetime.changed(ctx)
			if err != nil {
				return err
			}
		}
		timer := time.NewTimer(delay)
		select {
		case err := <-signaled:
			timer.Stop()
			return err
		case <-ctx.Done():
			timer.Stop()
			return context.Cause(ctx)
		case <-changed:
			timer.Stop()
			if err := r.checkProgram(ctx); err != nil {
				return err
			}
			continue
		case <-timer.C:
		}
		pane, err := r.pane.Refresh(ctx)
		if err != nil {
			return err
		}
		if err := r.checkProgram(ctx); err != nil {
			return err
		}
		if dead, ok := pane.Dead(); ok && dead {
			if outcomeRecorded(pane) {
				return awaitDeathSignal(ctx, signaled)
			}
			if settling >= limit {
				return fmt.Errorf("%w: pane %s is dead and its command unreaped",
					ErrOutcomeUnrecorded, pane.ID())
			}
			if settling >= outcomeReapDelay && !asked {
				asked = true
				r.askForAReap(ctx)
			}
			// tmux closes a pane's terminal before it reaps the command,
			// and reports pane_dead from that closed descriptor alone, so
			// reading the outcome now would call a command that exited 7 a
			// command that exited 0. Wait for tmux to catch up, asking less
			// often the longer it takes: tmux normally needs one or two of
			// these, and a caller who allowed minutes should not spend them
			// listing panes fifty times a second.
			//
			// Settling is timed on its own. The liveness backoff before it
			// grows to half a minute for a long command, and charging that
			// to the first settle would spend the whole allowance on the
			// wait that found the pane dead - the reap nudge below included.
			settleDelay = nextSettleDelay(settleDelay)
			settling += settleDelay
			delay = settleDelay
			continue
		}
		delay = min(delay*2, maximumLivenessDelay)
	}
}

// nextSettleDelay is how long to wait before asking tmux again whether it has
// recorded an outcome. It doubles, so a pane tmux is slow to reap is asked
// about a handful of times rather than fifty times a second, and stops
// doubling at maximumSettleDelay, so a wait bounded by a long deadline still
// answers soon after tmux catches up.
func nextSettleDelay(current time.Duration) time.Duration {
	return min(current*2, maximumSettleDelay)
}

// settleLimit is how long to let tmux catch up before reporting that it never
// recorded an outcome. A caller whose ctx runs longer than the built-in floor
// gets its whole remaining time, because a deadline is the caller saying how
// long the answer is worth. A shorter deadline still ends the wait: ctx.Done
// is selected on alongside this.
func settleLimit(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return outcomeSettleLimit
	}
	return max(outcomeSettleLimit, time.Until(deadline))
}

// settleFor answers this run's settle limit.
func (r *Running) settleFor(ctx context.Context) time.Duration {
	if r.settle != nil {
		return r.settle(ctx)
	}
	return settleLimit(ctx)
}

// outcomeRecorded reports whether tmux has recorded how the command in pane
// ended. A pane reads as dead as soon as tmux closes its terminal, which it
// does before reaping the command: server_destroy_pane closes the descriptor
// and only then returns early when the status is not ready, and through tmux
// 3.5a pane_dead is that closed descriptor and nothing more. Either field
// being readable is proof the command has been reaped.
func outcomeRecorded(pane Pane) bool {
	if _, ok := pane.DeadStatus(); ok {
		return true
	}
	_, ok := pane.DeadSignal()
	return ok
}

// awaitDeathSignal hands a wait that found the pane finished back to tmux's
// own signal, which says more: tmux sends it once it has finished with the
// pane's terminal, and the last of what the command printed reaches the screen
// on the way there. The liveness check is there to notice a signal that never
// comes, not to answer ahead of one that is on its way, so it gives up only
// after a delay no healthy pane needs.
func awaitDeathSignal(ctx context.Context, signaled <-chan error) error {
	timer := time.NewTimer(signalHandoverDelay)
	defer timer.Stop()
	select {
	case err := <-signaled:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

// askForAReap gives the server one more child to notice, which is what makes
// it collect the one it has already lost.
//
// tmux reads a command's exit status only in the waitpid drain that a SIGCHLD
// sends it through, and a server can miss that signal: a tmux linked against
// libutempter forks a helper to update utmp as a pane's terminal closes and
// raises SIGCHLD at itself afterwards to cover what the helper disturbs, which
// is the shape of the loss seen here -- the command left a zombie child of the
// server, pane_dead_status never appeared, and neither did the pane-died hook.
// The drain is unconditional and takes every child that is ready, so any later
// child death runs it again and collects the stalled one with it. This asks
// for that by the shortest command tmux will run for us, and ignores its own
// outcome: the wait's next read of the pane is the only answer that counts.
func (r *Running) askForAReap(ctx context.Context) {
	_, _ = r.session.server.Cmd(ctx, "run-shell", "-b", "true")
}

// finish reads the command's outcome after tmux has signaled its pane-died
// hook and removes the window unless Keep was set. A Keep'd window that Start
// piped for compatibility keeps that pipe only until here: its job is done
// once the outcome is read, and a caller can hold the window indefinitely.
func (r *Running) finish(ctx context.Context) (result RunResult, err error) {
	defer func() {
		if errors.Is(err, ErrProgramReplaced) {
			return
		}
		if r.keep && r.lifetime != nil {
			_, releaseErr := r.lifetime.operation(ctx, false)
			err = errors.Join(err, releaseErr)
			return
		}
		if r.keep {
			// Nothing else ever stops a compatibility pipe on a kept window,
			// so it runs on every path out of this function, not only the one
			// that reads the outcome cleanly.
			if r.installedPipe {
				stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
				defer cancel()
				_ = r.pane.Pipe(stopCtx, PipePaneRequest{})
			}
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if killErr := r.remove(cleanupCtx); killErr != nil {
			if r.lifetime != nil {
				err = errors.Join(err, fmt.Errorf("remove pane: %w", killErr))
			} else if err == nil {
				err = fmt.Errorf("remove window: %w", killErr)
			}
		}
	}()
	finished, err := r.pane.Refresh(ctx)
	if err != nil {
		return RunResult{}, err
	}
	if err := r.checkProgram(ctx); err != nil {
		return RunResult{}, err
	}
	result.Pane = finished.ID()
	// A signal-killed pane reports pane_dead_signal and no pane_dead_status, so
	// a missing status is the documented Signal case rather than an error.
	result.Status = -1
	if status, ok := finished.DeadStatus(); ok {
		result.Status = status
	}
	reported, _ := finished.DeadSignal()
	result.Signal = deadSignal(reported)
	lines, captureErr := captureFinishedScreen(ctx, finished, r.lifetime != nil)
	if err := r.checkProgram(ctx); err != nil {
		return RunResult{}, errors.Join(captureErr, err)
	}
	result.Lines = trimScreen(lines)
	if captureErr != nil {
		if r.lifetime == nil {
			result = RunResult{}
		}
		return result, fmt.Errorf("read screen: %w", captureErr)
	}
	return result, nil
}

// Owned programs and connections stage captures through a buffer/file, so hook
// output and application text resembling control frames cannot become results.
func captureFinishedScreen(ctx context.Context, pane Pane, owned bool) (lines []string, err error) {
	request := CapturePaneRequest{
		Start: CaptureBoundary, End: CaptureBoundary, JoinWrapped: true,
	}
	if !owned && !pane.server.ConnectionBound() {
		return pane.Capture(ctx, request)
	}
	file, err := os.CreateTemp("", "libtmux-go-run-*")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	retained := false
	defer func() {
		if retained {
			return
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove capture file: %w", removeErr))
		}
	}()
	if err := file.Close(); err != nil {
		return nil, err
	}
	lines, err = pane.CaptureToFile(ctx, path, request)
	if err != nil {
		// Removing an uncertain write's destination could let tmux recreate it
		// with its own, less restrictive umask.
		retained = true
		err = errors.Join(err, &os.PathError{Op: "retained capture", Path: path, Err: ErrOutcomeUnknown})
	}
	return lines, err
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
// does not have, so it returns [ErrConnectionRequiresProcess] there before
// waiting and leaves the command running; Wait alone does not.
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
	// A destination that blocks regardless of cancellation must not hold this
	// call: ending the read does not end a stuck Write.
	copyErr := awaitCopy(ctx, copied)
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
//
// A StartProgram owner first consumes its pending generation witness in the
// same daemon command group. Respawn or lost observation returns ErrProgramReplaced.
// tmux expands the process-group ID before dispatching the background signal
// job; kernel PID reuse and descendants that leave the group remain outside
// this guarantee. Once Wait has cached completion, Kill has no effect.
func (r *Running) Kill(ctx context.Context) error {
	if r.lifetime != nil {
		if r.exited.Load() {
			_, err := r.outcome()
			return err
		}
		_, err := r.lifetime.operation(ctx, true, "run-shell", "-b", "-t", r.pane.ID().String(), "--", `[ -n "#{pane_pid}" ] && kill -s KILL -- -#{pane_pid} 2>/dev/null; true`)
		return err
	}
	if _, err := r.session.server.RunShell(ctx, RunShellRequest{
		TargetPane: r.pane.ID(),
		Background: true,
		// Nothing to kill is not a failure. Background never reports this
		// command's exit to the caller, but tmux still posts a nonzero one as
		// a message once the job finishes, so a pane that has gone must leave
		// this exiting zero rather than surfacing kill's own failure.
		Command: "[ -n \"#{pane_pid}\" ] && kill -s KILL -- -#{pane_pid} 2>/dev/null; true",
	}); err != nil {
		return fmt.Errorf("kill command: %w", err)
	}
	return nil
}

func (r *Running) checkProgram(ctx context.Context) error {
	if r.lifetime == nil {
		return nil
	}
	return r.lifetime.check(ctx)
}

func (r *Running) remove(ctx context.Context) error {
	if r.lifetime == nil {
		return r.window.Kill(ctx)
	}
	if r.lifetime.birthPID != "" {
		return r.lifetime.removePlaceholder(ctx)
	}
	_, err := r.lifetime.operation(ctx, false, "kill-pane", "-t", r.pane.ID().String())
	return err
}

// trimScreen drops the blank lines tmux pads a screen to its height with.
func trimScreen(lines []string) []string {
	// Joining wrapped lines keeps the spaces that pad each row out to the
	// window's width, which some tmux versions then leave in place.
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " ")
	}
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}
