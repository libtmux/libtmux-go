package tmux

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// scratchBufferGrace bounds a scratch buffer's cleanup once its caller's
// context is done.
const scratchBufferGrace = 2 * time.Second

// scratchBuffer is a tmux buffer this package stages data through and never
// hands to a caller. The helper owns its name and its cleanup so no call site
// has to remember either: tmux's most recent buffer is what an interactive
// paste reaches, so a pane's screen left there would change what the user's
// own prefix-] does.
type scratchBuffer struct {
	name   string
	server Server
}

// newScratchBuffer names a scratch buffer on server. prefix says which feature
// owns it and unique must differ between calls that can overlap.
func newScratchBuffer(server Server, prefix, unique string) scratchBuffer {
	return scratchBuffer{
		name:   fmt.Sprintf("libtmux-go-%s-%s", prefix, unique),
		server: server,
	}
}

// delete removes the buffer on a context the caller cannot cancel, bounded by
// scratchBufferGrace, because a caller whose context has ended is exactly when
// the buffer is still there. A buffer tmux reports absent is already clean, so
// that answer is not an error, nor is a tmux too old to stage or delete a named
// buffer, which never staged one; transport failures are returned for the caller
// to join into its own.
func (b scratchBuffer) delete(ctx context.Context) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), scratchBufferGrace)
	defer cancel()
	name := b.name
	err := b.server.DeleteBuffer(cleanup, &name)
	if _, ok := errors.AsType[*CommandError](err); ok {
		return nil
	}
	if _, ok := errors.AsType[*VersionTooLowError](err); ok {
		// The gate that refuses this delete refused the command that would have
		// staged the buffer, so there is nothing to clean and nothing to report.
		return nil
	}
	if err != nil {
		return fmt.Errorf("clean scratch buffer: %w", err)
	}
	return nil
}
