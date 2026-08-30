//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// gatedFlag is one flag this package withholds below a tmux version.
type gatedFlag struct {
	subcommand string
	flag       string
	feature    string
	// minimum mirrors the version constant that gates the feature. It is
	// written out rather than read from the constant so that this test states
	// the boundary independently of the code it is checking.
	minimum string
	// reason is set only where the boundary is deliberately later than the
	// release tmux started offering the flag.
	reason string
}

// gatedFlags is every flag the tmux package refuses below a version.
var gatedFlags = []gatedFlag{
	{"capture-pane", "-T", "join_wrapped", "3.4", ""},
	{"capture-pane", "-M", "mouse_format", "3.6", ""},
	{"capture-pane", "-H", "hyperlinks", "3.7", ""},
	{"capture-pane", "-L", "line_numbers", "3.7", ""},
	{"capture-pane", "-F", "format", "3.7", ""},
	{"command-prompt", "-l", "literal", "3.6", ""},
	{"command-prompt", "-C", "bspace_exit", "3.7", ""},
	{"command-prompt", "-e", "no_freeze", "3.7", ""},
	{"confirm-before", "-c", "confirm_key", "3.4", ""},
	{"confirm-before", "-y", "default_yes", "3.4", ""},
	{"copy-mode", "-d", "page_down", "3.5", ""},
	{"display-menu", "-C", "starting_choice", "3.4", ""},
	{"display-menu", "-b", "border_lines", "3.4", ""},
	{"display-menu", "-s", "style", "3.4", ""},
	{"display-menu", "-S", "border_style", "3.4", ""},
	{"display-menu", "-H", "selected_style", "3.4", ""},
	{"display-menu", "-M", "mouse", "3.5", ""},
	{"display-message", "-l", "no_expand", "3.4", ""},
	{"display-message", "-C", "update_pane", "3.6", ""},
	{"display-popup", "-B", "no_border", "3.3", ""},
	{"display-popup", "-e", "environment", "3.3", ""},
	{"display-popup", "-N", "no_keys", "3.6", ""},
	{"display-popup", "-k", "close_on_any_key", "3.6", ""},
	{"kill-session", "-g", "group", "3.7", ""},
	{"list-clients", "-f", "filter", "3.4", ""},
	{"list-keys", "-F", "format", "3.7", ""},
	{"paste-buffer", "-S", "no_vis", "3.7", ""},
	{"run-shell", "-c", "start_directory", "3.4", ""},
	{"run-shell", "-E", "show_stderr", "3.6", ""},
	{"send-keys", "-K", "key_table", "3.4", ""},
	{
		subcommand: "refresh-client", flag: "-l", feature: "request_clipboard",
		minimum: "3.4",
		reason: "tmux has offered the flag since before the supported range, " +
			"but 3.2a ends the server on it for any client and 3.3a for a " +
			"client with a terminal",
	},
}

// Advertised flags and version gates must agree. A later gate needs a recorded
// tmux failure because parsing a flag does not mean the server survives it.
//
//libtmux:real-tmux
func TestNoFlagIsWithheldFromATmuxThatOffersIt(t *testing.T) {
	t.Parallel()

	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	running, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}

	for _, gate := range gatedFlags {
		minimum, err := tmux.ParseVersion(gate.minimum)
		if err != nil {
			t.Fatalf("ParseVersion(%q) = %v", gate.minimum, err)
		}
		usage, err := server.ListCommands(ctx, tmux.ListCommandsRequest{
			CommandName: &gate.subcommand,
		})
		if err != nil {
			t.Fatalf("ListCommands(%q) = %v", gate.subcommand, err)
		}
		offered := usageOffersFlag(strings.Join(usage, " "), gate.flag)
		sent := running.AtLeast(minimum)

		switch {
		case offered && !sent && gate.reason == "":
			t.Errorf("%s %s (%s) is withheld below %s, but tmux %s offers it and "+
				"no reason is recorded for holding it back",
				gate.subcommand, gate.flag, gate.feature, gate.minimum, running)
		case !offered && sent:
			t.Errorf("%s %s (%s) is sent from %s, but tmux %s does not offer it",
				gate.subcommand, gate.flag, gate.feature, gate.minimum, running)
		}
	}
}

// usageOffersFlag reports whether a tmux usage line lists the flag, reading the
// bracketed groups rather than the whole line so that a flag named in an
// argument placeholder is not mistaken for one the command takes.
func usageOffersFlag(usage, flag string) bool {
	letter := strings.TrimPrefix(flag, "-")
	for _, group := range strings.Split(usage, "[-")[1:] {
		end := strings.IndexAny(group, "] ")
		if end < 0 {
			continue
		}
		if strings.Contains(group[:end], letter) {
			return true
		}
	}
	return false
}
