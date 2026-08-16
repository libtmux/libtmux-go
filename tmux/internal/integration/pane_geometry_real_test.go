//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.pane.Pane.resize
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:adjustment:73daa10a3099
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:adjustment_direction:e4a3795db9a4
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:height,width:a104fc5529e1
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:height:3fde596c5d4b
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:height:555e479dfb49
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:mouse:61c1f4bb05bb
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:trim_below:39d89b9f4f73
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:width:483866c13c9b
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:width:84d6a1e76504
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:zoom:629cd868ae3d
// libtmux:parity libtmux.pane.Pane.set_height
// libtmux:parity libtmux.pane.Pane.set_title
// libtmux:parity libtmux.pane.Pane.set_width
//
//libtmux:real-tmux
func TestPaneGeometryAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}
	base := panes[0]
	resizeResult, resizeErr := server.Cmd(
		ctx, "resize-window", "-t", base.WindowID().String(), "-x", "100", "-y", "30",
	)
	requirePaneGeometryCommandSuccess(
		t,
		"resize-window",
		resizeResult,
		resizeErr,
	)

	horizontal := paneFromGeometryCommand(
		ctx,
		t,
		server,
		"split-window",
		"-h",
		"-d",
		"-t",
		base.ID().String(),
		"-P",
		"-F",
		"#{pane_id}",
		"sleep 30",
	)
	horizontal, err = horizontal.SetWidth(ctx, 20)
	if err != nil {
		t.Fatalf("SetWidth() error = %v", err)
	}
	if right, ok := horizontal.AtRight(); !ok || !right {
		t.Fatalf("horizontal AtRight() = (%t, %t), want (true, true)", right, ok)
	}
	if left, ok := horizontal.AtLeft(); !ok || left {
		t.Fatalf("horizontal AtLeft() = (%t, %t), want (false, true)", left, ok)
	}
	if width, _ := horizontal.Width(); width != 20 {
		t.Fatalf("SetWidth() pane width = %d, want 20", width)
	}
	adjustment := 3
	horizontal, err = horizontal.Resize(ctx, tmux.ResizePaneRequest{
		Direction: tmux.PaneResizeDirectionLeft, Adjustment: adjustment,
	})
	if err != nil {
		t.Fatalf("Resize(direction) error = %v", err)
	}
	if width, _ := horizontal.Width(); width != 23 {
		t.Fatalf("directional resize pane width = %d, want 23", width)
	}

	horizontal, err = horizontal.SetTitle(ctx, "go-#{pane_id};")
	if err != nil {
		t.Fatalf("SetTitle() error = %v", err)
	}
	if title, _ := horizontal.Title(); title != "go-"+horizontal.ID().String()+";" {
		t.Fatalf("SetTitle() pane title = %q, want expanded stable pane id", title)
	}

	verticalBase := paneFromGeometryCommand(
		ctx,
		t,
		server,
		"new-window",
		"-d",
		"-t",
		"work",
		"-P",
		"-F",
		"#{pane_id}",
		"sleep 30",
	)
	resizeResult, resizeErr = server.Cmd(
		ctx, "resize-window", "-t", verticalBase.WindowID().String(), "-x", "100", "-y", "30",
	)
	requirePaneGeometryCommandSuccess(
		t,
		"resize-window",
		resizeResult,
		resizeErr,
	)
	vertical := paneFromGeometryCommand(
		ctx,
		t,
		server,
		"split-window",
		"-v",
		"-d",
		"-t",
		verticalBase.ID().String(),
		"-P",
		"-F",
		"#{pane_id}",
		"sleep 30",
	)
	vertical, err = vertical.SetHeight(ctx, 8)
	if err != nil {
		t.Fatalf("SetHeight() error = %v", err)
	}
	if bottom, ok := vertical.AtBottom(); !ok || !bottom {
		t.Fatalf("vertical AtBottom() = (%t, %t), want (true, true)", bottom, ok)
	}
	if top, ok := vertical.AtTop(); !ok || top {
		t.Fatalf("vertical AtTop() = (%t, %t), want (false, true)", top, ok)
	}
	if height, _ := vertical.Height(); height != 8 {
		t.Fatalf("SetHeight() pane height = %d, want 8", height)
	}
	vertical, err = vertical.Resize(ctx, tmux.ResizePaneRequest{Height: tmux.PanePercent(25)})
	if err != nil {
		t.Fatalf("Resize(percent) error = %v", err)
	}
	height := paneGeometryFormatInt(t, vertical.Height)
	windowHeight := paneGeometryFormatInt(t, vertical.Formats().WindowHeight)
	if scaled := height * 100; scaled < windowHeight*15 || scaled > windowHeight*35 {
		t.Fatalf("percentage pane height = %d of %d, want approximately 25%%", height, windowHeight)
	}

	vertical, err = vertical.Resize(ctx, tmux.ResizePaneRequest{Zoom: true})
	if err != nil {
		t.Fatalf("Resize(Zoom) error = %v", err)
	}
	if zoomed, _ := vertical.Formats().WindowZoomedFlag(); !zoomed {
		t.Fatal("Resize(Zoom) window_zoomed_flag = false, want true")
	}
	vertical, err = vertical.Resize(ctx, tmux.ResizePaneRequest{Zoom: true})
	if err != nil {
		t.Fatalf("Resize(unzoom) error = %v", err)
	}
	if zoomed, _ := vertical.Formats().WindowZoomedFlag(); zoomed {
		t.Fatal("Resize(unzoom) window_zoomed_flag = true, want false")
	}
	if _, err := vertical.Resize(ctx, tmux.ResizePaneRequest{TrimBelow: true}); err != nil {
		t.Fatalf("Resize(TrimBelow) error = %v", err)
	}
}

func paneFromGeometryCommand(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	arguments ...string,
) tmux.Pane {
	t.Helper()
	result, err := server.Cmd(ctx, arguments...)
	requirePaneGeometryCommandSuccess(t, arguments[0], result, err)
	if len(result.Stdout) != 1 {
		t.Fatalf("%s stdout = %#v, want one pane id", arguments[0], result.Stdout)
	}
	pane, err := server.Pane(ctx, tmux.PaneID(result.Stdout[0]))
	if err != nil {
		t.Fatalf("Pane(%s) error = %v", result.Stdout[0], err)
	}
	return pane
}

func requirePaneGeometryCommandSuccess(
	t *testing.T,
	operation string,
	result tmux.CommandResult,
	err error,
) {
	t.Helper()
	if err != nil || result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf(
			"%s = (exit %d, stderr %q, %v), want success",
			operation,
			result.ExitCode,
			result.Stderr,
			err,
		)
	}
}

func paneGeometryFormatInt(t *testing.T, accessor func() (int, bool)) int {
	t.Helper()
	value, ok := accessor()
	if !ok {
		t.Fatal("pane geometry format was not queried")
	}
	return value
}
