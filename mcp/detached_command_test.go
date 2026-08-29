package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestRunCommandTimeoutIncludesCommandSetup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{FixedShell: true})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "run-wall-clock-timeout"})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	tmuxtest.WaitForShellReady(ctx, t, pane)
	instance, _, request := connectJobSession(ctx, t, target)
	command, err := instance.runtime.command(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const gate = "run-command-setup-gate"
	held := make(chan error, 1)
	go func() {
		held <- command.WaitFor(ctx, tmux.WaitForRequest{Channel: gate})
	}()
	waitForWaitTextLaneBlock(t, ctx, command)
	released := make(chan error, 1)
	time.AfterFunc(1250*time.Millisecond, func() {
		if err := target.WaitFor(ctx, tmux.WaitForRequest{
			Channel: gate, Mode: tmux.WaitForModeSignal,
		}); err != nil {
			released <- err
			return
		}
		released <- <-held
	})

	startedAt := time.Now()
	_, output, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: pane.ID().String(), Command: "sleep 300", TimeoutSeconds: 1,
	})
	elapsed := time.Since(startedAt)
	if releaseErr := <-released; releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if err != nil || !output.TimedOut ||
		output.EffectiveTimeoutSeconds != 1 || output.TimeoutClamped {
		t.Fatalf("timed run_command = (%+v, %v)", output, err)
	}
	if elapsed < 750*time.Millisecond || elapsed > 1500*time.Millisecond {
		t.Fatalf("run_command elapsed = %v, want whole-operation one-second budget", elapsed)
	}
}

//libtmux:real-tmux
func TestDetachedAdmissionPrecedesPaneDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{
		Name: "job-capacity-admission", Command: "sleep 300",
	})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	instance, serverSession, request := connectJobSession(ctx, t, target)
	for index := range jobsRetained {
		id := fmt.Sprintf("job-%02d", index)
		if err := serverSession.scope.jobs.keep(&job{id: id}); err != nil {
			t.Fatal(err)
		}
		if _, elected, _, found := serverSession.scope.jobs.beginCollection(id); !found || !elected {
			t.Fatalf("job %d did not elect a collector", index)
		}
	}

	_, _, err = instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: pane.ID().String(), Command: "echo MUST-NOT-BE-DELIVERED", Detach: true,
	})
	if !errors.Is(err, errJobCapacity) {
		t.Fatalf("runCommand() error = %v, want %v", err, errJobCapacity)
	}
	time.Sleep(100 * time.Millisecond)
	lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(lines, "\n"), "libtmux-mcp-run") {
		t.Fatalf("a refused detached command still reached the pane: %q", lines)
	}
}

//libtmux:real-tmux
func TestOneJobCompletionDoesNotFinishAnother(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	firstSession, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "job-shared-first"})
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "job-shared-second"})
	if err != nil {
		t.Fatal(err)
	}
	firstPane, ok, err := firstSession.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve first pane = (%v, %t, %v)", firstPane, ok, err)
	}
	secondPane, ok, err := secondSession.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve second pane = (%v, %t, %v)", secondPane, ok, err)
	}
	instance, serverSession, request := connectJobSession(ctx, t, target)

	_, first, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: firstPane.ID().String(), Command: "tmux wait-for job-gate-first; echo FIRST", Detach: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: secondPane.ID().String(), Command: "tmux wait-for job-gate-second; echo SECOND", Detach: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstJob, firstFound := serverSession.scope.jobs.find(first.JobID)
	if !firstFound {
		t.Fatal("detached jobs were not retained")
	}
	if first.JobID == second.JobID {
		t.Fatalf("job ids = (%q, %q), want distinct handles", first.JobID, second.JobID)
	}

	type result struct {
		output getJobOutput
		err    error
	}
	returned := make(chan result, 1)
	go func() {
		_, output, err := instance.tools.getJob(ctx, request, getJobInput{
			JobID: second.JobID, TimeoutSeconds: 10,
		})
		returned <- result{output: output, err: err}
	}()
	waitForJobCollection(t, ctx, serverSession.scope.jobs, second.JobID)
	if err := target.WaitFor(ctx, tmux.WaitForRequest{
		Channel: "job-gate-first", Mode: tmux.WaitForModeSignal,
	}); err != nil {
		t.Fatal(err)
	}
	waitForPath(t, ctx, firstJob.closedAt)
	select {
	case got := <-returned:
		t.Fatalf("another job's completion ended the wait: (%+v, %v)", got.output, got.err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := target.WaitFor(ctx, tmux.WaitForRequest{
		Channel: "job-gate-second", Mode: tmux.WaitForModeSignal,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-returned:
		if got.err != nil || !got.output.Finished || got.output.ExitStatus == nil ||
			*got.output.ExitStatus != 0 ||
			!strings.Contains(strings.Join(got.output.Output, "\n"), "SECOND") {
			t.Fatalf("second job collection = (%+v, %v)", got.output, got.err)
		}
	case <-ctx.Done():
		t.Fatalf("second job did not finish: %v", ctx.Err())
	}
}

//libtmux:real-tmux
func TestCompletedJobsDoNotDependOnSharedSignalEdges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{FixedShell: true})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "job-signal-edge"})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	tmuxtest.WaitForShellReady(ctx, t, pane)
	instance, _, request := connectJobSession(ctx, t, target)

	_, first, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: pane.ID().String(), Command: "echo FIRST", TimeoutSeconds: 5,
	})
	if err != nil || first.ExitStatus == nil || *first.ExitStatus != 0 {
		t.Fatalf("first run = (%+v, %v)", first, err)
	}
	_, detached, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: pane.ID().String(), Command: "echo SECOND", Detach: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	began := time.Now()
	_, collected, err := instance.tools.getJob(ctx, request, getJobInput{
		JobID: detached.JobID, TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(began); elapsed >= 4*time.Second {
		t.Fatalf("completed get_job waited for its signal deadline: %v", elapsed)
	}
	if !collected.Finished || collected.ExitStatus == nil || *collected.ExitStatus != 0 ||
		!strings.Contains(strings.Join(collected.Output, "\n"), "SECOND") {
		t.Fatalf("completed get_job = %+v", collected)
	}
}

//libtmux:real-tmux
func TestRunCommandCannotEscapeItsBookkeepingSubshell(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{FixedShell: true})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "command-wrapper"})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	tmuxtest.WaitForShellReady(ctx, t, pane)
	instance, _, request := connectJobSession(ctx, t, target)
	escaped := filepath.Join(t.TempDir(), "escaped")

	_, output, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID:         pane.ID().String(),
		Command:        ") ; printf ESCAPED > " + shellQuote(escaped) + "; #",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(escaped); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid command escaped the bookkeeping subshell: %v", err)
	}
	if output.ExitStatus == nil || *output.ExitStatus == 0 || output.TimedOut {
		t.Fatalf("invalid command result = %+v, want a recorded nonzero status", output)
	}
}

//libtmux:real-tmux
func TestRunCommandBookkeepingUsesTheConfiguredTmuxExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bookkeeping wrapper is a POSIX shell script")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux is not installed: %v", err)
	}
	realTmux, err = filepath.Abs(realTmux)
	if err != nil {
		t.Fatal(err)
	}
	proxyDirectory := filepath.Join(t.TempDir(), "configured proxy")
	if err := os.Mkdir(proxyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	proxy := filepath.Join(proxyDirectory, "custom tmux")
	proxyScript := "#!/bin/sh\nexec " + shellQuote(realTmux) + " \"$@\"\n"
	if err := os.WriteFile(proxy, []byte(proxyScript), 0o700); err != nil {
		t.Fatal(err)
	}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		Binary: proxy, FixedShell: true,
	})
	panePath := filepath.Join(t.TempDir(), "pane-path-without-tmux")
	if err := os.Mkdir(panePath, 0o700); err != nil {
		t.Fatal(err)
	}
	mv, err := exec.LookPath("mv")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mv, filepath.Join(panePath, "mv")); err != nil {
		t.Fatal(err)
	}

	for _, detached := range []bool{false, true} {
		name := "waited"
		if detached {
			name = "detached"
		}
		t.Run(name, func(t *testing.T) {
			paneShell := "PATH=" + shellQuote(panePath) + " ENV= PS1=" +
				shellQuote(tmuxtest.ShellPrompt) + " /bin/sh -i"
			created, err := target.NewSession(ctx, tmux.NewSessionRequest{
				Name: "configured-binary-" + name, Command: paneShell,
			})
			if err != nil {
				t.Fatal(err)
			}
			pane, ok, err := created.ResolveActivePane(ctx)
			if err != nil || !ok {
				t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
			}
			tmuxtest.WaitForShellReady(ctx, t, pane)
			assertPaneCannotResolveTmux(ctx, t, pane)
			instance, _, request := connectJobSession(ctx, t, target)
			marker := strings.ToUpper(name) + "-CUSTOM-BINARY"
			_, started, err := instance.tools.runCommand(ctx, request, runCommandInput{
				PaneID: pane.ID().String(), Command: "printf " + marker,
				TimeoutSeconds: 2, Detach: detached,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !detached {
				if started.TimedOut || started.ExitStatus == nil || *started.ExitStatus != 0 ||
					!strings.Contains(strings.Join(started.Output, "\n"), marker) {
					t.Fatalf("waited run_command = %+v", started)
				}
				return
			}
			_, collected, err := instance.tools.getJob(ctx, request, getJobInput{
				JobID: started.JobID, TimeoutSeconds: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !collected.Finished || collected.ExitStatus == nil || *collected.ExitStatus != 0 ||
				!strings.Contains(strings.Join(collected.Output, "\n"), marker) {
				t.Fatalf("detached get_job = %+v", collected)
			}
		})
	}
}

func assertPaneCannotResolveTmux(
	ctx context.Context,
	t *testing.T,
	pane tmux.Pane,
) {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "tmux-resolution")
	command := "command -v tmux > " + shellQuote(probe) +
		" 2>&1 || printf MISSING > " + shellQuote(probe)
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: &command, Literal: true,
	}); err != nil {
		t.Fatal(err)
	}
	for {
		resolved, err := os.ReadFile(probe)
		if err == nil && len(resolved) > 0 {
			if got := strings.TrimSpace(string(resolved)); got != "MISSING" {
				t.Fatalf("pane resolved tmux outside its test PATH as %q", got)
			}
			return
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane PATH probe did not finish: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForJobCollection(t *testing.T, ctx context.Context, owned *jobs, id string) {
	t.Helper()
	for {
		entry, found := owned.find(id)
		if !found {
			t.Fatalf("job %q disappeared before collection", id)
		}
		if entry.collecting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("job %q was not collected: %v", id, ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func waitForPath(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s was not published: %v", filepath.Base(path), ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}
