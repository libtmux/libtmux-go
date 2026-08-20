package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportedWorkflowCompilesFromDownstreamModule(t *testing.T) {
	// Ask the toolchain where the module is rather than deriving it from this
	// test's own directory. This test lives one package away from the module
	// root and would otherwise point a downstream replace directive at a
	// directory holding no go.mod.
	moduleRoot, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	// The downstream module declares whatever the module under test declares.
	// Naming a version here instead means a bump to the real go.mod leaves this
	// one behind, and the toolchain then refuses the build for a reason that
	// has nothing to do with what the test is checking.
	goDirective, err := moduleGoDirective(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	module := fmt.Sprintf(`module example.invalid/libtmux-smoke

go %s

require github.com/libtmux/libtmux-go v0.0.0

replace github.com/libtmux/libtmux-go => %q
`, goDirective, moduleRoot)
	writeDownstreamFile(t, filepath.Join(directory, "go.mod"), module)
	writeDownstreamFile(t, filepath.Join(directory, "workflow_test.go"), `package smoke

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmuxq"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func compileWorkflow(ctx context.Context, t *testing.T, server tmux.Server) error {
	strict := server
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
		if window, ok := pane.Window(); ok {
			_, _ = window.Panes()
		}
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

// findModuleRoot returns the directory holding the tmux module's go.mod, as the
// go command reports it. Deriving it from a test's own directory only works
// while the test sits in the module root, which these no longer do.
func findModuleRoot() (string, error) {
	output, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", tmuxModulePath).Output()
	if err != nil {
		return "", fmt.Errorf("locate the tmux module: %w", err)
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return "", errors.New("the go command reported no directory for the tmux module")
	}
	return root, nil
}

// moduleGoDirective reports the language version the module at root declares,
// so a downstream module built against it can declare the same one.
func moduleGoDirective(root string) (string, error) {
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(contents), "\n") {
		if version, found := strings.CutPrefix(strings.TrimSpace(line), "go "); found {
			return strings.TrimSpace(version), nil
		}
	}
	return "", fmt.Errorf("%s/go.mod declares no go version", root)
}
