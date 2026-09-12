package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

const controlModeSubscribeArtifact = "go-control-mode-subscribe"

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestControlModeSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resolved, err := exampletest.ResolveArenaServer(
		controlModeSubscribeArtifact,
		os.LookupEnv,
		func() tmux.Server { return tmuxtest.NewServer(ctx, t) },
	)
	if err != nil {
		t.Fatalf("resolve arena server: %v", err)
	}
	printed := exampletest.Output(t, func() error {
		return run(ctx, resolved.Server)
	})

	// run returns only after receiving the rename notification.
	if want := "heard the rename"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain %q", printed, want)
	}
	if want := "session has 2 windows"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want the subscription to report %q", printed, want)
	}
	if want := "notification:"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to name the notifications it read", printed)
	}
	if resolved.Active() {
		if err := resolved.EmitEvidence(ctx); err != nil {
			t.Fatalf("emit arena evidence: %v", err)
		}
	}
}
