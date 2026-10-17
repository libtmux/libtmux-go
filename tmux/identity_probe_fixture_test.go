package tmux

import (
	"strings"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// testIdentityProbeMarker pins the identity probe's random marker so fake
// executors can answer it. Native tests prove the real framing.
const testIdentityProbeMarker = identityProbeMarkerPrefix + "0000000000000000-"

func init() {
	newIdentityProbeMarker = func() string { return testIdentityProbeMarker }
}

// identityProbeResult is what tmux reports for the hook-free identity probe: a
// refusal whose command word carries the expanded record.
func identityProbeResult(fields []formatField, values map[string]string) tmuxcmd.Result {
	var record strings.Builder
	for _, field := range identityProbeOrder(fields) {
		record.WriteString(values[field.name])
		if field.name != "socket_path" {
			record.WriteByte(formatFieldSeparator)
		}
	}
	record.WriteByte(formatRecordTerminator)
	return tmuxcmd.Result{
		ExitCode: 1,
		Stderr:   []string{"unknown command: " + testIdentityProbeMarker + record.String()},
	}
}

// identityProbeArgumentsFor is the argv the probe sends for fields.
func identityProbeArgumentsFor(fields []formatField) []string {
	return identityProbeArguments(testIdentityProbeMarker, fields)
}

// identityProbeStderr is the stderr of [identityProbeResult] alone, for
// literals that already set the other result fields.
func identityProbeStderr(fields []formatField, values map[string]string) []string {
	return identityProbeResult(fields, values).Stderr
}

// isIdentityProbeRequest reports whether request is the identity probe, as
// opposed to any other run-shell.
func isIdentityProbeRequest(request tmuxcmd.Request) bool {
	arguments := exactArgvSubcommand(request.Arguments)
	return len(arguments) == 3 && arguments[0] == "run-shell" && arguments[1] == "-C" &&
		strings.HasPrefix(arguments[2], identityProbeMarkerPrefix)
}

// layoutVersionReply is tmux's refusal of the layout version probe's marker.
func layoutVersionReply(version string) tmuxcmd.Result {
	return tmuxcmd.Result{ExitCode: 1, Stderr: []string{"unknown command: " + testIdentityProbeMarker + version}}
}
