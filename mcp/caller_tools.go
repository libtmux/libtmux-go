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

// callerIdentity is what this process can prove about the pane it runs in.
//
// Knowing this is what keeps a client from acting on the terminal it is
// talking through. A pane reported with isCaller true is one whose contents
// are this conversation; killing it, clearing it, or typing into it is not
// like doing those things to any other pane.
//
// tmux tells a process inside a pane two things: TMUX_PANE, the pane's id, and
// TMUX, whose first field is the socket of the server that owns it. A pane id
// is unique only within one server, so an id alone proves nothing: another
// tmux on another socket may have a pane with the same id, and answering from
// the id would name someone else's pane as this one.
type callerIdentity struct {
	// paneID is the pane this process runs in, empty when it runs outside tmux.
	paneID string
	// socket is the resolved socket path of the server that owns that pane.
	socket string
	// inside reports whether this process runs inside a tmux pane at all.
	inside bool
}

// callerFromEnvironment reads what tmux told this process about itself.
func callerFromEnvironment() callerIdentity {
	pane := os.Getenv("TMUX_PANE")
	tmuxVariable := os.Getenv("TMUX")
	if pane == "" || tmuxVariable == "" {
		return callerIdentity{}
	}
	socket, _, _ := strings.Cut(tmuxVariable, ",")
	return callerIdentity{paneID: pane, socket: resolvePath(socket), inside: true}
}

// callerIdentityFor works out which pane this process runs in, asking tmux
// when its own environment does not say.
//
// The environment is the fast answer and usually the right one. It is also the
// answer a client can take away: an MCP client starts its servers with the
// environment it chooses, and most choose a curated one that keeps neither
// TMUX nor TMUX_PANE. A server started that way is inside a pane and cannot
// tell, which is the case where being wrong matters most — it would report
// every pane as somebody else's, including its own.
//
// So when the environment says nothing, the pane is found the way it can
// always be found: this process descends from whatever tmux started in the
// pane, so a pane whose process is one of this process's ancestors is the pane
// this process is in. That answer is also stronger than the environment's,
// because the panes were listed from this server and there is no second socket
// for an id to collide on.
func (t *tools) callerIdentityFor(ctx context.Context) callerIdentity {
	t.callerOnce.Do(func() {
		if fromEnvironment := callerFromEnvironment(); fromEnvironment.inside {
			t.caller = fromEnvironment
			return
		}
		t.caller = t.callerFromProcessTree(ctx)
	})
	return t.caller
}

// callerFromProcessTree finds the pane whose process this one descends from.
func (t *tools) callerFromProcessTree(ctx context.Context) callerIdentity {
	ancestors := ancestorPIDs()
	if len(ancestors) == 0 {
		return callerIdentity{}
	}
	// One listing rather than a lookup per ancestor: the panes are few and the
	// ancestors are fewer, and this runs once for the life of the process.
	result, err := t.tmux().Cmd(ctx, "list-panes", "-a", "-F", "#{pane_pid}|#{pane_id}")
	if err != nil || result.ExitCode != 0 {
		return callerIdentity{}
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
			paneID: paneID,
			socket: resolvePath(t.socketPath(ctx)),
			inside: true,
		}
	}
	return callerIdentity{}
}

// ancestorDepth bounds the walk up the process tree. A pane's shell is a
// handful of processes above whatever a client started, and a cycle in the
// answers would otherwise be a loop that never ends.
const ancestorDepth = 32

// ancestorPIDs reports this process and the processes above it, nearest first.
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

// parentPID reports which process started pid.
//
// ps rather than /proc, because it is the answer on every platform tmux runs
// on and this is asked a handful of times for the life of the process.
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

// resolvePath follows symbolic links so two spellings of one socket compare
// equal. A path that cannot be resolved is compared as it was given, which can
// only make the answer false rather than wrongly true.
func resolvePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return resolved
}

// isCaller reports whether pane is the one this process runs in.
//
// The three answers are deliberately distinct. True needs both the pane id and
// the server's socket to match, so a pane id that collides across sockets is
// not mistaken for this one. False is returned when the id matches and the
// socket does not, or cannot be proven, because an unproven match is not a
// match. Unknown is returned when this process is not inside tmux, where the
// question has no answer rather than a negative one.
func (c callerIdentity) isCaller(pane tmux.Pane, socket string) *bool {
	if !c.inside {
		return nil
	}
	answer := pane.ID().String() == c.paneID && socket != "" && resolvePath(socket) == c.socket
	return &answer
}

// callerInstructions is what a client is told before it calls anything.
//
// This is the highest-leverage text the server has. A tool description is read
// only once a client is already looking at the tool list; instructions decide
// whether it looks here at all, and which tool it reaches for when two would
// work. So this says what the server is for, what it is not for, and which of
// the overlapping tools to prefer.
//
// The words a person uses are ambiguous across programs: a pane is an editor
// split as often as a tmux one, and a window is a browser tab. Saying which
// this server means is worth more than any single tool's description.
func callerInstructions() string {
	var text strings.Builder
	text.WriteString(`Drive one tmux server: sessions, windows, and panes.

USE THIS FOR tmux objects. A bare "pane", "split", "this terminal", "send keys",
or "scrollback" means tmux here. The identifiers are unambiguous: %` + `N is a
pane, @N a window, $N a session.

DO NOT USE THIS FOR editor splits (VS Code, Neovim), browser windows or tabs,
desktop windows, or notebook cells. Ask which is meant if a bare "window" or
"session" could be either.

WAIT, DO NOT POLL. Reading a pane until it looks right costs a round trip per
look and finds the shell's echo of the command rather than its result:
  - a command this client runs, its exit status, and its output: run_command
  - output this client did not author, such as a service announcing itself:
    wait_for_text, with stop set to the failure markers you already know so a
    failed run returns at once instead of at the deadline
  - a program whose finishing you cannot predict the words of: wait_for_text
    with idleSeconds, which returns when the pane goes quiet
  - anything that signals a tmux channel, including another client:
    wait_for_channel
capture_pane is for what a pane shows right now, not for waiting.

DO NOT WAIT AT ALL FOR WORK YOU CAN COME BACK TO. run_command with detach
returns a jobId as soon as the command is typed, and get_job collects the exit
status and output when you want them. Use it for a build or a test run and
spend the turn on something else. Every wait is bounded either way, and the
reply says which bound it ran under.

WATCHING ACROSS TURNS IS capture_since. It returns what a pane wrote since the
cursor the last call gave you, so a pane you check every turn costs its new
lines rather than its whole screen every time. Call it once with no cursor to
start. Clients that can subscribe to resources can instead subscribe to
tmux://panes/{pane}/content and be told when the pane changes.

REPLIES ARE BOUNDED. Anything that returns pane text keeps the last lines and
says what it dropped, so a pane holding a day of output cannot fill your
context. Ask for more with maxLines; ask for scrollback with includeHistory.

NARROW A LISTING RATHER THAN READING THE SERVER. list_panes takes sessionName,
windowId, command, pathUnder, dead, and active; list_windows and list_sessions
take their own. Every reply says the total it selected from. On a machine
carrying somebody else's tmux, asking for the pane running a command rather
than for every pane is one answer instead of forty.

PREFER ONE RESPONSE. snapshot_pane returns a pane's contents and state together,
avoiding a second protocol call. State and content are collected sequentially,
not atomically. search_panes finds which pane shows something, and the lines
that showed it, without capturing each pane in turn. A batch runs several calls
in one request.

LISTING TELLS YOU WHAT EXISTS, NOT WHAT IS IN IT. list_windows and list_panes
report names, indexes, and positions; search_panes and capture_pane are what
read the text a pane is showing. For state without contents, list_panes with
detail full adds every matching pane's exit status, path, title, history size,
and whether it is in a mode that swallows keys. Use capture_since or a resource
subscription to detect new output; history size alone is not a change signal.
get_pane_info reports the same state for one pane you can name.

BEFORE YOU MOVE WHAT SOMEBODY IS LOOKING AT, get_server_info reports every
attached client and the session each is watching, marking the ones that are
programs rather than people. Selecting a window in a session nobody is
attached to moves nothing.

PUTTING TEXT IN A PANE, in order of how much tmux reads:
  - send_keys types a line and presses Enter, reading tmux key names, so "C-c"
    interrupts and "Escape" is a key
  - send_keys_batch sends key names in order with no Enter, for driving a pager
    or an editor
  - paste_text delivers text exactly, reading nothing. Use it for anything you
    did not write by hand: a word like "Escape" inside it would otherwise be
    sent as that key

CHANGING THINGS. split_window and create_window make room; select_layout,
resize_pane, swap_pane and select_window arrange it; move_pane puts a pane in
another window, or breaks it out into one of its own, keeping whatever it is
running; build_workspace lays out a whole session from a tmuxp-style document,
which is more than is wanted for one more window. rename_window and
set_pane_title label what you built for whoever reads it. kill_pane,
kill_window, kill_session and kill_server end things, and nothing undoes them.

WHEN A PANE MAKES NO SENSE, the reason is often a setting rather than its
contents: show_option for why scrollback stopped or a dead pane is still there,
show_hooks with a name for behaviour nothing here caused, show_environment for
what a new pane would inherit, get_server_info with includeMessages for tmux's
own log of what it refused, display_message for anything else tmux knows when
changing tools are enabled. A tmux format may run a shell command through #(). If a
program reports success or failure by colouring a word rather than saying so,
capture_pane with styles keeps the colour a capture strips.
`)
	text.WriteString("\n" + safetyFromEnvironment().describe() + "\n")
	capabilities, _ := capabilitiesFromEnvironment()
	text.WriteString(capabilities.describe() + "\n")

	caller := callerFromEnvironment()
	if !caller.inside {
		// Written before any tmux call is possible, so this only knows what the
		// environment said. A client that starts its servers with a curated
		// environment keeps neither variable, and the server then finds its own
		// pane by asking tmux instead — which it cannot do yet, here.
		text.WriteString("\nThis server's environment does not say which tmux pane it runs " +
			"in. It may still be running in one: get_server_info works it out from tmux " +
			"itself and reports the pane, and a pane listed with isCaller true is that " +
			"pane. Ask before acting on a pane, because that one is this conversation.\n")
		return text.String()
	}
	text.WriteString("\nThis server runs in pane " + caller.paneID + " on the tmux server at " +
		caller.socket + ". A pane reported with isCaller true is that pane: killing it, " +
		"clearing it, or typing into it acts on the terminal this server is running in. " +
		"A pane id is unique only within one tmux server, so isCaller is false when an id " +
		"matches on a different socket. get_server_info says whether the tmux server these " +
		"tools address is the one holding that pane.\n")
	return text.String()
}
