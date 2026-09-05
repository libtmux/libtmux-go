package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
)

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
