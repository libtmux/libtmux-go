package tmux

import (
	"context"
	"iter"
	"strings"

	"github.com/libtmux/libtmux-go/tmux/internal/layout"
)

// ValidateLayouts checks every layout and its required pane count before any
// mutation. Nil and empty sequences succeed without I/O unless ctx is canceled.
// Empty layout strings are ignored. Other entries require at least one pane;
// custom trees must contain enough leaves. Geometry remains tmux's responsibility.
//
// Names accept unique abbreviations. Version-sensitive names query the selected
// daemon through this server's transport; only a cold endpoint without a captured
// daemon identity or control connection uses the configured binary's version.
// Permission, protocol, decode and cancellation errors are preserved.
// Validation errors match
// [ErrInvalidServerCommandRequest]; unavailable presets match [ErrVersionTooLow].
func (s Server) ValidateLayouts(ctx context.Context, layouts iter.Seq2[string, int]) error {
	_, err := s.validateLayouts(ctx, layouts)
	return err
}

func (s Server) validateLayouts(ctx context.Context, layouts iter.Seq2[string, int]) (Version, error) {
	if err := ctx.Err(); err != nil {
		return Version{}, err
	}
	var pending []string
	if layouts != nil {
		for value, panes := range layouts {
			if err := ctx.Err(); err != nil {
				return Version{}, err
			}
			if err := (SelectLayoutRequest{Layout: value}).Validate(); err != nil {
				return Version{}, err
			}
			if value == "" {
				continue
			}
			if panes < 1 {
				return Version{}, invalidServerCommandRequest("select-layout", "Panes", "",
					"must be positive")
			}
			if cells, custom := layout.Cells(value); custom && cells < panes {
				return Version{}, invalidServerCommandRequest("select-layout", "Layout", value,
					"contains fewer cells than the required panes")
			}
			if layoutNeedsVersion(value) {
				pending = append(pending, value)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Version{}, err
	}
	if len(pending) == 0 {
		return Version{}, nil
	}
	version, err := s.layoutVersion(ctx)
	if err != nil {
		return Version{}, err
	}
	for _, value := range pending {
		if err := validateLayoutVersion(value, version); err != nil {
			return Version{}, err
		}
	}
	return version, nil
}

func (s Server) layoutVersion(ctx context.Context) (Version, error) {
	result, err := s.literalCmd(ctx, "display-message", "-p", "tmux #{version}")
	if err != nil {
		return Version{}, err
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		if s.connection == nil && s.daemon == nil && coldLayoutEndpoint(result) {
			return s.Version(ctx)
		}
		return Version{}, newCommandError("display-message", result)
	}
	if len(result.Stdout) == 1 {
		if token, ok := strings.CutPrefix(result.Stdout[0], "tmux "); ok {
			version, err := ParseVersion(token)
			if err == nil {
				return version, nil
			}
		}
	}
	return Version{}, newVersionQueryError(result, "layout version query returned an invalid version")
}

func coldLayoutEndpoint(result CommandResult) bool {
	if result.ExitCode != 1 || len(result.Stderr) != 1 {
		return false
	}
	reason := result.Stderr[0]
	return strings.HasPrefix(reason, "no server running on ") ||
		(strings.HasPrefix(reason, "error connecting to ") &&
			strings.HasSuffix(reason, " (No such file or directory)"))
}
