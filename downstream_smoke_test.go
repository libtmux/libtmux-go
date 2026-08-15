package tmux_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExportedWorkflowCompilesFromDownstreamModule(t *testing.T) {
	moduleRoot, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	module := fmt.Sprintf(`module example.invalid/libtmux-smoke

go 1.23.0

require github.com/tmux-python/libtmux/golang v0.0.0

replace github.com/tmux-python/libtmux/golang => %q
`, moduleRoot)
	writeDownstreamFile(t, filepath.Join(directory, "go.mod"), module)
	writeDownstreamFile(t, filepath.Join(directory, "workflow_test.go"), `package smoke

import (
	"context"
	"errors"
	"testing"

	tmux "github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxq"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

func compileWorkflow(ctx context.Context, t *testing.T, server tmux.Server) error {
	strict := server.WithStrictErrors()
	sessions, err := strict.Sessions(ctx)
	if err != nil {
		return err
	}
	_, err = server.Cmd(ctx, "display-message", "-p", "#{session_name}")
	if err != nil {
		return err
	}
	globalSession := strict.GlobalSessionScope()
	if _, err = globalSession.Options(ctx); err != nil {
		return err
	}
	array, err := tmux.NewSparseArray(tmux.SparseEntry[string]{Index: 2, Value: "value"})
	if err != nil {
		return err
	}
	arrayResult, err := globalSession.SetStatusFormat(ctx, array)
	if err != nil {
		return err
	}
	_, _ = arrayResult.Replaced, arrayResult.AppliedIndices
	if err = globalSession.SetStatus(ctx, tmux.StatusOn); err != nil {
		return err
	}
	if _, err = globalSession.Hooks(ctx); err != nil {
		return err
	}
	if err = globalSession.SetHook(ctx, "client-attached", "display-message attached"); err != nil {
		return err
	}
	globalWindow := server.GlobalWindowScope()
	if _, err = globalWindow.Options(ctx); err != nil {
		return err
	}
	if err = globalWindow.SetPaneBorderStatus(ctx, tmux.PaneBorderStatusTop); err != nil {
		return err
	}
	if _, err = globalWindow.SetPaneColours(ctx, array); err != nil {
		return err
	}
	if _, err = globalWindow.Hooks(ctx); err != nil {
		return err
	}
	snapshot, err := strict.Snapshot(ctx)
	if err != nil {
		return err
	}
	predicate, err := tmux.SessionNameIs("work").Predicate()
	if err != nil {
		return err
	}
	_ = tmuxq.Where(snapshot.Sessions(), predicate)
	for _, pane := range snapshot.Panes() {
		_, _ = pane.Active()
		_, _ = pane.Formats().Raw("pane_active")
	}
	filter := tmux.TmuxFilter("#{==:#{session_name},work}")
	_, err = strict.SearchSessions(ctx, &filter)
	if err != nil {
		return err
	}
	if len(sessions) != 0 {
		if _, err = sessions[0].SetUpdateEnvironment(ctx, array); err != nil {
			return err
		}
		if err = sessions[0].SetMouse(ctx, true); err != nil {
			return err
		}
		options, optionErr := sessions[0].Options(ctx)
		if optionErr != nil {
			return optionErr
		}
		_, _ = options.Mouse().Get()
	}
	owned := tmuxtest.NewServer(context.Background(), t)
	session := tmuxtest.NewSession(ctx, t, owned, tmux.NewSessionRequest{})
	_ = tmuxtest.NewWindow(ctx, t, session, tmux.NewWindowRequest{})
	var commandError *tmux.CommandError
	_ = errors.As(err, &commandError)
	var optionValueError *tmux.OptionValueError
	_ = errors.As(err, &optionValueError)
	return nil
}
`)

	command := exec.CommandContext(context.Background(), "go", "test", "-run", "^$", "./...")
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("downstream go test: %v\n%s", err, output)
	}
}

func writeDownstreamFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
