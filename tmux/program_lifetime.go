package tmux

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// A pending pane-created waiter attests that no later respawn occurred.
// Consumption and the protected operation share one daemon command group.
type programWitness struct {
	client   *ControlClient
	pool     *controlLanePool
	finished chan struct{}
	progress chan struct{}
	err      error
}

type programLifetime struct {
	session Session
	pane    Pane
	gate    chan struct{}
	current *programWitness
}

func openProgramWitness(ctx context.Context, session Session, pane Pane, launch []string) (*programWitness, error) {
	server := session.server
	var client *ControlClient
	var pool *controlLanePool
	var err error
	if server.connection != nil {
		pool = server.connection.pool
		client, err = pool.open(ctx)
		if err == nil && !pool.adopt(client) {
			return nil, errors.Join(ErrControlClosed, client.Close())
		}
	} else {
		client, err = server.openControl(ctx, session, controlCommands)
	}
	if err != nil {
		return nil, err
	}
	witness := &programWitness{
		client: client, pool: pool, finished: make(chan struct{}),
		progress: make(chan struct{}, 1),
	}
	wait := []string{
		"wait-for", "-E", "-F",
		"#{==:#{pane}," + pane.ID().String() + "}", "pane-created",
	}
	line, err := encodeControlCommand(wait, false)
	if err != nil {
		return nil, errors.Join(err, witness.close())
	}
	arguments := slices.Clone(wait)
	if len(launch) != 0 {
		launchLine, encodeErr := encodeControlCommand(launch, false)
		if encodeErr != nil {
			return nil, errors.Join(encodeErr, witness.close())
		}
		line = launchLine + " ; " + line
		arguments = append(append(slices.Clone(launch), ";"), wait...)
	}
	request := &controlRequest{
		ctx: context.WithoutCancel(ctx), command: arguments, line: line,
		response: make(chan controlResponse, 1), progress: witness.progress,
	}
	go func() {
		results, callErr := client.submitControlRequest(request)
		if callErr == nil {
			for _, result := range results {
				if result.Failed {
					callErr = newRedactedCommandError("program", controlCommandResults(arguments, results))
					break
				}
			}
		}
		witness.err = callErr
		close(witness.finished)
	}()
	for {
		select {
		case <-ctx.Done():
			return witness, errors.Join(ctx.Err(), witness.close())
		case <-witness.finished:
			return witness, errors.Join(ErrProgramReplaced, witness.err, witness.close())
		case <-witness.progress:
			pending, probeErr := witness.pending(ctx, server)
			if probeErr != nil {
				return witness, errors.Join(probeErr, witness.close())
			}
			if pending {
				return witness, nil
			}
		}
	}
}

func (w *programWitness) pending(ctx context.Context, server Server) (bool, error) {
	select {
	case <-w.finished:
		return false, errors.Join(ErrProgramReplaced, w.err)
	default:
	}
	result, err := server.literalCmd(ctx, "wait-for", "-E", "-l", "pane-created")
	if err := requireRedactedServerCommandNoStderr("wait-for", result, err); err != nil {
		return false, err
	}
	return slices.Contains(result.Stdout, w.client.clientName.String()), nil
}

func (w *programWitness) close() error {
	if w.pool != nil {
		return w.pool.retire(w.client)
	}
	return w.client.Close()
}

func (l *programLifetime) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case l.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *programLifetime) changed(ctx context.Context) (<-chan struct{}, error) {
	if err := l.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-l.gate }()
	if l.current == nil {
		return nil, ErrProgramReplaced
	}
	return l.current.finished, nil
}

func (l *programLifetime) check(ctx context.Context) error {
	if err := l.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-l.gate }()
	if l.current == nil {
		return ErrProgramReplaced
	}
	pending, err := l.current.pending(ctx, l.session.server)
	if err != nil || !pending {
		if err == nil {
			err = ErrProgramReplaced
		}
		if errors.Is(err, ErrProgramReplaced) {
			err = errors.Join(err, l.current.close())
			l.current = nil
		}
		return err
	}
	return nil
}

func (l *programLifetime) operation(ctx context.Context, rollover bool, arguments ...string) (result CommandResult, err error) {
	if err := l.acquire(ctx); err != nil {
		return CommandResult{}, err
	}
	defer func() { <-l.gate }()
	old := l.current
	if old == nil {
		return CommandResult{}, ErrProgramReplaced
	}
	select {
	case <-old.finished:
		l.current = nil
		return CommandResult{}, errors.Join(ErrProgramReplaced, old.err, old.close())
	default:
	}
	var next *programWitness
	if rollover {
		var err error
		next, err = openProgramWitness(ctx, l.session, l.pane, nil)
		if err != nil {
			return CommandResult{}, err
		}
	}
	l.current = next
	defer func() { err = errors.Join(err, old.close()) }()
	marker := "__libtmux_program_moved_" + rand.Text()
	condition := "#{==:#{window_id}," + l.pane.windowID.String() + "}"
	group := []string{
		"if-shell", "-F", "-t", l.pane.ID().String(), condition, "", marker, ";",
		"wait-for", "-E", "-w", old.client.clientName.String(), "pane-created",
	}
	subcommand := "wait-for"
	if len(arguments) != 0 {
		subcommand = arguments[0]
		group = append(group, ";")
		if !l.session.server.ConnectionBound() {
			arguments = escapeCommandListSeparators(arguments)
		}
		group = append(group, arguments...)
	}
	result, err = l.session.server.Cmd(ctx, group...)
	for _, line := range result.Stderr {
		if line == "unknown command: "+marker || (strings.HasPrefix(line, "waiter ") && strings.HasSuffix(line, " not found")) {
			var closeErr error
			if next != nil {
				closeErr = next.close()
				l.current = nil
			}
			return result, errors.Join(ErrProgramReplaced, closeErr)
		}
	}
	return result, requireRedactedServerCommandNoStderr(subcommand, result, err)
}

func launchProgram(ctx context.Context, session Session, pane Pane, request RespawnRequest, operands []string) (*programLifetime, Pane, error) {
	target, err := exactPaneTarget(pane)
	if err != nil {
		return nil, Pane{}, err
	}
	arguments, err := respawnArguments("respawn-pane", target, request)
	if err != nil {
		return nil, Pane{}, err
	}
	arguments = append(arguments, "--")
	arguments = append(arguments, operands...)
	lifetime := &programLifetime{session: session, pane: pane, gate: make(chan struct{}, 1)}
	observer := session.server.commandObserver()
	var started time.Time
	if observer != nil {
		started = time.Now()
	}
	witness, err := openProgramWitness(ctx, session, pane, arguments)
	if observer != nil {
		exitCode := 0
		if err != nil {
			exitCode = -1
			if commandError, ok := errors.AsType[*CommandError](err); ok {
				exitCode = commandError.Result.ExitCode
			}
		}
		observeCommand(observer, arguments, started, CommandTransportConnection, exitCode, err)
	}
	lifetime.current = witness
	if witness == nil {
		return nil, pane, err
	}
	if err != nil {
		return lifetime, pane, fmt.Errorf("observe program launch: %w", err)
	}
	refreshed, err := pane.Refresh(ctx)
	return lifetime, refreshed, err
}
