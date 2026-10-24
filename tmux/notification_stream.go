package tmux

import (
	"context"
	"errors"
	"iter"
	"strconv"
	"strings"
	"time"
)

// NotificationStream owns one observation-only control client attached to a
// session. It yields tmux notifications in wire order and must be closed.
// Acquisition requires tmux 3.7 before any control client is started.
//
// A stream ends in one of three ways, and none can be resumed. A stream is one
// tmux client, so a new one is a new client: it starts at tmux's present, with
// no subscriptions armed, no pane held by PauseAfter, and none of what the old
// stream had queued or tmux sent in the gap. To recover, close the old stream,
// read whatever state matters from tmux directly, open another with
// [Server.OpenNotifications] or [Session.OpenNotifications], and Subscribe
// again. [ControlClient.Reconnect] is not offered here because it would have to
// choose between dropping the subscriptions silently and replaying commands,
// which the package never does.
//
//   - The caller closed it: reads return an error matching [ErrControlClosed]
//     and [os.ErrClosed].
//   - tmux ended it, because the server exited or the client fell too far
//     behind: reads return an error matching [ErrControlClosed] and
//     [ErrControlStreamLost], after the notifications queued before the end.
//   - Its backlog filled: reads return a [ControlNotificationOverflowError]
//     matching [ErrControlNotificationOverflow] after the queued prefix. The
//     process is still running and tmux keeps sending, but everything after
//     the overflow is dropped; close the stream.
type NotificationStream struct {
	client *ControlClient
}

// NotificationOptions configures which notifications a stream receives. The
// zero value retains notifications other than pane output.
type NotificationOptions struct {
	// IncludePaneOutput includes output and extended-output notifications.
	IncludePaneOutput bool
	// PauseAfter asks tmux to hold a pane's output once this stream is that
	// far behind on it, and to say so with a pause notification. Zero leaves
	// tmux's own behavior, which ends a stream that falls five minutes behind
	// rather than pausing it. tmux resolves this to whole seconds, and it
	// governs pane output, so it requires IncludePaneOutput.
	PauseAfter time.Duration
	// BacklogBytes bounds the notifications this stream holds while the caller
	// is not reading, counting each notification's text and a small fixed
	// overhead. Zero means 16 MiB. Once a notification would cross the bound,
	// reads return the queued prefix and then
	// ErrControlNotificationOverflow. A larger bound trades memory for
	// headroom on a chatty pane; a smaller one makes a slow reader fail sooner.
	// It may not be below 64 KiB.
	BacklogBytes int
}

// minNotificationBacklog is the smallest bound BacklogBytes accepts, well above
// the largest single notification tmux sends.
const minNotificationBacklog = 64 << 10

func (o NotificationOptions) backlog() int {
	if o.BacklogBytes == 0 {
		return defaultControlNotificationLimit
	}
	return o.BacklogBytes
}

func (o NotificationOptions) validate() error {
	if o.BacklogBytes != 0 && o.BacklogBytes < minNotificationBacklog {
		return invalidServerCommandRequest(
			"refresh-client", "BacklogBytes", strconv.Itoa(o.BacklogBytes),
			"is below the 65536 byte minimum; zero selects the default",
		)
	}
	if o.PauseAfter == 0 {
		return nil
	}
	if o.PauseAfter < time.Second {
		return invalidServerCommandRequest(
			"refresh-client", "PauseAfter", o.PauseAfter.String(),
			"is below the whole second tmux resolves this flag to",
		)
	}
	if !o.IncludePaneOutput {
		return invalidServerCommandRequest(
			"refresh-client", "PauseAfter", o.PauseAfter.String(),
			"holds pane output, which this stream does not receive without IncludePaneOutput",
		)
	}
	return nil
}

// OpenNotifications starts an owned observation-only stream attached to
// session. Session must belong to the same configured server selector. A
// connection-bound server returns [ErrConnectionRequiresProcess].
//
// The stream starts at tmux's present and holds nothing from before it opened.
// When it ends or overflows it cannot be resumed; open another, as described
// on [NotificationStream], and arm its subscriptions again.
func (s Server) OpenNotifications(
	ctx context.Context,
	session Session,
	options NotificationOptions,
) (*NotificationStream, error) {
	profile := controlNotificationsNoPaneOutput
	if options.IncludePaneOutput {
		profile = controlNotificationsFull
	}
	if err := options.validate(); err != nil {
		return nil, err
	}
	client, err := s.openControlWithBacklog(ctx, session, profile, options.backlog())
	if err != nil {
		return nil, err
	}
	stream := &NotificationStream{client: client}
	if options.PauseAfter > 0 {
		if err := stream.armPause(ctx, options.PauseAfter); err != nil {
			return nil, errors.Join(err, client.CloseContext(ctx))
		}
	}
	return stream, nil
}

// armPause arms tmux's per-pane hold for this stream's own client.
func (s *NotificationStream) armPause(ctx context.Context, after time.Duration) error {
	_, err := s.client.Cmd(ctx, "refresh-client", "-f",
		"pause-after="+strconv.Itoa(int(after/time.Second)))
	return err
}

// ContinuePane resumes pane output tmux held after this stream fell behind on
// it. tmux does not replay what it held, so read the pane to recover that span
// rather than waiting for it. Resuming a pane tmux never paused does nothing
// and reports nothing, because tmux answers neither.
func (s *NotificationStream) ContinuePane(ctx context.Context, pane PaneID) error {
	if s == nil || s.client == nil {
		return ErrControlClosed
	}
	// tmux discards a resume argument it cannot parse without reporting it, so
	// an unusable identifier would leave the pane held with nothing to show why.
	if !resumablePaneID(pane) {
		return invalidServerCommandRequest(
			"refresh-client", "Pane", pane.String(),
			"is not a pane identifier tmux will resume, such as %1",
		)
	}
	_, err := s.client.Cmd(ctx, "refresh-client", "-A", pane.String()+":continue")
	return err
}

// SubscriptionRequest names a format for tmux to evaluate and report on this
// stream whenever its value changes. Its zero value is invalid because Name and
// Format are required. At most one of Window and Pane scopes the evaluation;
// neither evaluates against the stream's attached session, which tmux
// always uses for a session-scoped subscription and which cannot be selected.
//
// tmux stops evaluating a pane-scoped subscription once that pane's process has
// exited, so a subscription that must observe pane death is scoped to the
// window and reads the pane from the change it reports.
type SubscriptionRequest struct {
	// Name identifies the subscription in each change and to Unsubscribe. It
	// may not contain a colon or a space.
	Name string
	// Format is the tmux format to evaluate, such as #{pane_current_command}.
	Format string
	// Window scopes evaluation to one window.
	Window WindowID
	// Pane scopes evaluation to one pane.
	Pane PaneID
}

func (r SubscriptionRequest) validate() error {
	if r.Name == "" {
		return invalidServerCommandRequest("refresh-client", "Name", "", "is required")
	}
	if strings.ContainsAny(r.Name, ": ") {
		return invalidServerCommandRequest(
			"refresh-client", "Name", r.Name,
			"may not contain a colon or a space, which tmux reads as the scope",
		)
	}
	if err := validateServerCommandArgument("refresh-client", "Name", r.Name, true); err != nil {
		return err
	}
	if r.Format == "" {
		return invalidServerCommandRequest("refresh-client", "Format", "", "is required")
	}
	if err := validateServerCommandArgument("refresh-client", "Format", r.Format, true); err != nil {
		return err
	}
	if r.Window != "" && r.Pane != "" {
		return invalidServerCommandRequest(
			"refresh-client", "Scope", "", "selects both Window and Pane",
		)
	}
	return nil
}

// scope renders the target segment of refresh-client -B's
// name:target:format grammar. Session scope has no target of its own - tmux
// always evaluates it against the control client's attached session - so it
// renders empty, like the unscoped default. tmux 3.8 tightened that grammar
// to require target be exactly empty for session scope; anything else that is
// not a pane or window form, including a literal session ID, is now a parse
// failure that silently drops the subscription (monitor_parse in monitor.c).
// Earlier tmux was permissive there, so this also matches every older,
// supported version.
func (r SubscriptionRequest) scope() string {
	switch {
	case r.Window != "":
		return r.Window.String()
	case r.Pane != "":
		return r.Pane.String()
	}
	return ""
}

// Subscribe asks tmux to evaluate request's format and report it on this
// stream as a subscription-changed notification, read with
// [ControlNotification.Subscription]. tmux reports the value at its next
// evaluation, about a second later, and then each time it changes. Arming a
// name already armed replaces its format.
func (s *NotificationStream) Subscribe(ctx context.Context, request SubscriptionRequest) error {
	if s == nil || s.client == nil {
		return ErrControlClosed
	}
	if err := request.validate(); err != nil {
		return err
	}
	_, err := s.client.Cmd(ctx, "refresh-client", "-B",
		request.Name+":"+request.scope()+":"+request.Format)
	return err
}

// Unsubscribe stops the subscription armed under name. Stopping a name that
// was never armed does nothing and reports nothing, because tmux answers
// neither.
func (s *NotificationStream) Unsubscribe(ctx context.Context, name string) error {
	if s == nil || s.client == nil {
		return ErrControlClosed
	}
	if name == "" || strings.ContainsAny(name, ": ") {
		return invalidServerCommandRequest(
			"refresh-client", "Name", name, "is not a subscription name",
		)
	}
	if err := validateServerCommandArgument("refresh-client", "Name", name, true); err != nil {
		return err
	}
	_, err := s.client.Cmd(ctx, "refresh-client", "-B", name)
	return err
}

// resumablePaneID reports whether tmux's resume parser accepts id, which needs
// a per cent sign and then digits.
func resumablePaneID(id PaneID) bool {
	digits, ok := strings.CutPrefix(string(id), "%")
	if !ok || digits == "" {
		return false
	}
	return !strings.ContainsFunc(digits, func(r rune) bool { return r < '0' || r > '9' })
}

// OpenNotifications starts an owned notification stream attached to the
// receiver. It does not change the receiver's transport or expose control
// commands. A connection-bound receiver returns [ErrConnectionRequiresProcess].
func (s Session) OpenNotifications(
	ctx context.Context,
	options NotificationOptions,
) (*NotificationStream, error) {
	return s.server.OpenNotifications(ctx, s, options)
}

// Next returns the next ordered control-mode notification. Concurrent calls are
// serialized: each notification goes to exactly one call, and a call waiting
// its turn returns when ctx ends.
//
// A stream that has ended reports why: see [NotificationStream] for the errors
// to match. A caller that wants to treat every end alike writes
// errors.Is(err, [ErrControlClosed]).
func (s *NotificationStream) Next(
	ctx context.Context,
) (ControlNotification, error) {
	if s == nil || s.client == nil {
		return ControlNotification{}, ErrControlClosed
	}
	return s.client.NextNotification(ctx)
}

// Notifications returns [NotificationStream.Next] as a range loop; reads from
// several iterators or direct calls are serialized. Malformed notifications yield
// their error and iteration continues; every other error ends the loop after
// being yielded.
func (s *NotificationStream) Notifications(
	ctx context.Context,
) iter.Seq2[ControlNotification, error] {
	return notificationSeq(ctx, s.Next)
}

// CloseContext starts idempotent stream shutdown and waits within ctx. The
// context bounds only the wait; a later call may resume waiting for shutdown.
func (s *NotificationStream) CloseContext(ctx context.Context) error {
	if s == nil || s.client == nil {
		return ErrControlClosed
	}
	return s.client.CloseContext(ctx)
}

// Close stops the notification stream. It is safe to call concurrently and
// more than once.
func (s *NotificationStream) Close() error {
	if s == nil || s.client == nil {
		return ErrControlClosed
	}
	return s.client.Close()
}
