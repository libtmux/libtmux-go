package tmux

import (
	"slices"
	"testing"
)

func TestServerProcessEnvironmentReturnsAnOwnedConfiguration(t *testing.T) {
	t.Parallel()

	configured := []string{"LIBTMUX_ONE=one", "LIBTMUX_TWO=two"}
	server := NewServer(ServerOptions{ProcessEnvironment: configured})
	got := server.ProcessEnvironment()
	if !slices.Equal(got, configured) {
		t.Fatalf("ProcessEnvironment() = %#v, want %#v", got, configured)
	}
	got[0] = "LIBTMUX_ONE=mutated"
	if again := server.ProcessEnvironment(); !slices.Equal(again, configured) {
		t.Fatalf("ProcessEnvironment() after caller mutation = %#v, want %#v", again, configured)
	}

	inherited := NewServer(ServerOptions{})
	if inherited.ProcessEnvironment() != nil {
		t.Fatalf("inherited ProcessEnvironment() = %#v, want nil", inherited.ProcessEnvironment())
	}
	empty := NewServer(ServerOptions{ProcessEnvironment: []string{}})
	if got := empty.ProcessEnvironment(); got == nil || len(got) != 0 {
		t.Fatalf("empty ProcessEnvironment() = %#v, want nonnil empty", got)
	}
}
