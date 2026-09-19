package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/spf13/cobra"
)

func serverFor(o *options) (tmux.Server, error) {
	socketPath := o.socketPath
	if socketPath == "" && o.socketName == "" && os.Getenv("TMUX") != "" {
		var err error
		socketPath, _, err = inheritedEndpoint()
		if err != nil {
			return tmux.Server{}, err
		}
	}
	colors := tmux.ColorDefault
	if o.colors256 {
		colors = tmux.Color256
	}
	server, err := tmux.NewServer(tmux.ServerOptions{SocketName: o.socketName, SocketPath: socketPath, ConfigFile: o.tmuxConfig, Colors: colors})
	if err != nil {
		return server, &failure{"tmux_unavailable", err.Error(), 1}
	}
	return server, nil
}

func query(ctx context.Context, server tmux.Server, args ...string) (string, error) {
	for _, arg := range args {
		if arg == ";" || strings.ContainsRune(arg, 0) {
			return "", errors.New("invalid tmux query operand")
		}
	}
	result, err := server.Cmd(ctx, args...)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("%s: %s", args[0], strings.Join(result.Stderr, "\n"))
	}
	return strings.TrimSuffix(string(result.RawStdout), "\n"), nil
}

func findSession(ctx context.Context, server tmux.Server, target string) (tmux.Session, error) {
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		// A server with zero sessions fails the underlying query outright
		// ("no current target") instead of reporting emptiness, and a socket
		// with no server behind it holds no session either; there is nothing
		// this lookup could have found.
		message := "no sessions are running"
		if target != "" {
			message = fmt.Sprintf("session %q not found", target)
		}
		return tmux.Session{}, &failure{"session_not_found", message, 1}
	}
	if target == "" {
		if paneID := os.Getenv("TMUX_PANE"); paneID != "" && currentEndpoint(server) == nil {
			for _, pane := range snapshot.Panes() {
				if pane.ID().String() == paneID {
					session, ok := pane.Session()
					if ok {
						return session, nil
					}
				}
			}
		}
		if len(snapshot.Sessions()) == 1 {
			return snapshot.Sessions()[0], nil
		}
		return tmux.Session{}, usage("session is ambiguous; supply a session name")
	}
	for _, session := range snapshot.Sessions() {
		name, _ := session.Name()
		if name == target || session.ID().String() == target {
			return session, nil
		}
	}
	return tmux.Session{}, &failure{"session_not_found", fmt.Sprintf("session %q not found", target), 1}
}

func currentSession(ctx context.Context, server tmux.Server) (tmux.Session, error) {
	if os.Getenv("TMUX_PANE") == "" || os.Getenv("TMUX") == "" {
		return tmux.Session{}, usage("--append requires TMUX and TMUX_PANE identifying the current session")
	}
	snapshot, pane, err := currentView(ctx, server)
	if err != nil {
		return tmux.Session{}, err
	}
	return snapshot.SessionByID(pane.SessionID())
}

func currentView(ctx context.Context, server tmux.Server) (tmux.Snapshot, tmux.Pane, error) {
	if err := currentEndpoint(server); err != nil {
		return tmux.Snapshot{}, tmux.Pane{}, err
	}
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		return tmux.Snapshot{}, tmux.Pane{}, err
	}
	paneID := tmux.PaneID(os.Getenv("TMUX_PANE"))
	pane, err := snapshot.PaneByID(paneID)
	if errors.Is(err, tmux.ErrSnapshotAmbiguous) {
		// Linked panes use tmux's canonical session, protected by this snapshot.
		pane, err = snapshot.Server().Pane(ctx, paneID)
	}
	if err != nil {
		return tmux.Snapshot{}, tmux.Pane{}, usage("current pane does not belong to the selected tmux server")
	}
	_, inheritedPID, err := inheritedEndpoint()
	pid, ok := pane.Formats().PID()
	if err != nil || !ok || pid != inheritedPID {
		return tmux.Snapshot{}, tmux.Pane{}, usage("selected socket does not identify the current tmux server")
	}
	return snapshot, pane, nil
}

func inheritedEndpoint() (string, int, error) {
	value := os.Getenv("TMUX")
	last := strings.LastIndexByte(value, ',')
	if last > 0 {
		previous := strings.LastIndexByte(value[:last], ',')
		if previous > 0 {
			pid, pidErr := strconv.Atoi(value[previous+1 : last])
			_, sessionErr := strconv.ParseUint(value[last+1:], 10, 64)
			if pidErr == nil && pid > 0 && sessionErr == nil {
				return value[:previous], pid, nil
			}
		}
	}
	return "", 0, usage("TMUX must identify the current tmux server with socket,positive-pid,session")
}

func currentEndpoint(server tmux.Server) error {
	path, _, err := inheritedEndpoint()
	if err != nil {
		return err
	}
	current, currentErr := os.Stat(path)
	selected, selectedErr := os.Stat(server.SocketPath())
	if currentErr == nil && selectedErr == nil && os.SameFile(current, selected) {
		return nil
	}
	return usage("selected socket does not identify the current tmux server")
}

func (r *invocation) loadValidation(cmd *cobra.Command, o *options) error {
	if _, _, err := sessionDimensions(); err != nil {
		return err
	}
	if o.colors256 && o.colors88 {
		return usage("-2 and -8 are mutually exclusive")
	}
	if o.colors88 {
		return &failure{"unsupported_color_mode", "tmux 3.2a+ rejects the legacy 88-color flag (-8); remove it or use -2 for 256 colors", 2}
	}
	if r.machine() && !o.detached && !o.append {
		return usage("machine load requires -d or explicit --append")
	}
	_, height := r.progressTerminal(o)
	if height != 0 && !cmd.Flags().Changed("progress-lines") {
		if raw := os.Getenv("TMUXP_PROGRESS_LINES"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return usage("TMUXP_PROGRESS_LINES must be an integer")
			}
			o.progressLines = n
		}
	}
	if o.progressLines < -1 {
		return usage("progress-lines must be -1 or nonnegative")
	}
	if height != 0 && o.progressFormat == "" {
		o.progressFormat = os.Getenv("TMUXP_PROGRESS_FORMAT")
	}
	if o.progressFormat == "" {
		o.progressFormat = "default"
	}
	if os.Getenv("TMUXP_PROGRESS") == "0" {
		o.noProgress = true
	}
	return nil
}

func (r *invocation) load(cmd *cobra.Command, o *options, args []string) error {
	if o.detached {
		// -d always builds a new detached session; --append only applies
		// without it, matching tmuxp.
		o.append = false
	}
	if err := r.loadValidation(cmd, o); err != nil {
		return err
	}
	if !o.detached && !o.append && os.Getenv("TMUX") == "" && !terminal(r.in) {
		return &failure{"terminal_required", "attach requires terminal stdin; use -d", 2}
	}
	type input struct {
		path     string
		plan     loadPlan
		scripted bool
	}
	inputs := []input{}
	needsPython := false
	for index, arg := range args {
		path, err := resolveFile(arg, "")
		if err != nil {
			return err
		}
		doc, err := readDocument(path)
		if err != nil {
			return err
		}
		if o.session != "" && index == len(args)-1 {
			doc["session_name"] = o.session
		}
		plan, err := normalize(doc, filepath.Dir(path))
		if err != nil {
			// Preserve a *failure's code and exit status; only the message
			// gains the failing input's path, so the classification a
			// --json/--ndjson consumer branches on survives the wrap.
			var specific *failure
			if errors.As(err, &specific) {
				return &failure{specific.Code, privatePath(path) + ": " + specific.Message, specific.Exit}
			}
			return fmt.Errorf("%s: %w", privatePath(path), err)
		}
		if plan.Bridge {
			needsPython = true
		}
		_, scripted := doc["before_script"]
		inputs = append(inputs, input{path, plan, scripted})
	}
	// Prompting needs to know whether the target session already exists, so
	// it runs only once parsing has resolved the final input's name -- and
	// only for a real interactive terminal; a script or pipe proceeds as
	// though it answered yes, the same default every other question here
	// uses. Only this path needs a server before the bridge/append check
	// below: a machine-mode or already-decided invocation must not require
	// tmux before that refusal.
	askable := !r.machine() && !o.detached && !o.append && !o.yes && terminal(r.in)
	var server tmux.Server
	var err error
	if askable {
		server, err = serverFor(o)
		if err != nil {
			return err
		}
		name := ""
		if len(inputs) > 0 {
			name = inputs[len(inputs)-1].plan.Name
		}
		exists := false
		if name != "" {
			exists, err = server.HasSession(r.ctx, tmux.HasSessionRequest{Target: name})
			if err != nil {
				return err
			}
		}
		switch {
		case exists:
			answer, err := r.prompt(name+" is already running. Attach?", "y")
			if err != nil {
				return err
			}
			switch strings.ToLower(answer) {
			case "y", "yes":
			case "n", "no":
				return nil
			default:
				return usage("attach choice must be y or n")
			}
		case os.Getenv("TMUX") != "":
			choice, err := r.prompt("Already inside tmux: switch (y), load detached (n), or append (a)", "y")
			if err != nil {
				return err
			}
			switch strings.ToLower(choice) {
			case "y", "yes":
			case "n", "no":
				o.detached = true
			case "a", "append":
				o.append = true
			default:
				return usage("load choice must be y, n or a")
			}
		}
	}
	for _, in := range inputs {
		if in.plan.Bridge && o.append && in.scripted {
			return &failure{"unsupported_combination", "--append with Python plugins/custom builders and before_script is unavailable: tmuxp can delete the borrowed session on script failure", 2}
		}
	}
	if o.logFile != "" {
		file, err := openLogFile(expand(o.logFile))
		if err != nil {
			return err
		}
		r.log = newDiagnosticLog(file, r.diagnosticLevel())
	}
	if !askable {
		server, err = serverFor(o)
		if err != nil {
			return err
		}
	}
	var borrowed tmux.Session
	var handoff *loadHandoff
	// The borrowed daemon answers the layout preflight, so --append resolves it
	// first: the version a layout is checked against has to be the one that
	// will apply it.
	if o.append {
		borrowed, err = currentSession(r.ctx, server)
		if err != nil {
			return err
		}
		server = borrowed.Server()
	}
	if err := server.ValidateLayouts(r.ctx, func(yield func(string, int) bool) {
		for _, input := range inputs {
			if input.plan.Bridge {
				continue
			}
			for _, window := range input.plan.Windows {
				if !yield(window.Layout, max(1, len(window.Panes))) {
					return
				}
			}
		}
	}); err != nil {
		return err
	}
	if !o.append && !o.detached {
		handoff, err = r.prepareHandoff(server)
		if err != nil {
			return err
		}
		defer handoff.close()
		if handoff.client.Name() != "" {
			server = handoff.client.Server()
		}
	}
	if needsPython {
		if err := r.checkPython(true); err != nil {
			return err
		}
	}
	if err := r.event("started", map[string]any{"inputs": len(inputs)}); err != nil {
		return err
	}
	r.startProgress(o)
	if r.progress != nil {
		defer func() {
			if err := r.progress.close(); err != nil {
				r.writeErr = err
			}
		}()
	}
	results := []map[string]any{}
	failures := []map[string]any{}
	summary := map[string]any{"schema_version": 1, "command": "load", "status": "partial", "results": results, "errors": failures}
	var last tmux.Session
	retainedEffects := false
	sessionRemoved := false
	retainedWindows := []string{}
	for index, input := range inputs {
		if r.ctx.Err() != nil {
			break
		}
		if r.progress != nil {
			r.progress.begin(input.plan, privatePath(input.path))
		}
		if err := r.event("workspace-started", map[string]any{"input_index": index, "input": privatePath(input.path)}); err != nil {
			return err
		}
		for _, warning := range input.plan.Warnings {
			if err := r.event("warning", map[string]any{"input_index": index, "code": "unsupported_key", "message": warning}); err != nil {
				return err
			}
		}
		var session tmux.Session
		var buildErr error
		var built []string
		reused := false
		if o.append {
			session = borrowed
		} else {
			var exists bool
			exists, buildErr = server.HasSession(r.ctx, tmux.HasSessionRequest{Target: input.plan.Name})
			if buildErr == nil && exists {
				session, buildErr = findSession(r.ctx, server, input.plan.Name)
				reused = true
				if buildErr == nil {
					buildErr = reusedSessionGap(r.ctx, server, session, input.plan)
				}
			}
		}
		if buildErr == nil && !reused {
			if input.plan.Bridge {
				session, buildErr = r.bridgeLoad(server, session, o, input.path, input.plan.Name, index)
			} else {
				session, built, buildErr = r.build(server, session, input.plan, index)
			}
		}
		entry := map[string]any{"input_index": index, "input": privatePath(input.path), "session_name": input.plan.Name, "session_id": session.ID().String(), "reused": reused, "created_windows": built}
		if buildErr != nil {
			entry["stage"] = "failed"
			// build only ever runs tmux mutations and before_script, so any
			// error that is not already classified (script_failed) is a
			// tmux command failure.
			code := "tmux_failed"
			var specific *failure
			if errors.As(buildErr, &specific) {
				code = specific.Code
			}
			var removedErr *removedSessionError
			var noEffectErr *noEffectError
			removed := errors.As(buildErr, &removedErr)
			entry["removed"] = removed
			switch {
			case removed:
				sessionRemoved = true
			case errors.As(buildErr, &noEffectErr):
				// Nothing this input built survived and nothing was removed
				// either: it never created anything to describe either way.
			default:
				retainedEffects = true
				retainedWindows = append(retainedWindows, built...)
			}
			entryFailure := map[string]any{"code": code, "message": buildErr.Error(), "input_index": index, "stage": "load", "session_id": session.ID().String()}
			failures = append(failures, entryFailure)
		} else {
			last = session
			entry["stage"] = "completed"
			retainedEffects = true
			retainedWindows = append(retainedWindows, built...)
		}
		results = append(results, entry)
		summary["results"], summary["errors"] = results, failures
		if len(r.scripts) > 0 {
			summary["scripts"] = r.scripts
		}
		r.loadResult = summary
		if buildErr != nil {
			if err := r.event("warning", failures[len(failures)-1]); err != nil {
				return err
			}
		} else {
			if err := r.event("workspace-completed", entry); err != nil {
				return err
			}
			if !r.machine() {
				if o.append {
					name, _ := session.Name()
					if _, err := fmt.Fprintf(r.out, "%s %s\n", r.style("success", "Appended"), r.style("subject", name)); err != nil {
						return err
					}
				} else {
					verb := "Loaded"
					if reused {
						verb = "Reused"
					}
					if _, err := fmt.Fprintf(r.out, "%s %s %s\n", r.style("success", verb), r.style("subject", input.plan.Name), r.style("secondary", session.ID().String())); err != nil {
						return err
					}
				}
			}
		}
	}
	if r.ctx.Err() != nil {
		failures = append(failures, map[string]any{"code": "interrupted", "message": "operation interrupted", "stage": "load"})
	}
	// error and retained effects are contradictory: error is the status that
	// says nothing survived, so anything left on the server is partial.
	status := "ok"
	if len(failures) > 0 {
		status = "partial"
		if !retainedEffects {
			status = "error"
		}
	}
	summary["status"], summary["errors"] = status, failures
	r.loadResult = summary
	if r.progress != nil {
		if err := r.progress.close(); err != nil {
			return err
		}
	}
	event := "completed"
	if len(failures) > 0 {
		event = "failed"
	}
	if r.ndjson {
		if err := r.event(event, summary); err != nil {
			return err
		}
	} else {
		r.logEvent(event, summary)
		if r.json {
			if err := r.encode(summary); err != nil {
				return err
			}
		}
	}
	if len(failures) > 0 {
		// A stderr-only consumer never sees errors[]; give it the first
		// entry's specific code (tmux_failed, script_failed, ...) rather
		// than a generic one, keeping the full summary under "result".
		code, _ := failures[0]["code"].(string)
		if code == "" {
			code = "load_failed"
		}
		// Retained effects take priority: a completed input, or a failure
		// whose session was never this load's to remove, leaves something
		// behind. Otherwise, a killed session says so; a failure that built
		// nothing at all (an append or reuse whose first window never took
		// hold) gets neither clause.
		first, _ := failures[0]["message"].(string)
		message := "one or more workspaces failed; completed effects are retained"
		switch {
		case !retainedEffects:
			message = first
			if sessionRemoved {
				message += "; the session it created was removed"
			}
		case len(retainedWindows) > 0:
			// Nothing here can be rolled back -- an appended or reused session
			// belongs to the user -- so the message names what it left.
			message = first + "; windows retained: " + strings.Join(retainedWindows, ", ")
		}
		return &failure{code, message, 1}
	}
	if handoff != nil && last.ID() != "" {
		if err := errors.Join(flushOutput(r.out), flushOutput(r.err)); err != nil {
			return err
		}
		return r.finishHandoff(handoff, last)
	}
	return nil
}

func (r *invocation) bridgeLoad(server tmux.Server, borrowed tmux.Session, o *options, path, name string, index int) (tmux.Session, error) {
	args := []string{"--color", "never", "load", "--no-progress"}
	if o.append {
		args = append(args, "--append")
	} else {
		args = append(args, "-d")
	}
	if o.yes {
		args = append(args, "--yes")
	}
	if o.socketPath != "" {
		args = append(args, "-S", o.socketPath)
	} else if o.socketName != "" {
		args = append(args, "-L", o.socketName)
	}
	if o.tmuxConfig != "" {
		args = append(args, "-f", o.tmuxConfig)
	}
	args = append(args, "-s", name)
	if o.colors256 {
		args = append(args, "-2")
	}
	args = append(args, path)
	argv := bridgeArgv(args)
	if o.append {
		var err error
		argv, err = appendBridgeArgv(borrowed, o, path, name)
		if err != nil {
			return borrowed, err
		}
	}
	if err := r.event("warning", map[string]any{"input_index": index, "code": "python_compatibility", "message": "plugins/custom builder execute in version-checked tmuxp " + referenceVersion}); err != nil {
		return borrowed, err
	}
	if err := r.event("script-started", map[string]any{"input_index": index}); err != nil {
		return borrowed, err
	}
	r.scriptInput = &index
	result, err := r.process(argv, "", nil, true)
	r.scriptInput = nil
	r.scripts = append(r.scripts, map[string]any{"input_index": index, "kind": "python-workspace", "result": result})
	if eventErr := r.event("script-completed", map[string]any{"input_index": index, "child_status": result.Status, "truncated": result.Truncated}); eventErr != nil {
		return borrowed, eventErr
	}
	if err != nil {
		return borrowed, err
	}
	if result.Status != 0 {
		// The bridge is a Python subprocess, not a tmux command, so its exit
		// is classified as a script failure, not tmux_failed.
		message := fmt.Sprintf("python workspace bridge exited %d", result.Status)
		if output := result.Stdout + result.Stderr; output != "" {
			message += ": " + output
		}
		return borrowed, &failure{"script_failed", message, 1}
	}
	if o.append {
		return borrowed, nil
	}
	return findSession(r.ctx, server, name)
}

// removedSessionError marks a build failure that also removed the session
// build created (a failed before_script never leaves a half-built session
// behind): the caller must not report that input's effects as retained.
// Wrapping only fires when Kill itself succeeds; a Kill failure leaves the
// session's fate unknown, so the failure is left unmarked rather than
// asserting a removal that may not have happened.
type removedSessionError struct{ err error }

func (e *removedSessionError) Error() string { return e.err.Error() }
func (e *removedSessionError) Unwrap() error { return e.err }

// noEffectError marks a build failure that left no mutation behind on a
// session this load did not create (an append or a reuse): the first window
// this input tried to add failed before any of it took hold, so the caller
// must not report the input's effects as retained.
type noEffectError struct{ err error }

func (e *noEffectError) Error() string { return e.err.Error() }
func (e *noEffectError) Unwrap() error { return e.err }

// reusedSessionGap names the windows a reused session does not have. Reuse is
// keyed on the session name, so a session that exists satisfies it whatever
// state it is in; comparing is the rule here, and rebuilding is a separate
// feature. A window the document leaves unnamed cannot be compared by name,
// so it is not checked.
func reusedSessionGap(ctx context.Context, server tmux.Server, session tmux.Session, plan loadPlan) error {
	names, err := query(ctx, server, "list-windows", "-t", session.ID().String(), "-F", "#{window_name}")
	if err != nil {
		return err
	}
	present := map[string]int{}
	for _, line := range strings.Split(names, "\n") {
		if line != "" {
			present[line]++
		}
	}
	missing := []string{}
	for _, window := range plan.Windows {
		if window.Name == "" {
			continue
		}
		if present[window.Name] > 0 {
			present[window.Name]--
			continue
		}
		missing = append(missing, strconv.Quote(window.Name))
	}
	if len(missing) == 0 {
		return nil
	}
	label := "window"
	if len(missing) > 1 {
		label = "windows"
	}
	return &failure{"session_not_found", fmt.Sprintf("session %s already exists and is missing %s %s this workspace describes", strconv.Quote(plan.Name), label, strings.Join(missing, ", ")), 1}
}

// windowIndexTaken reports whether session already has a window at index,
// so a collision names its own index instead of tmux's redacted new-window
// failure.
func windowIndexTaken(ctx context.Context, server tmux.Server, session tmux.Session, index int) (bool, error) {
	indices, err := query(ctx, server, "list-windows", "-t", session.ID().String(), "-F", "#{window_index}")
	if err != nil {
		return false, err
	}
	target := strconv.Itoa(index)
	for _, line := range strings.Split(indices, "\n") {
		if line == target {
			return true, nil
		}
	}
	return false, nil
}

// build creates or extends a session and, when it created one, removes it
// again on failure: a load only reports that nothing was retained when
// nothing was, and a half-built session left on the server turns the next run
// of the same document into a reuse that reports success.
func (r *invocation) build(server tmux.Server, session tmux.Session, plan loadPlan, inputIndex int) (tmux.Session, []string, error) {
	created := session.ID() == ""
	session, windows, err := r.buildInto(server, session, plan, inputIndex)
	if err == nil || !created || session.ID() == "" {
		return session, windows, err
	}
	var removed *removedSessionError
	if errors.As(err, &removed) {
		return session, nil, err
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	killErr := session.Kill(cleanup)
	cancel()
	if killErr != nil {
		return session, windows, errors.Join(err, killErr)
	}
	return session, nil, &removedSessionError{err}
}

func (r *invocation) buildInto(server tmux.Server, session tmux.Session, plan loadPlan, inputIndex int) (tmux.Session, []string, error) {
	created := session.ID() == ""
	windows := []string{}
	if !created {
		if _, err := session.Refresh(r.ctx); err != nil {
			return session, windows, err
		}
	}
	var bootstrap tmux.Window
	if created {
		width, height, err := sessionDimensions()
		if err != nil {
			return session, windows, err
		}
		session, err = server.NewSession(r.ctx, tmux.NewSessionRequest{Name: plan.Name, StartDirectory: plan.Directory, Environment: plan.Environment, Width: width, Height: height})
		if err != nil {
			return session, windows, err
		}
		bootstrap, err = session.ResolveActiveWindow(r.ctx)
		if err != nil {
			return session, windows, err
		}
		if err := r.event("session-created", map[string]any{"input_index": inputIndex, "session_id": session.ID().String(), "session_name": plan.Name}); err != nil {
			return session, windows, err
		}
	}
	if len(plan.BeforeScript) != 0 {
		if err := r.event("script-started", map[string]any{"input_index": inputIndex}); err != nil {
			return session, windows, err
		}
		r.scriptInput = &inputIndex
		result, err := r.process(plan.BeforeScript, plan.ScriptDirectory, nil, true)
		r.scriptInput = nil
		r.scripts = append(r.scripts, map[string]any{"input_index": inputIndex, "kind": "before-script", "result": result})
		eventErr := r.event("script-completed", map[string]any{"input_index": inputIndex, "child_status": result.Status, "truncated": result.Truncated})
		var start *childStartError
		if errors.As(err, &start) && r.ctx.Err() == nil {
			err = &failure{"script_failed", "before_script could not start: " + start.Error(), 1}
		}
		if err == nil && result.Status != 0 {
			message := fmt.Sprintf("before_script exited %d", result.Status)
			if result.Stderr != "" {
				message += ": " + result.Stderr
			}
			err = &failure{"script_failed", message, 1}
		}
		if err != nil {
			return session, windows, errors.Join(err, eventErr)
		}
		if eventErr != nil {
			return session, windows, eventErr
		}
	}
	for _, key := range sortedKeys(plan.Environment) {
		if err := session.SetEnvironment(r.ctx, key, plan.Environment[key], tmux.SetEnvironmentOptions{}); err != nil {
			return session, windows, err
		}
	}
	for _, key := range sortedKeys(plan.GlobalOptions) {
		if err := setGlobalOption(r.ctx, server, key, plan.GlobalOptions[key]); err != nil {
			return session, windows, err
		}
	}
	// tmux keeps some of what a document writes under the session's options:
	// in the window table -- pane-base-index and its like. Those land on the
	// window the session was created with, which the first window of the
	// document replaces, so each window created below is given them too.
	windowScoped := map[string]string{}
	for _, key := range sortedKeys(plan.Options) {
		scope, err := setSessionOption(r.ctx, session, key, plan.Options[key])
		if err != nil {
			return session, windows, err
		}
		if scope == windowOptionScope {
			windowScoped[key] = plan.Options[key]
		}
	}
	// Readiness is not conditional on the shell. Text sent before any shell
	// owns the terminal is echoed by the tty and drawn again once the line
	// editor takes over, so the command appears twice.
	waitForPrompt := plan.Readiness != "never"
	var focus tmux.Window
	var explicitFocus bool
	baseIndex := bootstrap.Index()
	if created {
		value, err := query(r.ctx, server, "show-options", "-A", "-v", "-t", session.ID().String(), "base-index")
		if err != nil {
			return session, windows, err
		}
		baseIndex, err = strconv.Atoi(value)
		if err != nil {
			return session, windows, fmt.Errorf("decode base-index: %w", err)
		}
	}
	for index, wp := range plan.Windows {
		first := wp.Panes[0]
		requestedIndex := wp.Index
		if created && index == 0 && requestedIndex == nil {
			n := baseIndex
			requestedIndex = &n
		}
		var name *string
		if wp.Name != "" {
			name = &wp.Name
		}
		killingBootstrap := created && index == 0 && requestedIndex != nil && *requestedIndex == bootstrap.Index()
		if requestedIndex != nil && !killingBootstrap {
			taken, err := windowIndexTaken(r.ctx, server, session, *requestedIndex)
			if err != nil {
				return session, windows, err
			}
			if taken {
				collision := &failure{"tmux_failed", fmt.Sprintf("create window failed: index %d in use", *requestedIndex), 1}
				if !created && index == 0 {
					return session, windows, &noEffectError{collision}
				}
				return session, windows, collision
			}
		}
		window, err := session.NewWindow(r.ctx, tmux.NewWindowRequest{Name: name, Index: requestedIndex, StartDirectory: first.Directory, Command: first.Shell, Environment: first.Environment, KillExisting: killingBootstrap})
		if err != nil {
			return session, windows, err
		}
		if created && index == 0 && window.Index() != bootstrap.Index() {
			if err := bootstrap.Kill(r.ctx); err != nil {
				return session, windows, err
			}
		}
		built, named := window.Name()
		if !named || built == "" {
			built = window.ID().String()
		}
		windows = append(windows, built)
		if err := r.event("window-created", map[string]any{"input_index": inputIndex, "session_id": session.ID().String(), "window_id": window.ID().String(), "window_index": window.Index(), "window_name": wp.Name, "pane_total": len(wp.Panes)}); err != nil {
			return session, windows, err
		}
		for _, key := range sortedKeys(windowScoped) {
			if err := window.SetOption(r.ctx, key, windowScoped[key], tmux.SetOptionOptions{}); err != nil {
				return session, windows, err
			}
		}
		for _, key := range sortedKeys(wp.Options) {
			if err := window.SetOption(r.ctx, key, wp.Options[key], tmux.SetOptionOptions{}); err != nil {
				return session, windows, err
			}
		}
		pane, ok, err := window.ResolveActivePane(r.ctx)
		if err != nil {
			return session, windows, err
		}
		if !ok {
			return session, windows, errors.New("new window has no active pane")
		}
		// Every pane is created and the layout is final before any shell is
		// typed into: a pane resized after its command redraws the prompt at
		// a stale width and leaves the shell's partial-line marker behind.
		panes := make([]tmux.Pane, len(wp.Panes))
		panes[0] = pane
		for pi := 1; pi < len(wp.Panes); pi++ {
			pp := wp.Panes[pi]
			panes[pi], err = panes[pi-1].Split(r.ctx, tmux.SplitPaneRequest{Direction: tmux.PaneDirectionBelow, StartDirectory: pp.Directory, Command: pp.Shell, Environment: pp.Environment})
			if err != nil {
				return session, windows, err
			}
			// Keep the window tiled while it grows so a split never starves
			// for room; the layout below is the one every pane's shell
			// actually settles into.
			if pi < len(wp.Panes)-1 {
				if err := window.SelectLayout(r.ctx, tmux.SelectLayoutRequest{Layout: "tiled"}); err != nil {
					return session, windows, err
				}
			}
		}
		layout := wp.Layout
		if layout == "" && len(wp.Panes) > 1 {
			layout = "tiled"
		}
		if layout != "" {
			if err := window.SelectLayout(r.ctx, tmux.SelectLayoutRequest{Layout: layout}); err != nil {
				return session, windows, err
			}
		}
		var focusedPane tmux.Pane
		for pi, pp := range wp.Panes {
			pane := panes[pi]
			if err := r.event("pane-created", map[string]any{"input_index": inputIndex, "session_id": session.ID().String(), "window_id": window.ID().String(), "pane_id": pane.ID().String(), "pane_index": pane.Index()}); err != nil {
				return session, windows, err
			}
			if waitForPrompt && pp.Shell == "" {
				ready, err := paneReady(r.ctx, pane)
				if err != nil {
					return session, windows, err
				}
				if !ready {
					if err := r.event("warning", map[string]any{"input_index": inputIndex, "pane_id": pane.ID().String(), "code": "pane_readiness_timeout", "message": "pane prompt did not move the cursor within two seconds"}); err != nil {
						return session, windows, err
					}
				}
			}
			for _, command := range pp.Commands {
				if err := delay(r.ctx, command.SleepBefore); err != nil {
					return session, windows, err
				}
				text := command.Text
				if err := pane.SendKeys(r.ctx, tmux.SendKeysRequest{Command: &text, SuppressHistory: pp.SuppressHistory, SkipEnter: !command.Enter, Literal: true}); err != nil {
					return session, windows, err
				}
				if err := delay(r.ctx, command.SleepAfter); err != nil {
					return session, windows, err
				}
			}
			if pp.Focus {
				focusedPane = pane
			}
			if err := r.event("pane-completed", map[string]any{"input_index": inputIndex, "pane_id": pane.ID().String()}); err != nil {
				return session, windows, err
			}
		}
		for _, key := range sortedKeys(wp.OptionsAfter) {
			if err := window.SetOption(r.ctx, key, wp.OptionsAfter[key], tmux.SetOptionOptions{}); err != nil {
				return session, windows, err
			}
		}
		if focusedPane.ID() != "" {
			if _, err := focusedPane.Select(r.ctx, tmux.PaneSelectRequest{}); err != nil {
				return session, windows, err
			}
		}
		if wp.Focus {
			focus, explicitFocus = window, true
		} else if focus.ID() == "" {
			focus = window
		}
		if err := r.event("window-completed", map[string]any{"input_index": inputIndex, "window_id": window.ID().String()}); err != nil {
			return session, windows, err
		}
	}
	// A fresh session needs its client looking at a window, so the default
	// (first, absent an explicit focus) still applies. Appending to a
	// session the user already owns must not move them unless a window
	// asked for it -- the default-first fallback above exists only to
	// pick something if focus is requested at all.
	if focus.ID() != "" && (created || explicitFocus) {
		if _, err := focus.Select(r.ctx); err != nil {
			return session, windows, err
		}
	}
	return session, windows, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func delay(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func paneReady(ctx context.Context, pane tmux.Pane) (bool, error) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fresh, err := pane.Refresh(ctx)
		if err != nil {
			return false, err
		}
		x, _ := fresh.CursorX()
		y, _ := fresh.CursorY()
		if x != 0 || y != 0 {
			return true, nil
		}
		if err := delay(ctx, 50*time.Millisecond); err != nil {
			return false, err
		}
	}
	return false, nil
}

func setGlobalOption(ctx context.Context, server tmux.Server, name, value string) error {
	first := server.GlobalSessionScope().SetOption(ctx, name, value, tmux.SetOptionOptions{})
	if first == nil {
		return nil
	}
	if err := server.GlobalWindowScope().SetOption(ctx, name, value, tmux.SetOptionOptions{}); err == nil {
		return nil
	}
	if err := server.SetOption(ctx, name, value, tmux.SetOptionOptions{}); err == nil {
		return nil
	}
	return first
}

// optionScope names the tmux option table that accepted a write.
type optionScope int

const (
	sessionOptionScope optionScope = iota
	windowOptionScope
)

// setSessionOption reproduces tmuxp's untyped dispatch across the session and
// window tables, and reports which one took the value.
func setSessionOption(ctx context.Context, session tmux.Session, name, value string) (optionScope, error) {
	first := session.SetOption(ctx, name, value, tmux.SetOptionOptions{})
	if first == nil {
		return sessionOptionScope, nil
	}
	window, err := session.ResolveActiveWindow(ctx)
	if err == nil {
		if err := window.SetOption(ctx, name, value, tmux.SetOptionOptions{}); err == nil {
			return windowOptionScope, nil
		}
	}
	return sessionOptionScope, first
}

func (r *invocation) freeze(_ *cobra.Command, o *options, args []string) error {
	if err := validateFormat(o.format); err != nil {
		return err
	}
	if o.saveTo == "" && !r.machine() {
		return usage("freeze needs --save-to <path>, or --json/--ndjson to print the document")
	}
	server, err := serverFor(o)
	if err != nil {
		return err
	}
	target := ""
	if len(args) > 0 {
		target = args[0]
	}
	session, err := findSession(r.ctx, server, target)
	if err != nil {
		return err
	}
	// freeze must never write a document load would refuse. tmux runs a
	// session whose name it cannot address by name; the workspace file
	// naming it would be unloadable.
	name, _ := session.Name()
	if strings.ContainsAny(name, ".:\x00\r\n") {
		return &failure{"invalid_workspace", fmt.Sprintf("session %s cannot be captured: a session_name containing a period, colon, NUL or newline is refused on load, because tmux reads a period and a colon as separators inside a target", strconv.Quote(name)), 1}
	}
	doc, err := capture(r.ctx, session)
	if err != nil {
		return err
	}
	warnings := []string{"capture cannot recover original commands, process arguments, shell history, plugins or before_script"}
	if r.ndjson && o.saveTo == "" {
		return r.result(map[string]any{"status": "ok", "workspace": doc, "warnings": warnings})
	}
	if o.format == "" {
		o.format = "yaml"
	}
	return r.documentResult(o, doc, "", "yaml", warnings)
}

// interactiveShells covers what default-shell alone misses: /bin/sh is dash on
// Linux and bash on macOS, and the pane reports the real program.
var interactiveShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ash": true,
	"ksh": true, "mksh": true, "fish": true, "csh": true, "tcsh": true,
}

func capture(ctx context.Context, session tmux.Session) (document, error) {
	name, _ := session.Name()
	doc := document{"session_name": name}
	windows, ok := session.Windows()
	if !ok {
		return nil, errors.New("capture session lacks snapshot relations")
	}
	// A pane sitting at a shell needs no shell_command. pane_start_command
	// cannot tell one apart here, because panes are created bare and driven
	// with send-keys, so the current command is the only signal.
	defaultShell, err := query(ctx, session.Server(), "show-options", "-A", "-v", "-t", session.ID().String(), "default-shell")
	if err != nil {
		return nil, err
	}
	defaultShell = filepath.Base(strings.TrimSpace(defaultShell))
	result := []any{}
	for _, window := range windows {
		name, _ := window.Name()
		layout, _ := window.Layout()
		active, _ := window.Active()
		wp := document{"window_name": name, "window_index": window.Index(), "layout": layout, "focus": active}
		options := document{}
		names, err := query(ctx, session.Server(), "show-options", "-w", "-t", window.ID().String())
		if err != nil {
			return nil, err
		}
		for _, row := range strings.Split(names, "\n") {
			name, _, _ := strings.Cut(row, " ")
			if name == "" {
				continue
			}
			value, err := query(ctx, session.Server(), "show-options", "-w", "-t", window.ID().String(), "-v", name)
			if err != nil {
				return nil, err
			}
			options[name] = value
		}
		// automatic-rename: off (and any other captured option) only holds if
		// applied after the panes exist; options_after is where load applies
		// window options at that point.
		wp["options_after"] = options
		panes, _ := window.Panes()
		pp := []any{}
		for _, pane := range panes {
			cwd, _ := pane.CurrentPath()
			command, _ := pane.CurrentCommand()
			active, _ := pane.Active()
			name := filepath.Base(strings.TrimPrefix(command, "-"))
			if strings.HasPrefix(command, "-") || strings.HasSuffix(command, "python") || strings.HasSuffix(command, "ruby") || strings.HasSuffix(command, "node") {
				command = ""
			} else if name == defaultShell || interactiveShells[name] {
				command = ""
			}
			p := document{"start_directory": cwd, "focus": active}
			if command != "" {
				p["shell_command"] = []string{command}
			}
			pp = append(pp, p)
		}
		wp["panes"] = pp
		result = append(result, wp)
	}
	doc["windows"] = result
	return doc, nil
}
