//go:build !unix

package tmux

import "os"

// platformSignal has no signal numbers to match where tmux does not run
// natively, so a report keeps tmux's own spelling.
func platformSignal(string) (os.Signal, bool) { return nil, false }
