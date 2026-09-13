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
	return tmux.NewServer(tmux.ServerOptions{SocketName: o.socketName, SocketPath: socketPath, ConfigFile: o.tmuxConfig, Colors: colors})
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
		return tmux.Session{}, err
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
	return tmux.Session{}, fmt.Errorf("session %q not found", target)
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
	if err := r.loadValidation(cmd, o); err != nil {
		return err
	}
	if !r.machine() && !o.detached && !o.append && !o.yes && os.Getenv("TMUX") != "" {
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
	if !o.detached && !o.append && os.Getenv("TMUX") == "" && !terminal(r.in) {
		return &failure{"terminal_required", "attach requires terminal stdin; use -d", 2}
	}
	type input struct {
		path string
		plan loadPlan
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
			return fmt.Errorf("%s: %w", privatePath(path), err)
		}
		if plan.Bridge {
			if _, scripted := doc["before_script"]; o.append && scripted {
				return &failure{"unsupported_combination", "--append with Python plugins/custom builders and before_script is unavailable: tmuxp can delete the borrowed session on script failure", 2}
			}
			needsPython = true
		}
		inputs = append(inputs, input{path, plan})
	}
	if o.logFile != "" {
		file, err := openLogFile(expand(o.logFile))
		if err != nil {
			return err
		}
		r.log = newDiagnosticLog(file, r.diagnosticLevel())
	}
	server, err := serverFor(o)
	if err != nil {
		return err
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
	var borrowed tmux.Session
	var handoff *loadHandoff
	if o.append {
		borrowed, err = currentSession(r.ctx, server)
		if err != nil {
			return err
		}
		server = borrowed.Server()
	} else if !o.detached {
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
	if err := r.event("started", map[string]any{"input_count": len(inputs)}); err != nil {
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
	lastReused := false
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
		var session tmux.Session
		var buildErr error
		reused := false
		if o.append {
			session = borrowed
		} else {
			var exists bool
			exists, buildErr = server.HasSession(r.ctx, tmux.HasSessionRequest{Target: input.plan.Name})
			if buildErr == nil && exists {
				session, buildErr = findSession(r.ctx, server, input.plan.Name)
				reused = true
			}
		}
		if buildErr == nil && !reused {
			if input.plan.Bridge {
				session, buildErr = r.bridgeLoad(server, session, o, input.path, input.plan.Name, index)
			} else {
				session, buildErr = r.build(server, session, input.plan, index)
			}
		}
		entry := map[string]any{"input_index": index, "input": privatePath(input.path), "session_name": input.plan.Name, "session_id": session.ID().String(), "reused": reused}
		if buildErr != nil {
			entry["stage"] = "failed"
			failure := map[string]any{"code": "workspace_failed", "message": buildErr.Error(), "input_index": index, "stage": "load", "session_id": session.ID().String()}
			failures = append(failures, failure)
		} else {
			last = session
			lastReused = reused
			entry["stage"] = "completed"
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
	status := "ok"
	if len(failures) > 0 {
		status = "partial"
		if len(failures) == len(results) {
			status = "error"
		}
	}
	if r.ctx.Err() != nil {
		status = "partial"
		failures = append(failures, map[string]any{"code": "interrupted", "message": "operation interrupted", "stage": "load"})
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
		return &failure{"load_failed", "one or more workspaces failed; completed effects are retained", 1}
	}
	if handoff != nil && last.ID() != "" {
		if lastReused && !o.yes && !r.machine() {
			answer, err := r.prompt("Session is already running. Attach (y/n)", "y")
			if err != nil {
				return err
			}
			if strings.ToLower(answer) == "n" || strings.ToLower(answer) == "no" {
				return nil
			}
			if strings.ToLower(answer) != "y" && strings.ToLower(answer) != "yes" {
				return usage("attach choice must be y or n")
			}
		}
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
	result, err := r.process(argv, "", nil, true)
	r.scripts = append(r.scripts, map[string]any{"input_index": index, "kind": "python-workspace", "result": result})
	if eventErr := r.event("script-completed", map[string]any{"input_index": index, "child_status": result.Status, "truncated": result.Truncated}); eventErr != nil {
		return borrowed, eventErr
	}
	if err != nil {
		return borrowed, err
	}
	if result.Status != 0 {
		return borrowed, fmt.Errorf("python workspace bridge exited %d: %s", result.Status, result.Stdout+result.Stderr)
	}
	if o.append {
		return borrowed, nil
	}
	return findSession(r.ctx, server, name)
}

func (r *invocation) build(server tmux.Server, session tmux.Session, plan loadPlan, inputIndex int) (tmux.Session, error) {
	created := session.ID() == ""
	if !created {
		if _, err := session.Refresh(r.ctx); err != nil {
			return session, err
		}
	}
	var bootstrap tmux.Window
	if created {
		width, height, err := sessionDimensions()
		if err != nil {
			return session, err
		}
		session, err = server.NewSession(r.ctx, tmux.NewSessionRequest{Name: plan.Name, StartDirectory: plan.Directory, Environment: plan.Environment, Width: width, Height: height})
		if err != nil {
			return session, err
		}
		bootstrap, err = session.ResolveActiveWindow(r.ctx)
		if err != nil {
			return session, err
		}
		if err := r.event("session-created", map[string]any{"input_index": inputIndex, "session_id": session.ID().String(), "session_name": plan.Name}); err != nil {
			return session, err
		}
	}
	if len(plan.BeforeScript) != 0 {
		if err := r.event("script-started", map[string]any{"input_index": inputIndex}); err != nil {
			return session, err
		}
		result, err := r.process(plan.BeforeScript, plan.ScriptDirectory, nil, true)
		r.scripts = append(r.scripts, map[string]any{"input_index": inputIndex, "kind": "before-script", "result": result})
		eventErr := r.event("script-completed", map[string]any{"input_index": inputIndex, "child_status": result.Status, "truncated": result.Truncated})
		if err == nil && result.Status != 0 {
			err = fmt.Errorf("before_script exited %d: %s", result.Status, result.Stderr)
		}
		if err != nil {
			if created {
				cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				killErr := session.Kill(cleanup)
				cancel()
				if killErr != nil {
					err = errors.Join(err, killErr)
				}
			}
			return session, errors.Join(err, eventErr)
		}
		if eventErr != nil {
			return session, eventErr
		}
	}
	for _, key := range sortedKeys(plan.Environment) {
		if err := session.SetEnvironment(r.ctx, key, plan.Environment[key], tmux.SetEnvironmentOptions{}); err != nil {
			return session, err
		}
	}
	for _, key := range sortedKeys(plan.GlobalOptions) {
		if err := setGlobalOption(r.ctx, server, key, plan.GlobalOptions[key]); err != nil {
			return session, err
		}
	}
	for _, key := range sortedKeys(plan.Options) {
		if err := setSessionOption(r.ctx, session, key, plan.Options[key]); err != nil {
			return session, err
		}
	}
	waitForPrompt := plan.Readiness == "always"
	if plan.Readiness == "auto" {
		shell, err := query(r.ctx, server, "show-options", "-A", "-v", "-t", session.ID().String(), "default-shell")
		if err != nil {
			return session, err
		}
		waitForPrompt = filepath.Base(shell) == "zsh"
	}
	var focus tmux.Window
	baseIndex := bootstrap.Index()
	if created {
		value, err := query(r.ctx, server, "show-options", "-A", "-v", "-t", session.ID().String(), "base-index")
		if err != nil {
			return session, err
		}
		baseIndex, err = strconv.Atoi(value)
		if err != nil {
			return session, fmt.Errorf("decode base-index: %w", err)
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
		window, err := session.NewWindow(r.ctx, tmux.NewWindowRequest{Name: name, Index: requestedIndex, StartDirectory: first.Directory, Command: first.Shell, Environment: first.Environment, KillExisting: created && index == 0 && requestedIndex != nil && *requestedIndex == bootstrap.Index()})
		if err != nil {
			return session, err
		}
		if created && index == 0 && window.Index() != bootstrap.Index() {
			if err := bootstrap.Kill(r.ctx); err != nil {
				return session, err
			}
		}
		if err := r.event("window-created", map[string]any{"input_index": inputIndex, "session_id": session.ID().String(), "window_id": window.ID().String(), "window_index": window.Index(), "window_name": wp.Name, "pane_total": len(wp.Panes)}); err != nil {
			return session, err
		}
		for _, key := range sortedKeys(wp.Options) {
			if err := window.SetOption(r.ctx, key, wp.Options[key], tmux.SetOptionOptions{}); err != nil {
				return session, err
			}
		}
		pane, ok, err := window.ResolveActivePane(r.ctx)
		if err != nil {
			return session, err
		}
		if !ok {
			return session, errors.New("new window has no active pane")
		}
		var focusedPane tmux.Pane
		for pi, pp := range wp.Panes {
			if pi > 0 {
				pane, err = pane.Split(r.ctx, tmux.SplitPaneRequest{Direction: tmux.PaneDirectionBelow, StartDirectory: pp.Directory, Command: pp.Shell, Environment: pp.Environment})
				if err != nil {
					return session, err
				}
			}
			if err := r.event("pane-created", map[string]any{"input_index": inputIndex, "session_id": session.ID().String(), "window_id": window.ID().String(), "pane_id": pane.ID().String(), "pane_index": pane.Index()}); err != nil {
				return session, err
			}
			if waitForPrompt && pp.Shell == "" {
				ready, err := paneReady(r.ctx, pane)
				if err != nil {
					return session, err
				}
				if !ready {
					if err := r.event("warning", map[string]any{"input_index": inputIndex, "pane_id": pane.ID().String(), "code": "pane_readiness_timeout", "message": "pane prompt did not move the cursor within two seconds"}); err != nil {
						return session, err
					}
				}
			}
			for _, command := range pp.Commands {
				if err := delay(r.ctx, command.SleepBefore); err != nil {
					return session, err
				}
				text := command.Text
				if err := pane.SendKeys(r.ctx, tmux.SendKeysRequest{Command: &text, SuppressHistory: pp.SuppressHistory, SkipEnter: !command.Enter, Literal: true}); err != nil {
					return session, err
				}
				if err := delay(r.ctx, command.SleepAfter); err != nil {
					return session, err
				}
			}
			if pp.Focus {
				focusedPane = pane
			}
			if err := r.event("pane-completed", map[string]any{"input_index": inputIndex, "pane_id": pane.ID().String()}); err != nil {
				return session, err
			}
			if pi > 0 {
				if err := window.SelectLayout(r.ctx, tmux.SelectLayoutRequest{Layout: "tiled"}); err != nil {
					return session, err
				}
			}
		}
		if wp.Layout != "" {
			if err := window.SelectLayout(r.ctx, tmux.SelectLayoutRequest{Layout: wp.Layout}); err != nil {
				return session, err
			}
		}
		for _, key := range sortedKeys(wp.OptionsAfter) {
			if err := window.SetOption(r.ctx, key, wp.OptionsAfter[key], tmux.SetOptionOptions{}); err != nil {
				return session, err
			}
		}
		if focusedPane.ID() != "" {
			if _, err := focusedPane.Select(r.ctx, tmux.PaneSelectRequest{}); err != nil {
				return session, err
			}
		}
		if wp.Focus || focus.ID() == "" {
			focus = window
		}
		if err := r.event("window-completed", map[string]any{"input_index": inputIndex, "window_id": window.ID().String()}); err != nil {
			return session, err
		}
	}
	if focus.ID() != "" {
		if _, err := focus.Select(r.ctx); err != nil {
			return session, err
		}
	}
	return session, nil
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

func setSessionOption(ctx context.Context, session tmux.Session, name, value string) error {
	first := session.SetOption(ctx, name, value, tmux.SetOptionOptions{})
	if first == nil {
		return nil
	}
	window, err := session.ResolveActiveWindow(ctx)
	if err == nil {
		if err := window.SetOption(ctx, name, value, tmux.SetOptionOptions{}); err == nil {
			return nil
		}
	}
	return first
}

func (r *invocation) freeze(_ *cobra.Command, o *options, args []string) error {
	if err := validateFormat(o.format); err != nil {
		return err
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
	doc, err := capture(r.ctx, session)
	if err != nil {
		return err
	}
	warnings := []string{"capture cannot recover original commands, process arguments, shell history, plugins or before_script"}
	if r.ndjson && o.saveTo == "" {
		return r.result(map[string]any{"status": "ok", "workspace": doc, "warnings": warnings})
	}
	if !r.machine() {
		if err := r.confirm("Freeze session", o.yes); err != nil {
			return err
		}
		if o.format == "" {
			o.format, err = r.prompt("Workspace format (yaml/json)", "yaml")
			if err != nil {
				return err
			}
			if err := validateFormat(o.format); err != nil {
				return err
			}
		}
	}
	return r.documentResult(o, doc, "", "yaml", warnings)
}

func capture(ctx context.Context, session tmux.Session) (document, error) {
	name, _ := session.Name()
	doc := document{"session_name": name}
	windows, ok := session.Windows()
	if !ok {
		return nil, errors.New("capture session lacks snapshot relations")
	}
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
		wp["options"] = options
		panes, _ := window.Panes()
		pp := []any{}
		for _, pane := range panes {
			cwd, _ := pane.CurrentPath()
			command, _ := pane.CurrentCommand()
			active, _ := pane.Active()
			if strings.HasPrefix(command, "-") || strings.HasSuffix(command, "python") || strings.HasSuffix(command, "ruby") || strings.HasSuffix(command, "node") {
				command = ""
			}
			p := document{"start_directory": cwd, "focus": active}
			if command != "" {
				p["shell_command"] = command
			}
			pp = append(pp, p)
		}
		wp["panes"] = pp
		result = append(result, wp)
	}
	doc["windows"] = result
	return doc, nil
}
