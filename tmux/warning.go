package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// UnsupportedPolicy selects what a request does when it needs an optional tmux
// capability the running server does not have.
//
// The default refuses because dropping a flag can change behavior while still
// returning success.
//
// Choose degradation only when omission is acceptable:
//
//	server, err := tmux.NewServer(tmux.ServerOptions{
//		Unsupported:    tmux.DegradeUnsupported,
//		WarningHandler: func(w tmux.Warning) { log.Printf("tmux: %s", w) },
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//
// Set [ServerOptions.WarningHandler] alongside it to observe omissions.
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

// WarningHandler receives warnings synchronously on the caller goroutine:
//
//	server, err := tmux.NewServer(tmux.ServerOptions{
//		WarningHandler: func(warning tmux.Warning) {
//			log.Printf("tmux: %s", warning.Message)
//		},
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//
// Operations may invoke it concurrently; synchronize shared state. Diagnostics
// may contain caller-supplied arguments and are delivered only to this handler.
type WarningHandler func(Warning)

// String implements fmt.Stringer by returning Message.
func (w Warning) String() string { return w.Message }

func (s Server) warn(warning Warning) {
	if s.state == nil {
		return
	}
	handler := s.state.config.warningHandler
	if handler != nil {
		handler(warning)
	}
}

func newCommandStderrWarning(subcommand string, stderr []string) Warning {
	return Warning{
		Kind:       WarningCommandStderr,
		Subcommand: subcommand,
		Message:    subcommand + ": " + strings.Join(stderr, "; "),
	}
}

// unsupportedFeature either refuses or warns before the caller omits its flag.
func (s Server) unsupportedFeature(
	subcommand string,
	feature string,
	current Version,
	required Version,
) error {
	if s.state == nil || s.state.config.unsupported == FailUnsupported {
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

// reportUnsupported applies the server's policy to renderer warnings.
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

// reportUnsupported applies one run's policy to the warnings an operation's
// renderer produced. It mirrors the [Server] method so a planned operation
// refuses or reports exactly what the same request does when issued directly.
func (c planRenderContext) reportUnsupported(warnings []Warning) error {
	for _, warning := range warnings {
		if warning.Kind != WarningUnsupportedFeature {
			c.warn(warning)
			continue
		}
		if c.unsupported == FailUnsupported {
			return &VersionTooLowError{
				Current:    warning.CurrentVersion,
				Minimum:    warning.RequiredVersion,
				Subcommand: warning.Subcommand,
				Feature:    warning.Feature,
			}
		}
		c.warn(warning)
	}
	return nil
}

func (c planRenderContext) warn(warning Warning) {
	if c.warningHandler != nil {
		c.warningHandler(warning)
	}
}
