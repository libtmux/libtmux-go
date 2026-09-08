package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
)

type paneInputCallerState uint8

const (
	paneInputCallerDetached paneInputCallerState = iota
	paneInputCallerUnresolved
	paneInputCallerForeign
	paneInputCallerSelected
)

type paneInputCaller struct {
	state           paneInputCallerState
	socket          string
	serverPID       uint64
	serverStartTime uint64
	sessionID       string
	paneID          string
}

type paneInputCallerPane struct {
	sessionID string
	paneID    string
}

func parsePaneInputCaller(
	tmuxVariable string,
	tmuxPresent bool,
	pane string,
	panePresent bool,
) (paneInputCaller, error) {
	if !tmuxPresent && !panePresent {
		return paneInputCaller{state: paneInputCallerDetached}, nil
	}
	if !tmuxPresent || !panePresent || tmuxVariable == "" || !canonicalPaneID(pane) {
		return paneInputCaller{}, errors.New("pane input caller context is incomplete or malformed")
	}
	sessionSeparator := strings.LastIndexByte(tmuxVariable, ',')
	pidSeparator := -1
	if sessionSeparator > 0 {
		pidSeparator = strings.LastIndexByte(tmuxVariable[:sessionSeparator], ',')
	}
	if pidSeparator <= 0 || pidSeparator+1 == sessionSeparator ||
		sessionSeparator+1 == len(tmuxVariable) {
		return paneInputCaller{}, errors.New("pane input caller context is incomplete or malformed")
	}
	socket := tmuxVariable[:pidSeparator]
	pidText := tmuxVariable[pidSeparator+1 : sessionSeparator]
	sessionText := tmuxVariable[sessionSeparator+1:]
	pid, err := parseCanonicalPaneInputNumber(pidText, false)
	if err != nil {
		return paneInputCaller{}, errors.New("pane input caller context is incomplete or malformed")
	}
	if _, err := parseCanonicalPaneInputNumber(sessionText, true); err != nil {
		return paneInputCaller{}, errors.New("pane input caller context is incomplete or malformed")
	}
	if !filepath.IsAbs(socket) || hasASCIIControl(socket) {
		return paneInputCaller{}, errors.New("pane input caller context is incomplete or malformed")
	}
	return paneInputCaller{
		state: paneInputCallerUnresolved, socket: socket, serverPID: pid,
		sessionID: "$" + sessionText, paneID: pane,
	}, nil
}

func classifyPaneInputCaller(
	caller paneInputCaller,
	server paneInputServerIdentity,
	panes []paneInputCallerPane,
) (paneInputCaller, error) {
	if caller.state == paneInputCallerDetached {
		return caller, nil
	}
	if caller.state != paneInputCallerUnresolved {
		return paneInputCaller{}, errors.New("pane input caller context is inconsistent")
	}
	sameEndpoint, err := samePaneInputEndpoint(caller.socket, server.endpoint)
	if err != nil {
		return paneInputCaller{}, fmt.Errorf("resolve pane input caller endpoint: %w", err)
	}
	if !sameEndpoint {
		caller.state = paneInputCallerForeign
		return caller, nil
	}
	if caller.serverPID != server.serverPID {
		return paneInputCaller{}, errors.New("pane input caller daemon is stale or malformed")
	}
	for _, pane := range panes {
		if pane.sessionID == caller.sessionID && pane.paneID == caller.paneID {
			caller.state = paneInputCallerSelected
			caller.socket = server.endpoint
			caller.serverStartTime = server.serverStartTime
			return caller, nil
		}
	}
	return paneInputCaller{}, errors.New("pane input caller session or pane is stale or malformed")
}

func samePaneInputEndpoint(left, right string) (bool, error) {
	if !filepath.IsAbs(left) || !filepath.IsAbs(right) {
		return false, errors.New("pane input endpoint is not absolute")
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr == nil && rightErr == nil {
		return os.SameFile(leftInfo, rightInfo), nil
	}
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	if leftErr != nil || rightErr != nil {
		return false, errors.Join(leftErr, rightErr)
	}
	return leftResolved == rightResolved, nil
}

func parseCanonicalPaneInputNumber(value string, allowZero bool) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || value != strconv.FormatUint(parsed, 10) || (!allowZero && parsed == 0) {
		return 0, errors.New("noncanonical decimal")
	}
	return parsed, nil
}

type callerIdentity struct {
	paneID string
	socket string
	inside bool
}

func callerFromEnvironment() callerIdentity {
	pane := os.Getenv("TMUX_PANE")
	tmuxVariable := os.Getenv("TMUX")
	if pane == "" || tmuxVariable == "" {
		return callerIdentity{}
	}
	socket, _, _ := strings.Cut(tmuxVariable, ",")
	return callerIdentity{paneID: pane, socket: resolvePath(socket), inside: true}
}

func (t *tools) callerIdentityFor(ctx context.Context) (callerIdentity, error) {
	t.callerMutex.Lock()
	defer t.callerMutex.Unlock()
	if t.callerCached {
		return t.caller, nil
	}
	caller := callerFromEnvironment()
	var err error
	if !caller.inside {
		caller, err = t.callerFromProcessTree(ctx)
		if err != nil {
			return callerIdentity{}, err
		}
	}
	t.caller = caller
	t.callerCached = true
	return caller, nil
}

func (t *tools) callerFromProcessTree(ctx context.Context) (callerIdentity, error) {
	ancestors := ancestorPIDs()
	if len(ancestors) == 0 {
		return callerIdentity{}, nil
	}
	process, err := t.runtime.process(ctx)
	if err != nil {
		return callerIdentity{}, err
	}
	result, err := process.Cmd(ctx, "list-panes", "-a", "-F", "#{pane_pid}|#{pane_id}")
	if err != nil {
		return callerIdentity{}, err
	}
	if result.ExitCode != 0 {
		return callerIdentity{}, &tmux.CommandError{Subcommand: "list-panes", Result: result}
	}
	for _, line := range result.Stdout {
		pid, paneID, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok {
			continue
		}
		number, err := strconv.Atoi(pid)
		if err != nil || !slices.Contains(ancestors, number) {
			continue
		}
		return callerIdentity{
			paneID: paneID, socket: resolvePath(t.socketPath(ctx)), inside: true,
		}, nil
	}
	return callerIdentity{}, nil
}

const ancestorDepth = 32

func ancestorPIDs() []int {
	pids := make([]int, 0, ancestorDepth)
	for pid := os.Getpid(); pid > 1 && len(pids) < ancestorDepth; {
		pids = append(pids, pid)
		parent, ok := parentPID(pid)
		if !ok || parent == pid {
			break
		}
		pid = parent
	}
	return pids
}

func parentPID(pid int) (int, bool) {
	output, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	parent, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, false
	}
	return parent, true
}

func resolvePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return resolved
}

func (c callerIdentity) isCaller(pane tmux.Pane, socket string) *bool {
	if !c.inside {
		return nil
	}
	answer := pane.ID().String() == c.paneID && socket != "" && resolvePath(socket) == c.socket
	return &answer
}
