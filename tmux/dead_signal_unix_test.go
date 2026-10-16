//go:build unix

package tmux

import (
	"os"
	"syscall"
	"testing"
)

func TestDeadSignalIsTheSameOnLinuxAndMacOS(t *testing.T) {
	tests := []struct {
		reported string
		want     os.Signal
	}{
		{reported: "", want: nil},
		// Linux: the C library has no signal names, so tmux sends the number.
		{reported: "9", want: syscall.SIGKILL},
		{reported: "15", want: syscall.SIGTERM},
		// macOS and the BSDs: tmux sends the C library's name.
		{reported: "kill", want: syscall.SIGKILL},
		{reported: "KILL", want: syscall.SIGKILL},
		{reported: "term", want: syscall.SIGTERM},
		// A name with no constant here keeps tmux's spelling.
		{reported: "emt", want: reportedSignal("emt")},
	}
	for _, test := range tests {
		if got := deadSignal(test.reported); got != test.want {
			t.Errorf("deadSignal(%q) = %#v, want %#v", test.reported, got, test.want)
		}
	}
	if got := deadSignal("emt").String(); got != "emt" {
		t.Errorf("deadSignal(%q).String() = %q, want tmux's spelling", "emt", got)
	}
}
