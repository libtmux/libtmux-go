package tmux

import (
	"errors"
	"testing"
)

// Each case is one literal command (or a ";" list) as a caller would send it
// to Server.Cmd, Connection.Call or ControlClient.Call, resolved the way tmux's
// cmd_find resolves a name: an exact alias, otherwise a unique prefix.
func TestLiteralCommandGatesResolveNamesLikeTmux(t *testing.T) {
	t.Parallel()

	const classic = "c195,80x24,0,0[80x12,0,0,0,80x11,0,13,1]"
	for _, test := range []struct {
		name    string
		version string
		list    bool
		args    []string
		refused bool
	}{
		{"select-layout garbage", "3.3a", false, []string{"select-layout", "-t", "@1", "garbage"}, true},
		{"selectl garbage", "3.3a", false, []string{"selectl", "-t", "@1", "garbage"}, true},
		{"select-l garbage", "3.3a", false, []string{"select-l", "-t", "@1", "garbage"}, true},
		{"select-layout after list", "3.3a", true, []string{"list-windows", ";", "selectl", "-t", "@1", "zzz"}, true},
		{"select-layout joined list", "3.3a", true, []string{"list-windows;", "select-layout", "zzz"}, true},
		{"dash dash then -o", "3.3a", false, []string{"select-layout", "-t", "@1", "--", "-o"}, true},
		{"empty operand", "3.3a", false, []string{"select-layout", "-t", "@1", ""}, true},
		{"ambiguous prefix", "3.3a", false, []string{"select-layout", "-t", "@1", "main-"}, true},
		{"json layout", "3.3a", false, []string{"select-layout", "-t", "@1", `{"a":1}`}, true},
		{"undo", "3.3a", false, []string{"select-layout", "-t", "@1", "-o"}, true},
		{"undo with preset", "3.3a", false, []string{"selectl", "-o", "tiled"}, true},
		{"joined target flag", "3.3a", false, []string{"select-layout", "-t@1", "garbage"}, true},
		{"preset", "3.3a", false, []string{"select-layout", "-t", "@1", "tiled"}, false},
		{"preset prefix", "3.3a", false, []string{"select-layout", "-t", "@1", "even-h"}, false},
		{"classic layout", "3.3a", false, []string{"select-layout", "-t", "@1", classic}, false},
		{"reapply last", "3.3a", false, []string{"select-layout", "-t", "@1"}, false},
		{"next", "3.3a", false, []string{"select-layout", "-t", "@1", "-n"}, false},
		{"spread", "3.3a", false, []string{"select-layout", "-t", "@1", "-E"}, false},
		{"previous", "3.3a", false, []string{"select-layout", "-t", "@1", "-p"}, false},
		{"other command named like it", "3.3a", false, []string{"select-pane", "-t", "@1", "garbage"}, false},
		{"fixed in 3.4", "3.4", false, []string{"select-layout", "-t", "@1", "garbage"}, false},
		{"not a 3.3a defect", "3.7", false, []string{"select-layout", "-t", "@1", "-o"}, false},

		{"new alias names a session", "3.7", false, []string{"new", "-d", "-s", "c.d"}, true},
		{"new alias in a list", "3.7", true, []string{"list-windows", ";", "new", "-d", "-s", "bb.cc"}, true},
		{"new-s prefix", "3.7", false, []string{"new-s", "-d", "-s", "c:d"}, true},
		{"new-w prefix", "3.7", false, []string{"new-w", "-n", "c.d"}, true},
		{"neww alias", "3.7", false, []string{"neww", "-n", "c.d"}, true},
		{"new alias, plain name", "3.7", false, []string{"new", "-d", "-s", "plain"}, false},
		{"new alias names the first window", "3.7", false, []string{"new", "-d", "-n", "c.d"}, true},
		{"new alias on 3.7a", "3.7a", false, []string{"new", "-d", "-s", "c.d"}, false},

		{"refresh-client -l", "3.3a", false, []string{"refresh-client", "-l", "-t", "/dev/pts/1"}, true},
		{"refresh alias", "3.3a", false, []string{"refresh", "-t", "/dev/pts/1", "-l"}, true},
		{"ref prefix", "3.3a", false, []string{"ref", "-lt", "/dev/pts/1"}, true},
		{"refresh-c prefix in a list", "3.3a", true, []string{"list-clients", ";", "refresh-c", "-l"}, true},
		{"re is ambiguous", "3.3a", false, []string{"re", "-l"}, false},
		{"refresh-client without -l", "3.3a", false, []string{"refresh-client", "-S", "-t", "/dev/pts/1"}, false},
		{"l as a flag value", "3.3a", false, []string{"refresh-client", "-f", "l"}, false},
		{"l joined to a valued flag", "3.3a", false, []string{"refresh-client", "-fl"}, false},
		{"l joined to the target flag", "3.3a", false, []string{"refresh-client", "-tl"}, false},
		{"l joined to the client size flag", "3.3a", false, []string{"refresh-client", "-Cl"}, false},
		{"l after a valued flag's value", "3.3a", false, []string{"refresh-client", "-fl", "-l"}, true},
		{"refresh-client -l on 3.4", "3.4", false, []string{"refresh-client", "-l", "-t", "/dev/pts/1"}, true},
		{"refresh-client -l on 3.5a", "3.5a", false, []string{"refresh-client", "-l", "-t", "/dev/pts/1"}, true},
		{"refresh-client -l on 3.6", "3.6", false, []string{"refresh-client", "-l", "-t", "/dev/pts/1"}, true},
		{"refresh-client -l on 3.6a", "3.6a", false, []string{"refresh-client", "-l", "-t", "/dev/pts/1"}, false},
		{"refresh-client -l on 3.8", "3.8", false, []string{"refresh-client", "-l", "-t", "/dev/pts/1"}, false},

		{"break-pane", "3.7", false, []string{"break-pane", "-d", "-s", "%1"}, true},
		{"breakp", "3.7", false, []string{"breakp", "-d", "-s", "%1"}, true},
		{"break-p", "3.7", false, []string{"break-p", "-d", "-s", "%1"}, true},
		{"br", "3.7", false, []string{"br", "-d", "-s", "%1"}, true},
		{"b is ambiguous", "3.7", false, []string{"b", "-d", "-s", "%1"}, false},
		{"break-pane on 3.7a", "3.7a", false, []string{"break-pane", "-d", "-s", "%1"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			version, err := ParseVersion(test.version)
			if err != nil {
				t.Fatal(err)
			}
			err = Server{startupVersion: &version}.checkCommandFeatures(test.args, test.list, true)
			if test.refused != errors.Is(err, ErrVersionTooLow) {
				t.Fatalf("checkCommandFeatures(%q) on %s = %v, refused want %v",
					test.args, test.version, err, test.refused)
			}
		})
	}
}
