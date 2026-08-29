package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// UnsupportedPolicy selects what a request does when it needs an optional tmux
// capability the running server does not have — a flag added in a later tmux
// than the one answering.
//
// The default refuses, because dropping a flag changes what the command does.
// A split asked to leave a pane empty otherwise starts a shell in it, a
// run-shell asked for arguments runs without them, and a kill-session asked for
// a session group takes one session instead of the group. Each returns success
// while doing something the caller did not ask for.
//
// Choosing degradation is how a program stays portable across the supported
// tmux range when the capability is cosmetic and its absence is acceptable:
//
//	server := tmux.NewServer(tmux.ServerOptions{
//		Unsupported:    tmux.DegradeUnsupported,
//		WarningHandler: func(w tmux.Warning) { log.Printf("tmux: %s", w) },
//	})
//
// Set [ServerOptions.WarningHandler] alongside it. Degradation with no handler
// is the silence this setting exists to make deliberate.
type UnsupportedPolicy uint8

const (
	// FailUnsupported refuses a request naming a capability the running tmux
	// does not have, with a VersionTooLowError naming the subcommand and the
	// capability. It is the zero value.
	FailUnsupported UnsupportedPolicy = iota
	// DegradeUnsupported omits the capability, runs the reduced command, and
	// reports the decision to ServerOptions.WarningHandler as a
	// WarningUnsupportedFeature.
	DegradeUnsupported
)

// String implements fmt.Stringer.
func (p UnsupportedPolicy) String() string {
	switch p {
	case FailUnsupported:
		return "fail"
	case DegradeUnsupported:
		return "degrade"
	default:
		return "UnsupportedPolicy(" + strconv.Itoa(int(p)) + ")"
	}
}

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
	// WarningControlPoolUnused reports a command that started a tmux process
	// while a control pool was open on the same configuration, which happens
	// when the record issuing it was materialized before the pool existed and
	// so kept the handle it was made on. The command ran and its result is
	// unchanged; only its cost is.
	WarningControlPoolUnused
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

func newControlPoolUnusedWarning() Warning {
	return Warning{
		Kind: WarningControlPoolUnused,
		Message: "a control pool is open on this server, but this command " +
			"started a tmux process: the record issuing it predates the pool; " +
			"move it with WithEngine",
	}
}

// warnIfPoolUnused reports a command paying for a tmux process while a
// connection that could have carried it is open.
//
// It completes [WarningControlPoolClosed], which reports the same symptom for a
// pool that has been closed. Both are the cost of a record holding a handle
// other than the one the caller meant, and reporting only one of them left the
// commoner case -- a record materialized before the pool -- to be measured
// rather than told.
//
// Only [CommandServer] is worth reporting. A process command needs its own
// process whatever is open, and a handle that gave up its engine on purpose,
// for a read whose result is tmux's exact stdout bytes, is not paying for
// anything it did not choose.
func (s Server) warnIfPoolUnused(kind CommandKind) {
	if kind != CommandServer || s.engineless {
		return
	}
	if s.connectionState().coordination().pools.Load() == 0 {
		return
	}
	s.warn(newControlPoolUnusedWarning())
}

func newCommandStderrWarning(subcommand string, stderr []string) Warning {
	return Warning{
		Kind:       WarningCommandStderr,
		Subcommand: subcommand,
		Message:    subcommand + ": " + strings.Join(stderr, "; "),
	}
}

// unsupportedFeature reports what a request should do about an optional tmux
// capability the running server does not have. It returns an error under
// [FailUnsupported], and under [DegradeUnsupported] it reports the decision to
// [WarningHandler] and returns nil so the caller runs the reduced command.
//
// The caller keeps the branch that decides which flag to omit, because only it
// knows which one this is.
func (s Server) unsupportedFeature(
	subcommand string,
	feature string,
	current Version,
	required Version,
) error {
	if s.connectionState().options.Unsupported == FailUnsupported {
		return &VersionTooLowError{
			Current:    current,
			Minimum:    required,
			Subcommand: subcommand,
			Feature:    feature,
		}
	}
	s.warn(newUnsupportedFeatureWarning(subcommand, feature, current, required))
	return nil
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

// reportUnsupported applies the server's [UnsupportedPolicy] to the warnings an
// argv renderer produced.
//
// The renderers that gate on the tmux version perform no I/O, so a [Plan] can
// render one without a server. They report a dropped capability rather than
// deciding what to do about it, and this is where that decision is made.
func (s Server) reportUnsupported(warnings []Warning) error {
	for _, warning := range warnings {
		if warning.Kind != WarningUnsupportedFeature {
			s.warn(warning)
			continue
		}
		if err := s.unsupportedFeature(
			warning.Subcommand,
			warning.Feature,
			warning.CurrentVersion,
			warning.RequiredVersion,
		); err != nil {
			return err
		}
	}
	return nil
}

// reportUnsupported applies the run's policy to the warnings one step's argv
// renderer produced. It mirrors the [Server] method so that a step refuses
// exactly what the same request refuses when issued directly.
func (p *Plan) reportUnsupported(warnings []Warning) error {
	if p.unsupported == DegradeUnsupported {
		return nil
	}
	for _, warning := range warnings {
		if warning.Kind != WarningUnsupportedFeature {
			continue
		}
		return &VersionTooLowError{
			Current:    warning.CurrentVersion,
			Minimum:    warning.RequiredVersion,
			Subcommand: warning.Subcommand,
			Feature:    warning.Feature,
		}
	}
	return nil
}
