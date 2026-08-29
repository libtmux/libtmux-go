//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestControlClientCommandsNotificationsAndReconnectAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if client.ClientName() == "" || !client.Server().Equal(server) ||
		client.Session().ID() != sessions[0].ID() {
		t.Fatalf("control identity = (%q, %#v, %#v)", client.ClientName(), client.Server(), client.Session())
	}

	payload := "safe ; $HOME ' \" \\ \nsecond line"
	bufferName := "go-control-command-data"
	stored, err := client.Cmd(ctx, "set-buffer", "-b", bufferName, "--", payload)
	if err != nil {
		t.Fatalf("Cmd(set-buffer) error = %v", err)
	}
	if stored.Failed || len(stored.RawStdout) != 0 {
		t.Fatalf("Cmd(set-buffer) result = %#v", stored)
	}
	result, err := client.Cmd(ctx, "show-buffer", "-b", bufferName)
	if err != nil {
		t.Fatalf("Cmd(show-buffer) error = %v", err)
	}
	if result.Failed || len(result.RawStdout) == 0 || result.Number == 0 || result.Timestamp == 0 {
		t.Fatalf("Cmd(show-buffer) result = %#v", result)
	}
	if len(stored.Command) != 5 || stored.Command[4] != payload {
		t.Fatalf("Cmd(set-buffer) command = %#v", stored.Command)
	}
	storedBytes, err := server.ShowBufferBytes(ctx, &bufferName)
	if err != nil || !bytes.Equal(storedBytes, []byte(payload)) {
		t.Fatalf("ShowBufferBytes() = (%q, %v), want encoded payload", storedBytes, err)
	}
	t.Cleanup(func() { _ = server.DeleteBuffer(context.Background(), &bufferName) })

	failed, err := client.Cmd(ctx, "not-a-control-command")
	if err != nil {
		t.Fatalf("Cmd(invalid) error = %v", err)
	}
	if !failed.Failed || !strings.Contains(string(failed.RawStdout), "unknown command") {
		t.Fatalf("Cmd(invalid) result = %#v", failed)
	}
	afterFailure, err := client.Cmd(ctx, "display-message", "-p", "after failure")
	if err != nil || afterFailure.Failed || string(afterFailure.RawStdout) != "after failure\n" {
		t.Fatalf("Cmd(after failure) = (%#v, %v)", afterFailure, err)
	}
	const guardText = "%end 1 2 3"
	guardStored, err := client.Cmd(ctx, "set-buffer", "-b", bufferName, "--", guardText)
	if err != nil || guardStored.Failed {
		t.Fatalf("Cmd(set guard-shaped buffer) = (%#v, %v)", guardStored, err)
	}
	guardPayload, err := client.Cmd(ctx, "show-buffer", "-b", bufferName)
	if err != nil || guardPayload.Failed || string(guardPayload.RawStdout) != "%end 1 2 3\n" {
		t.Fatalf("Cmd(guard-shaped payload) = (%#v, %v)", guardPayload, err)
	}
	const renamed = "control-client-renamed"
	if _, err := sessions[0].Rename(ctx, renamed); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	for {
		notification, err := client.NextNotification(ctx)
		if err != nil {
			t.Fatalf("NextNotification() error = %v", err)
		}
		if notification.Kind() != tmux.ControlNotificationSessionRenamed {
			continue
		}
		arguments := notification.Arguments()
		if len(arguments) != 2 || arguments[0] != sessions[0].ID().String() || arguments[1] != renamed {
			t.Fatalf("session-renamed arguments = %#v", arguments)
		}
		break
	}

	reconnected, err := client.Reconnect(ctx)
	if err != nil {
		t.Fatalf("Reconnect() error = %v", err)
	}
	client = reconnected
	if client.ClientName() == "" {
		t.Fatal("reconnected ClientName() is empty")
	}
	result, err = client.Cmd(ctx, "display-message", "-p", "reconnected")
	if err != nil || result.Failed || string(result.RawStdout) != "reconnected\n" {
		t.Fatalf("reconnected Cmd() = (%#v, %v)", result, err)
	}

	canceledCtx, cancelClose := context.WithCancel(context.Background())
	cancelClose()
	if err := client.CloseContext(canceledCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseContext(canceled) error = %v, want context canceled", err)
	}
	if _, err := client.Cmd(ctx, "display-message", "-p", "closed"); !errors.Is(err, tmux.ErrControlClosed) {
		t.Fatalf("Cmd(after canceled close) error = %v, want ErrControlClosed", err)
	}
	if err := client.CloseContext(ctx); err != nil {
		t.Fatalf("CloseContext(resume wait) error = %v", err)
	}
}

//libtmux:real-tmux
func TestControlClientSurvivesItsStartupSession(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	requireNoDetachOnDestroy(t, server)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	startup := sessions[0]
	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "survivor"}); err != nil {
		t.Fatal(err)
	}
	client, err := server.OpenControl(ctx, startup)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := startup.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := client.Cmd(ctx, "display-message", "-p", "still-connected")
	if err != nil || result.Failed || string(result.RawStdout) != "still-connected\n" {
		t.Fatalf("Cmd() after startup session destruction = (%#v, %v)", result, err)
	}
	if client.Session().ID() != startup.ID() {
		t.Fatalf("client session identity changed from %s to %s",
			startup.ID(), client.Session().ID())
	}
}

//libtmux:real-tmux
func TestControlClientReplyFenceAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	emptyAliasFloor, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatalf("ParseVersion(3.3) error = %v", err)
	}
	type commandAlias struct {
		index int
		value string
	}
	aliases := []commandAlias{
		{80, "go-two=display-message -p one ; display-message -p two"},
		{81, `go-fence-a=\400`},
	}
	if version.AtLeast(emptyAliasFloor) {
		aliases = append(aliases, commandAlias{82, "go-empty="})
	}
	for _, alias := range aliases {
		result, err := server.Cmd(ctx, "set-option", "-s", fmt.Sprintf("command-alias[%d]", alias.index), alias.value)
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("set command alias %d = (%#v, %v)", alias.index, result, err)
		}
	}

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	results, err := client.Call(ctx, "go-two")
	if err != nil || len(results) != 2 || string(results[0].RawStdout) != "one\n" ||
		string(results[1].RawStdout) != "two\n" {
		t.Fatalf("Call(go-two) = (%#v, %v), want two frames", results, err)
	}

	results, err = client.Call(ctx, "go-fence-a")
	if err != nil || len(results) != 1 || !results[0].Failed {
		t.Fatalf("Call(go-fence-a) = (%#v, %v), want one failed frame", results, err)
	}
	results, err = client.Call(ctx, "display-message", "-p", "after overlap")
	if err != nil || len(results) != 1 || string(results[0].RawStdout) != "after overlap\n" {
		t.Fatalf("Call(after overlap) = (%#v, %v), want aligned reply", results, err)
	}
	if version.AtLeast(emptyAliasFloor) {
		results, err = client.Call(ctx, "go-empty")
		if err != nil || len(results) != 0 {
			t.Fatalf("Call(go-empty) = (%#v, %v), want no frames", results, err)
		}
	}
}

//libtmux:real-tmux
func TestControlClientCanceledCallDrainsBeforeReuseAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const (
		enteredOption = "@go-control-entered"
		releaseToken  = "go-control-release"
	)
	alias := "go-block=set-option -g " + enteredOption + " yes ; wait-for " + releaseToken
	setAlias, err := server.Cmd(ctx, "set-option", "-s", "command-alias[83]", alias)
	if err != nil || setAlias.ExitCode != 0 {
		t.Fatalf("set blocking command alias = (%#v, %v)", setAlias, err)
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), time.Second)
		defer releaseCancel()
		_, _ = server.Cmd(releaseCtx, "wait-for", "-S", releaseToken)
	}()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	callCtx, cancelCall := context.WithCancel(ctx)
	blocked := make(chan error, 1)
	go func() {
		_, err := client.Call(callCtx, "go-block")
		blocked <- err
	}()

	for {
		entered, err := server.Cmd(ctx, "show-options", "-gqv", enteredOption)
		if err != nil {
			t.Fatalf("observe blocking alias: %v", err)
		}
		if entered.ExitCode == 0 && slices.Equal(entered.Stdout, []string{"yes"}) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("blocking alias did not start: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancelCall()
	select {
	case err := <-blocked:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, tmux.ErrOutcomeUnknown) {
			t.Fatalf("Call(go-block) error = %v, want canceled unknown outcome", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled Call(go-block) did not return")
	}

	nextStarted := make(chan struct{})
	next := make(chan struct {
		results []tmux.ControlCommandResult
		err     error
	}, 1)
	go func() {
		close(nextStarted)
		results, err := client.Call(ctx, "display-message", "-p", "aligned")
		next <- struct {
			results []tmux.ControlCommandResult
			err     error
		}{results: results, err: err}
	}()
	<-nextStarted
	select {
	case result := <-next:
		t.Fatalf("next Call completed before canceled command drained: (%#v, %v)",
			result.results, result.err)
	case <-time.After(30 * time.Millisecond):
	}

	released, err := server.Cmd(ctx, "wait-for", "-S", releaseToken)
	if err != nil || released.ExitCode != 0 {
		t.Fatalf("release blocked control command = (%#v, %v)", released, err)
	}
	select {
	case result := <-next:
		if result.err != nil || len(result.results) != 1 ||
			string(result.results[0].RawStdout) != "aligned\n" {
			t.Fatalf("next Call() = (%#v, %v), want aligned reply", result.results, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("next Call did not finish after canceled command drained")
	}
}

//libtmux:real-tmux
func TestControlClientPreservesCommandArgumentBytesAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	allNonNULBytes := make([]byte, 255)
	for index := range allNonNULBytes {
		allNonNULBytes[index] = byte(index + 1)
	}
	for index, payload := range []string{
		"tab\tvalue",
		"carriage\rreturn",
		"valid café",
		"invalid\xffutf8",
		string(allNonNULBytes),
	} {
		bufferName := fmt.Sprintf("go-control-bytes-%d", index)
		result, err := client.Cmd(ctx, "set-buffer", "-b", bufferName, "--", payload)
		if err != nil || result.Failed {
			t.Fatalf("Cmd(set-buffer, %q) = (%#v, %v)", []byte(payload), result, err)
		}
		stored, err := server.ShowBufferBytes(ctx, &bufferName)
		if err != nil || !bytes.Equal(stored, []byte(payload)) {
			t.Fatalf("ShowBufferBytes(%q) = (%q, %v)", []byte(payload), stored, err)
		}
		if err := server.DeleteBuffer(ctx, &bufferName); err != nil {
			t.Fatalf("DeleteBuffer(%q) error = %v", bufferName, err)
		}
	}
}

//libtmux:real-tmux
func TestControlClientPreservesExitNotificationAfterWait(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	result, err := server.Cmd(ctx, "detach-client", "-t", client.ClientName().String())
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Cmd(detach-client) = (%#v, %v)", result, err)
	}
	if err := client.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	var foundExit bool
	for {
		notification, err := client.NextNotification(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("NextNotification() error = %v", err)
		}
		if notification.Kind() == tmux.ControlNotificationExit {
			foundExit = true
		}
	}
	if !foundExit {
		t.Fatalf("notifications after Wait() did not include %%exit")
	}
}

//libtmux:real-tmux
func TestControlClientDemultiplexesPaneOutputAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	const marker = "go-control-pane-output"
	command := "printf '" + marker + "'"
	if err := panes[0].SendKeys(ctx, tmux.SendKeysRequest{
		Command: &command,
		Literal: true,
	}); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	var output []byte
	for notification, err := range client.Notifications(ctx) {
		if err != nil {
			t.Fatalf("Notifications() error = %v", err)
		}
		paneID, data, ok := notification.Output()
		if !ok {
			continue
		}
		if paneID != panes[0].ID() {
			t.Fatalf("Output() pane = %q, want %q", paneID, panes[0].ID())
		}
		output = append(output, data...)
		if bytes.Contains(output, []byte(marker)) {
			break
		}
	}
	if !bytes.Contains(output, []byte(marker)) {
		t.Fatalf("pane output = %q, want it to contain %q", output, marker)
	}
}
