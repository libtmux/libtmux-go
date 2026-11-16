package tmux

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s Session) startManagedProgram(ctx context.Context, operands []string, options RunOptions) (running *Running, err error) {
	if err := validateTypedTarget("new-window", "Target", "session", s.ID().String()); err != nil {
		return nil, err
	}
	request := NewWindowRequest{Command: "cat"}
	if options.WindowName != "" {
		request.Name = &options.WindowName
	}
	create, err := newWindowArguments(s.ID().String(), request)
	if err != nil {
		return nil, err
	}
	respawn := RespawnRequest{Kill: true, Environment: options.Environment}
	if options.StartDirectory != "" {
		respawn.StartDirectory = &options.StartDirectory
	}
	respawnOptions, err := respawnRequestArguments("respawn-pane", respawn)
	if err != nil {
		return nil, err
	}
	marker := "libtmux-program-birth-" + rand.Text() + ":"
	for index, argument := range create {
		if argument == "-F#{window_id}" {
			create[index] = "-F" + marker + "#{window_id}|#{pane_id}|#{pane_pid}|#{window_index}|#{pane_index}"
		}
	}
	result, createErr := s.server.literalCmd(ctx, create...)
	_, createErr = requireRedactedLifecycleSuccess("new-window", result, createErr)
	pane, pid, receiptErr := programBirthReceipt(s, marker, result.Stdout)
	if receiptErr != nil {
		if createErr != nil {
			return nil, fmt.Errorf("start window: %w", createErr)
		}
		return nil, fmt.Errorf("start window: %w", receiptErr)
	}
	window := Window{server: s.server, sessionID: s.ID(), windowID: pane.windowID, windowIndex: pane.windowIndex}
	lifetime := &programLifetime{session: s, pane: pane, gate: make(chan struct{}, 1), birthPID: pid}
	channel := "libtmux-go-run-" + rand.Text()
	running = &Running{session: s, window: window, pane: pane, channel: channel, keep: options.Keep, lifetime: lifetime}
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			err = errors.Join(err, running.remove(cleanupCtx))
		}
	}()
	if createErr != nil {
		return running, createErr
	}
	target, err := exactPaneTarget(pane)
	if err != nil {
		return running, err
	}
	for _, command := range [][]string{
		{"set-option", "-t", target, "-p", "--", "remain-on-exit", RemainOnExitOn.String()},
		{"set-option", "-t", target, "-p", "--", "remain-on-exit-format", ""},
		{"set-hook", "-t", target, "-p", "--", "pane-died", "wait-for -S " + channel},
	} {
		if _, err = guardedProgramCommand(ctx, pane, lifetime.birthCondition(), command); err != nil {
			return running, err
		}
	}
	arguments := append([]string{"respawn-pane", "-t", target}, respawnOptions...)
	arguments = append(arguments, "--")
	arguments = append(arguments, operands...)
	running.pane, err = lifetime.launch(ctx, arguments)
	if err != nil {
		return running, fmt.Errorf("start command: %w", err)
	}
	return running, nil
}

// new-window prints this receipt before its after-hook can change the active
// pane. A missing receipt grants no authority to clean up an uncertain launch.
func programBirthReceipt(session Session, marker string, lines []string) (Pane, string, error) {
	var receipt []string
	for _, line := range lines {
		if value, ok := strings.CutPrefix(line, marker); ok {
			if receipt != nil {
				return Pane{}, "", ErrInvalidCommandOutput
			}
			receipt = strings.Split(value, "|")
		}
	}
	if len(receipt) != 5 {
		return Pane{}, "", ErrInvalidCommandOutput
	}
	if validateStableTarget("window", receipt[0]) != nil || validateStableTarget("pane", receipt[1]) != nil {
		return Pane{}, "", ErrInvalidCommandOutput
	}
	pid, err := strconv.Atoi(receipt[2])
	if err != nil || pid <= 0 || strconv.Itoa(pid) != receipt[2] {
		return Pane{}, "", ErrInvalidCommandOutput
	}
	windowIndex, windowErr := strconv.Atoi(receipt[3])
	paneIndex, paneErr := strconv.Atoi(receipt[4])
	if windowErr != nil || paneErr != nil || windowIndex < 0 || paneIndex < 0 {
		return Pane{}, "", ErrInvalidCommandOutput
	}
	return Pane{
		server: session.server, sessionID: session.ID(), windowID: WindowID(receipt[0]), paneID: PaneID(receipt[1]),
		windowIndex: windowIndex, paneIndex: paneIndex,
	}, receipt[2], nil
}

func (l *programLifetime) birthCondition() string {
	return "#{&&:#{==:#{pane_id}," + l.pane.ID().String() + "},#{&&:#{==:#{window_id}," + l.pane.windowID.String() + "},#{==:#{pane_pid}," + l.birthPID + "}}}"
}

func (l *programLifetime) removePlaceholder(ctx context.Context) error {
	if err := l.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-l.gate }()
	_, err := guardedProgramCommand(ctx, l.pane, l.birthCondition(), []string{"kill-pane", "-t", l.pane.ID().String()})
	if err == nil {
		l.birthPID = ""
	}
	return err
}
