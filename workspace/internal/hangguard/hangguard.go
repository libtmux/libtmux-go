// Package hangguard holds the one bound that tests put on an event that must
// happen, such as a server starting or a process exiting.
package hangguard

import "time"

// Wait is a ceiling, not a delay: every wait it bounds returns as soon as the
// event happens, so the bound costs time only when a test hangs. It clears
// the slowest start of a tmux server, control client or shell on a loaded
// macOS runner, where a process start takes several times as long as on Linux.
// Deadline-enforcement tests and waits for something not to happen keep their
// own short bounds.
const Wait = 30 * time.Second
