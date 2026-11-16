package tmux

import (
	"context"
	"io"
	"slices"
)

// SendKeys sends keyboard input to the command's pane. For a StartProgram
// owner, generation admission and delivery share one daemon command group;
// replacement or lost observation returns ErrProgramReplaced. KeyName is
// rejected because it routes through a client rather than the owned pane.
// Other request semantics and delivery ambiguity are those of [Pane.SendKeys].
func (r *Running) SendKeys(ctx context.Context, request SendKeysRequest) error {
	if r.lifetime == nil {
		return r.pane.SendKeys(ctx, request)
	}
	request = captureSendKeysRequest(request)
	if err := validateSendKeysRequest(r.pane, request); err != nil {
		return err
	}
	if request.KeyName {
		return invalidServerCommandRequest("send-keys", "KeyName", "", "does not target the owned pane")
	}
	target, err := exactPaneTarget(r.pane)
	if err != nil {
		return err
	}
	command, err := sendKeysCommand(target, request, paneInputVersion34)
	if err != nil {
		return err
	}
	arguments, err := command.apply(r.session.server.policy())
	if err != nil {
		return err
	}
	if _, err := r.lifetime.operation(ctx, true, arguments...); err != nil {
		return err
	}
	if sendKeysNeedsEnter(request) {
		return r.Enter(ctx)
	}
	return nil
}

// SendKeySequence sends ordered keys without appending Enter. A StartProgram
// owner refuses delivery after replacement or loss of observation. Other
// request semantics and delivery ambiguity are those of [Pane.SendKeySequence].
func (r *Running) SendKeySequence(ctx context.Context, request SendKeySequenceRequest) error {
	if r.lifetime == nil {
		return r.pane.SendKeySequence(ctx, request)
	}
	request.Keys = slices.Clone(request.Keys)
	if err := validateSendKeySequenceRequest(r.pane, request); err != nil {
		return err
	}
	target, err := exactPaneTarget(r.pane)
	if err != nil {
		return err
	}
	arguments, err := sendKeySequenceArguments(target, request)
	if err != nil {
		return err
	}
	_, err = r.lifetime.operation(ctx, true, arguments...)
	return err
}

// Enter sends the Enter key to the command's pane. A StartProgram owner
// refuses delivery after replacement or loss of observation.
func (r *Running) Enter(ctx context.Context) error {
	if r.lifetime == nil {
		return r.pane.Enter(ctx)
	}
	target, err := exactPaneTarget(r.pane)
	if err != nil {
		return err
	}
	arguments, err := enterArguments(target)
	if err != nil {
		return err
	}
	_, err = r.lifetime.operation(ctx, true, arguments...)
	return err
}

// Writer returns the command's keyboard as an [io.Writer], with the text,
// newline and chunking semantics of [Pane.Writer]. Every chunk and Enter uses
// the owner's generation admission; a whole write is not atomic. An error
// cannot establish how many bytes of the failing chunk tmux delivered.
func (r *Running) Writer(ctx context.Context) io.Writer {
	return paneWriter{ctx: ctx, pane: r}
}
