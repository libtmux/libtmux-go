package cli

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
)

type loadHandoff struct {
	client   tmux.Client
	terminal *os.File
	input    *os.File
}

func (r *invocation) prepareHandoff(server tmux.Server) (*loadHandoff, error) {
	if r.terminalInput == nil || !terminal(r.terminalInput) {
		return nil, &failure{"terminal_required", "attach requires terminal stdin; use -d", 2}
	}
	if os.Getenv("TMUX") == "" {
		file, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return nil, &failure{"terminal_required", "attach requires a controlling terminal; use -d", 2}
		}
		return &loadHandoff{terminal: file, input: r.terminalInput}, nil
	}
	snapshot, pane, err := currentView(r.ctx, server)
	if err != nil {
		return nil, err
	}
	tty, ok := pane.TTY()
	if !ok {
		return nil, usage("current pane has no terminal; use -d")
	}
	paneInfo, paneErr := os.Stat(tty)
	inputInfo, inputErr := r.terminalInput.Stat()
	if paneErr != nil || inputErr != nil || !os.SameFile(paneInfo, inputInfo) {
		return nil, usage("TMUX_PANE does not identify terminal stdin; use -d")
	}
	var selected tmux.Client
	for _, client := range snapshot.Clients() {
		control, queried := client.ControlMode()
		clientTTY, hasTTY := client.TTY()
		window, hasWindow := client.Formats().WindowID()
		if !queried || control || !hasTTY || clientTTY == "" || !hasWindow || window != pane.WindowID() {
			continue
		}
		flags, hasFlags := client.Flags()
		if !hasFlags || contains(strings.Split(flags, ","), "active-pane") {
			return nil, usage("cannot identify independent active-pane client focus; use -d or --append")
		}
		clientPane, hasPane := client.Formats().PaneID()
		if !hasPane || clientPane != pane.ID() {
			continue
		}
		if selected.Name() != "" {
			return nil, usage("multiple terminal clients view this pane; use -d or --append")
		}
		selected = client
	}
	pid, hasPID := selected.ProcessPID()
	_, hasCreated := selected.Created()
	if selected.Name() == "" || !hasPID || pid <= 0 || !hasCreated {
		return nil, usage("no identifiable terminal client views this pane; use -d or --append")
	}
	return &loadHandoff{client: selected}, nil
}

func (h *loadHandoff) close() {
	if h.terminal != nil {
		_ = h.terminal.Close()
	}
}

func (h *loadHandoff) attach(ctx context.Context, session tmux.Session) error {
	if h.terminal != nil {
		return session.Attach(ctx, tmux.AttachSessionOptions{Stdin: h.input, Stdout: h.terminal, Stderr: h.terminal})
	}
	live, err := h.client.Refresh(ctx)
	if err != nil {
		return errors.Join(&failure{"client_changed", "the invoking client changed before handoff", 1}, err)
	}
	oldPID, _ := h.client.ProcessPID()
	pid, hasPID := live.ProcessPID()
	oldCreated, _ := h.client.Created()
	created, hasCreated := live.Created()
	oldTTY, _ := h.client.TTY()
	tty, hasTTY := live.TTY()
	oldSession, _ := h.client.Formats().SessionID()
	currentSession, hasSession := live.Formats().SessionID()
	oldWindow, _ := h.client.Formats().WindowID()
	window, hasWindow := live.Formats().WindowID()
	oldPane, _ := h.client.Formats().PaneID()
	pane, hasPane := live.Formats().PaneID()
	flags, hasFlags := live.Flags()
	if !h.client.Equal(live) || !hasPID || pid != oldPID ||
		!hasCreated || !created.Equal(oldCreated) || !hasTTY || tty != oldTTY ||
		!hasSession || currentSession != oldSession || !hasWindow || window != oldWindow ||
		!hasPane || pane != oldPane || !hasFlags || contains(strings.Split(flags, ","), "active-pane") {
		return &failure{"client_changed", "the invoking client changed before handoff", 1}
	}
	// tmux targets the client name; the incarnation check is observational.
	plan := tmux.NewPlan()
	plan.SwitchClient(session.Ref(), h.client.Name())
	result, err := plan.Run(ctx, h.client.Server())
	if err != nil {
		return err
	}
	return result.Err()
}

func (r *invocation) finishHandoff(h *loadHandoff, session tmux.Session) error {
	if h.terminal != nil {
		restore, err := prepareTerminalRestore(h.input)
		if err != nil {
			return err
		}
		defer func() { r.terminalRestoreErr = restore() }()
	}
	return h.attach(r.ctx, session)
}
