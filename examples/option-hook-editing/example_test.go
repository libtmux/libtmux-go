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

const optionHookEditingArtifact = "go-option-hook-editing"

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestOptionHookEditing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resolved, err := exampletest.ResolveArenaServer(
		optionHookEditingArtifact,
		os.LookupEnv,
		func() tmux.Server { return tmuxtest.NewServer(ctx, t) },
	)
	if err != nil {
		t.Fatalf("resolve arena server: %v", err)
	}
	printed := exampletest.Output(t, func() error {
		return run(ctx, resolved.Server)
	})

	if want := "client-attached hook present: true"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain %q", printed, want)
	}
	if resolved.Active() {
		if err := resolved.EmitEvidence(ctx); err != nil {
			t.Fatalf("emit arena evidence: %v", err)
		}
	}
}
