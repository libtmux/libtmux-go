package tmux

import (
	"fmt"
	"strings"
)

// WarningKind identifies one closed class of nonfatal library warning.
type WarningKind uint8

const (
	// WarningUnsupportedFeature reports a requested feature omitted because
	// the connected tmux binary is too old.
	WarningUnsupportedFeature WarningKind = iota + 1
	// WarningCommandStderr reports stderr from a completed command whose Python
	// API treats the diagnostic as nonfatal.
	WarningCommandStderr
	// WarningControlPoolClosed reports a command that started a tmux process
	// because the control pool carrying it had been closed. The command ran
	// and its result is unchanged; only its cost is.
	WarningControlPoolClosed
)

// Warning describes one nonfatal compatibility decision delivered to a
// [WarningHandler].
type Warning struct {
	// Kind classifies the compatibility decision.
	Kind WarningKind
	// Subcommand names the tmux subcommand affected by the decision.
	Subcommand string
	// Feature names the optional tmux capability involved.
	Feature string
	// CurrentVersion is the observed tmux version.
	CurrentVersion Version
	// RequiredVersion is the minimum tmux version for Feature.
	RequiredVersion Version
	// Message describes the nonfatal compatibility decision.
	Message string
}

// WarningHandler receives warnings synchronously on the operation's caller
// goroutine. It is a function type rather than an interface, so a handler is
// written as a literal and needs no adapter:
//
//	tmux.NewServer(tmux.ServerOptions{
//		WarningHandler: func(warning tmux.Warning) {
//			log.Printf("tmux: %s", warning.Message)
//		},
//	})
//
// Server operations may invoke the handler concurrently; callers
// must synchronize any shared handler state. The library starts no goroutine
// for warning delivery. Command diagnostics may contain caller-supplied tmux
// arguments; the library delivers them only to this handler and does not log
// them.
type WarningHandler func(Warning)

// String implements fmt.Stringer, so a warning can be logged or printed
// without a caller reaching for a field. It reports the message, which already
// names the subcommand and the decision.
func (w Warning) String() string { return w.Message }

func (s Server) warn(warning Warning) {
	handler := s.connectionState().options.WarningHandler
	if handler != nil {
		handler(warning)
	}
}

func newControlPoolClosedWarning(kind CommandKind) Warning {
	return Warning{
		Kind: WarningControlPoolClosed,
		Message: "control pool is closed: " + kind.String() +
			" started a tmux process instead",
	}
}

func newCommandStderrWarning(subcommand string, stderr []string) Warning {
	return Warning{
		Kind:       WarningCommandStderr,
		Subcommand: subcommand,
		Message:    subcommand + ": " + strings.Join(stderr, "; "),
	}
}

func newUnsupportedFeatureWarning(
	subcommand string,
	feature string,
	current Version,
	required Version,
) Warning {
	return Warning{
		Kind:            WarningUnsupportedFeature,
		Subcommand:      subcommand,
		Feature:         feature,
		CurrentVersion:  current,
		RequiredVersion: required,
		Message: fmt.Sprintf(
			"%s: %s requires tmux %s or newer; current %s; feature ignored",
			subcommand,
			feature,
			required,
			current,
		),
	}
}
