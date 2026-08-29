package tmux

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestOpenControlRejectsInvalidSessionBeforeStartingProcess(t *testing.T) {
	t.Parallel()

	_, err := (Server{}).OpenControl(context.Background(), Session{})
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("OpenControl() error = %v, want ErrInvalidServerCommandRequest", err)
	}

	other := NewServer(ServerOptions{SocketName: "other"})
	session := Session{server: other, sessionID: "$1"}
	_, err = NewServer(ServerOptions{SocketName: "one"}).OpenControl(
		context.Background(),
		session,
	)
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("OpenControl(other server) error = %v, want request error", err)
	}
}

func TestControlClientCollectsAliasFramesThroughReplyFence(t *testing.T) {
	t.Parallel()

	client, reader := newRequestLoopTestClient(t)
	response := make(chan controlResponse, 1)
	go func() {
		results, err := client.Call(context.Background(), "two-commands")
		response <- controlResponse{results: results, err: err}
	}()

	if got := readRequestLoopLine(t, reader); got != "'two-commands'" {
		t.Fatalf("command line = %q, want %q", got, "'two-commands'")
	}
	readRequestLoopFence(t, reader)
	client.frames <- controlFrame{flags: 0, rawStdout: []byte("queued hook\n")}
	client.frames <- controlFrame{flags: 1, rawStdout: []byte("one\n")}
	client.frames <- controlFrame{flags: 1, rawStdout: []byte("two\n")}
	client.frames <- replyFenceFrame(client.replyFence.first)
	select {
	case got := <-response:
		t.Fatalf("Call() stopped at fence A: (%#v, %v)", got.results, got.err)
	case <-time.After(30 * time.Millisecond):
	}
	client.frames <- replyFenceFrame(client.replyFence.second)

	select {
	case got := <-response:
		if got.err != nil || len(got.results) != 2 ||
			string(got.results[0].RawStdout) != "one\n" ||
			string(got.results[1].RawStdout) != "two\n" {
			t.Fatalf("Call() = (%#v, %v), want two frames", got.results, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call() did not reach its reply fence")
	}
}

func TestControlClientCollectsNoFramesBeforeReplyFence(t *testing.T) {
	t.Parallel()

	client, reader := newRequestLoopTestClient(t)
	response := make(chan controlResponse, 1)
	go func() {
		results, err := client.Call(context.Background(), "empty-alias")
		response <- controlResponse{results: results, err: err}
	}()

	_ = readRequestLoopLine(t, reader)
	readRequestLoopFence(t, reader)
	completeControlRequest(client)

	select {
	case got := <-response:
		if got.err != nil || len(got.results) != 0 {
			t.Fatalf("Call() = (%#v, %v), want no frames", got.results, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call() did not reach its empty reply fence")
	}
}

func TestControlClientCmdRejectsVariableReplyCount(t *testing.T) {
	t.Parallel()

	for name, frames := range map[string][]controlFrame{
		"none": nil,
		"two": {
			{rawStdout: []byte("one\n")},
			{rawStdout: []byte("two\n")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client, reader := newRequestLoopTestClient(t)
			result := make(chan error, 1)
			go func() {
				_, err := client.Cmd(context.Background(), "variable-alias")
				result <- err
			}()
			_ = readRequestLoopLine(t, reader)
			readRequestLoopFence(t, reader)
			completeControlRequest(client, frames...)

			select {
			case err := <-result:
				if !errors.Is(err, ErrControlReplyCount) {
					t.Fatalf("Cmd() error = %v, want ErrControlReplyCount", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Cmd() did not reject the variable reply count")
			}
		})
	}
}

func TestControlClientPreservesAResultThatMatchesFenceA(t *testing.T) {
	t.Parallel()

	client, reader := newRequestLoopTestClient(t)
	response := make(chan controlResponse, 1)
	go func() {
		results, err := client.Call(context.Background(), "malformed-alias")
		response <- controlResponse{results: results, err: err}
	}()
	_ = readRequestLoopLine(t, reader)
	readRequestLoopFence(t, reader)

	client.frames <- replyFenceFrame(client.replyFence.first)
	client.frames <- replyFenceFrame(client.replyFence.first)
	select {
	case got := <-response:
		t.Fatalf("Call() stopped on overlapping A,A: (%#v, %v)", got.results, got.err)
	case <-time.After(30 * time.Millisecond):
	}
	client.frames <- replyFenceFrame(client.replyFence.second)

	select {
	case got := <-response:
		if got.err != nil || len(got.results) != 1 || !got.results[0].Failed ||
			string(got.results[0].RawStdout) != client.replyFence.first.rawStdout {
			t.Fatalf("Call() = (%#v, %v), want one request-owned A", got.results, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call() did not finish on overlapping A,A,B")
	}
}

func TestControlClientCloseContextStartsWithCanceledContext(t *testing.T) {
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestServerCommandHelperProcess$",
		"--",
		"block",
	)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	queue := newControlNotificationQueue(defaultControlNotificationLimit)
	client := &ControlClient{
		command:       command,
		stdin:         &controlSignalWriteCloser{wrote: make(chan struct{}, 1)},
		stdout:        io.NopCloser(strings.NewReader("")),
		stderr:        &controlLockedBuffer{},
		notifications: queue,
		frames:        make(chan controlFrame),
		requests:      make(chan *controlRequest),
		stopRequests:  make(chan struct{}),
		requestDone:   make(chan struct{}),
		closing:       make(chan struct{}),
		done:          make(chan struct{}),
		closeDone:     make(chan struct{}),
	}
	go client.waitProcess()
	go client.runRequests()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = queue.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.CloseContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseContext() error = %v, want context canceled", err)
	}
	select {
	case <-client.closeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled CloseContext did not start shutdown")
	}
}

func TestControlClientCancellationBeforeWriteIsNotIndeterminate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := &controlRequest{ctx: ctx}
	request.state.Store(uint32(controlRequestAccepted))
	client := &ControlClient{stdin: controlRejectWriteCloser{t: t}}

	response, keepRunning := client.executeControlRequest(request)
	if !keepRunning || !errors.Is(response.err, context.Canceled) ||
		errors.Is(response.err, ErrOutcomeUnknown) {
		t.Fatalf("executeControlRequest() = (%#v, %t), want plain cancellation",
			response, keepRunning)
	}
}

func TestControlClientReturnsProvenFramesWhenStreamEndsBeforeFence(t *testing.T) {
	t.Parallel()

	client, reader := newRequestLoopTestClient(t)
	response := make(chan controlResponse, 1)
	go func() {
		results, err := client.Call(context.Background(), "partial-alias")
		response <- controlResponse{results: results, err: err}
	}()

	_ = readRequestLoopLine(t, reader)
	readRequestLoopFence(t, reader)
	client.frames <- controlFrame{flags: 1, rawStdout: []byte("proven\n")}
	close(client.frames)

	select {
	case got := <-response:
		if len(got.results) != 1 || string(got.results[0].RawStdout) != "proven\n" ||
			!errors.Is(got.err, ErrOutcomeUnknown) || !errors.Is(got.err, ErrControlClosed) {
			t.Fatalf("Call() = (%#v, %v), want proven frame and unknown outcome",
				got.results, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call() did not report the interrupted reply")
	}
}

func TestControlClientPublishesFinishedStateWithReplyBoundary(t *testing.T) {
	t.Parallel()

	client := &ControlClient{
		stdin:   &controlSignalWriteCloser{wrote: make(chan struct{}, 1)},
		frames:  make(chan controlFrame, 2),
		closing: make(chan struct{}),
		replyFence: controlReplyFence{
			first:  controlFrameFingerprint{flags: 1, rawStdout: "first fence\n"},
			second: controlFrameFingerprint{flags: 1, rawStdout: "second fence\n"},
		},
	}
	client.frames <- replyFenceFrame(client.replyFence.first)
	client.frames <- replyFenceFrame(client.replyFence.second)
	request := &controlRequest{ctx: context.Background(), line: "'display-message'"}
	request.state.Store(uint32(controlRequestAccepted))

	response, keepRunning := client.executeControlRequest(request)
	if response.err != nil || !keepRunning {
		t.Fatalf("executeControlRequest() = (%#v, %t)", response, keepRunning)
	}
	if got := controlRequestState(request.state.Load()); got != controlRequestFinished {
		t.Fatalf("request state = %v, want finished before response publication", got)
	}
}

func TestControlClientDrainsCanceledWrittenCommandBeforeNextWrite(t *testing.T) {
	t.Parallel()

	client, reader := newRequestLoopTestClient(t)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	go func() {
		_, err := client.Cmd(firstCtx, "display-message", "first")
		firstResult <- err
	}()
	firstLine := readRequestLoopLine(t, reader)
	if want, _ := encodeControlCommand([]string{"display-message", "first"}, false); firstLine != want {
		t.Fatalf("first command line = %q, want %q", firstLine, want)
	}
	readRequestLoopFence(t, reader)
	cancelFirst()
	select {
	case err := <-firstResult:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("first Cmd() error = %v, want canceled unknown outcome", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Cmd() did not return after cancellation")
	}

	secondCtx, cancelSecond := context.WithTimeout(context.Background(), time.Second)
	defer cancelSecond()
	secondResult := make(chan controlResponse, 1)
	go func() {
		result, err := client.Cmd(secondCtx, "display-message", "second")
		secondResult <- controlResponse{results: []ControlCommandResult{result}, err: err}
	}()
	secondLine := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		secondLine <- line
	}()
	select {
	case line := <-secondLine:
		t.Fatalf("second command written before first frame drained: %q", line)
	case <-time.After(30 * time.Millisecond):
	}

	completeControlRequest(client, controlFrame{number: 1, rawStdout: []byte("first\n")})
	select {
	case line := <-secondLine:
		line = line[:len(line)-1]
		want, _ := encodeControlCommand([]string{"display-message", "second"}, false)
		if line != want {
			t.Fatalf("second command line = %q, want %q", line, want)
		}
		readRequestLoopFence(t, reader)
	case <-time.After(time.Second):
		t.Fatal("second command was not written after first frame")
	}
	completeControlRequest(client, controlFrame{number: 2, rawStdout: []byte("second\n")})
	select {
	case response := <-secondResult:
		if response.err != nil || response.results[0].Number != 2 ||
			!slices.Equal(response.results[0].Command, []string{"display-message", "second"}) ||
			string(response.results[0].RawStdout) != "second\n" {
			t.Fatalf("second Cmd() = (%#v, %v)", response.results, response.err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Cmd() did not receive its frame")
	}
}

func TestControlClientSerializesConcurrentCommands(t *testing.T) {
	t.Parallel()

	client, reader := newRequestLoopTestClient(t)
	commands := [][]string{
		{"display-message", "one"},
		{"display-message", "two"},
		{"display-message", "three"},
	}
	type result struct {
		value ControlCommandResult
		err   error
	}
	results := make(chan result, len(commands))
	for _, command := range commands {
		command := slices.Clone(command)
		go func() {
			value, err := client.Cmd(context.Background(), command...)
			results <- result{value: value, err: err}
		}()
	}

	expected := make(map[string][]string, len(commands))
	for _, command := range commands {
		line, err := encodeControlCommand(command, false)
		if err != nil {
			t.Fatal(err)
		}
		expected[line] = command
	}
	for number := uint64(1); number <= uint64(len(commands)); number++ {
		line := readRequestLoopLine(t, reader)
		readRequestLoopFence(t, reader)
		command, ok := expected[line]
		if !ok {
			t.Fatalf("unexpected encoded command %q", line)
		}
		delete(expected, line)
		completeControlRequest(client, controlFrame{
			number:    number,
			rawStdout: []byte(command[1] + "\n"),
		})
	}
	for range commands {
		response := <-results
		if response.err != nil || len(response.value.Command) != 2 ||
			string(response.value.RawStdout) != response.value.Command[1]+"\n" {
			t.Fatalf("concurrent Cmd() = (%#v, %v)", response.value, response.err)
		}
	}
}

func TestControlClientDoesNotSubmitCommandsAfterClosing(t *testing.T) {
	t.Parallel()

	closing := make(chan struct{})
	close(closing)
	requests := make(chan *controlRequest, 256)
	client := &ControlClient{
		requests:     requests,
		stopRequests: make(chan struct{}),
		requestDone:  make(chan struct{}),
		closing:      closing,
	}
	client.closeRequested.Store(true)
	close(client.stopRequests)
	for range cap(requests) {
		if _, err := client.Cmd(context.Background(), "display-message", "late"); !errors.Is(err, ErrControlClosed) {
			t.Fatalf("Cmd() error = %v, want ErrControlClosed", err)
		}
	}
	if len(requests) != 0 {
		t.Fatalf("Cmd() submitted %d requests after closing", len(requests))
	}
}

func TestControlClientRequestStopWaitsForAcceptedFrame(t *testing.T) {
	t.Parallel()

	client, reader := newRequestLoopTestClient(t)
	result := make(chan controlResponse, 1)
	go func() {
		value, err := client.Cmd(context.Background(), "display-message", "accepted")
		result <- controlResponse{results: []ControlCommandResult{value}, err: err}
	}()
	_ = readRequestLoopLine(t, reader)
	readRequestLoopFence(t, reader)

	client.closeRequested.Store(true)
	close(client.stopRequests)
	select {
	case <-client.requestDone:
		t.Fatal("request stop passed an accepted command before its frame")
	case <-time.After(30 * time.Millisecond):
	}

	completeControlRequest(client, controlFrame{number: 1, rawStdout: []byte("accepted\n")})
	select {
	case response := <-result:
		if response.err != nil || string(response.results[0].RawStdout) != "accepted\n" {
			t.Fatalf("accepted Cmd() = (%#v, %v)", response.results, response.err)
		}
	case <-time.After(time.Second):
		t.Fatal("accepted Cmd() did not receive its frame")
	}
	select {
	case <-client.requestDone:
	case <-time.After(time.Second):
		t.Fatal("request stop did not finish after the accepted frame")
	}
}

func TestControlClientRequestStopReleasesQueuedCommands(t *testing.T) {
	t.Parallel()

	client, reader := newRequestLoopTestClient(t)
	const commands = 3
	results := make(chan error, commands)
	go func() {
		_, err := client.Cmd(context.Background(), "display-message", "active")
		results <- err
	}()
	_ = readRequestLoopLine(t, reader)
	readRequestLoopFence(t, reader)
	for _, value := range []string{"queued-one", "queued-two"} {
		go func() {
			_, err := client.Cmd(context.Background(), "display-message", value)
			results <- err
		}()
	}

	client.closeRequested.Store(true)
	close(client.stopRequests)
	close(client.closing)
	for range commands {
		select {
		case err := <-results:
			if !errors.Is(err, ErrControlClosed) {
				t.Fatalf("Cmd() error = %v, want ErrControlClosed", err)
			}
		case <-time.After(time.Second):
			t.Fatal("command did not return after request stop")
		}
	}
}

func TestControlClientCloseEscalatesWhenFrameNeverArrives(t *testing.T) {
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestServerCommandHelperProcess$",
		"--",
		"block",
	)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	queue := newControlNotificationQueue(defaultControlNotificationLimit)
	writer := &controlSignalWriteCloser{wrote: make(chan struct{}, 1)}
	client := &ControlClient{
		command:       command,
		stdin:         writer,
		stdout:        io.NopCloser(strings.NewReader("")),
		stderr:        &controlLockedBuffer{},
		notifications: queue,
		frames:        make(chan controlFrame),
		requests:      make(chan *controlRequest),
		stopRequests:  make(chan struct{}),
		requestDone:   make(chan struct{}),
		closing:       make(chan struct{}),
		done:          make(chan struct{}),
		closeDone:     make(chan struct{}),
	}
	go client.waitProcess()
	go client.runRequests()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		select {
		case <-client.done:
		case <-time.After(time.Second):
			t.Error("control helper process did not exit")
		}
		close(client.frames)
		_ = queue.Close()
	})

	commandResult := make(chan error, 1)
	go func() {
		_, err := client.Cmd(context.Background(), "display-message", "blocked")
		commandResult <- err
	}()
	select {
	case <-writer.wrote:
	case <-time.After(time.Second):
		t.Fatal("control command was not written")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.CloseContext(ctx); err != nil {
		t.Fatalf("CloseContext() error = %v", err)
	}
	select {
	case err := <-commandResult:
		if !errors.Is(err, ErrControlClosed) {
			t.Fatalf("blocked Cmd() error = %v, want ErrControlClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Cmd() did not return after close")
	}
}

func TestControlClientNotificationReaderDrainsBeforeProtocolError(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(defaultControlNotificationLimit)
	t.Cleanup(func() { _ = queue.Close() })
	closing := make(chan struct{})
	close(closing)
	client := &ControlClient{
		stdout: io.NopCloser(strings.NewReader(
			"%session-renamed $1 renamed\n%end malformed\n",
		)),
		notifications: queue,
		frames:        make(chan controlFrame, 1),
		closing:       closing,
		readDone:      make(chan struct{}),
	}
	client.readStream()

	notification, err := client.NextNotification(context.Background())
	if err != nil || notification.Kind() != ControlNotificationSessionRenamed {
		t.Fatalf("first NextNotification() = (%#v, %v)", notification, err)
	}
	if _, err := client.NextNotification(context.Background()); !errors.Is(err, ErrControlProtocol) {
		t.Fatalf("second NextNotification() error = %v, want protocol error", err)
	}
}

func TestNotificationsReportAnUnreadableRecordAndKeepGoing(t *testing.T) {
	t.Parallel()

	client := newQueuedNotificationClient(t,
		"%session-renamed $1 first\n"+
			"%not-a-notification-this-package-knows arguments\n"+
			"%session-renamed $1 second\n",
	)

	var names []string
	var reported []error
	for notification, err := range client.Notifications(context.Background()) {
		if err != nil {
			reported = append(reported, err)
			continue
		}
		names = append(names, notification.Arguments()[1])
	}

	if want := []string{"first", "second"}; !slices.Equal(names, want) {
		t.Errorf("notifications read = %#v, want %#v", names, want)
	}
	if len(reported) != 1 || !errors.Is(reported[0], ErrUnknownControlNotification) {
		t.Errorf("errors reported = %#v, want one unknown-notification error", reported)
	}
}

func TestNotificationsStopAtAnErrorThatEndedTheStream(t *testing.T) {
	t.Parallel()

	client := newQueuedNotificationClient(t,
		"%session-renamed $1 first\n%end malformed\n",
	)

	var read, failed int
	for _, err := range client.Notifications(context.Background()) {
		if err != nil {
			failed++
			if !errors.Is(err, ErrControlProtocol) {
				t.Errorf("error yielded = %v, want protocol error", err)
			}
			continue
		}
		read++
	}

	if read != 1 || failed != 1 {
		t.Errorf("loop read %d notifications and %d errors, want 1 and 1", read, failed)
	}
}

func TestNotificationsLeaveTheRestOfTheQueueForTheNextReader(t *testing.T) {
	t.Parallel()

	client := newQueuedNotificationClient(t,
		"%session-renamed $1 first\n%session-renamed $1 second\n",
	)

	for notification, err := range client.Notifications(context.Background()) {
		if err != nil || notification.Arguments()[1] != "first" {
			t.Fatalf("first iteration = (%#v, %v)", notification, err)
		}
		break
	}

	notification, err := client.NextNotification(context.Background())
	if err != nil || notification.Arguments()[1] != "second" {
		t.Fatalf("NextNotification() after break = (%#v, %v), want the second record",
			notification, err)
	}
}

// newQueuedNotificationClient returns a control client whose notification
// queue holds what stream carries, without a tmux process: the stream is read
// to its end before the client is returned.
func newQueuedNotificationClient(t *testing.T, stream string) *ControlClient {
	t.Helper()

	queue := newControlNotificationQueue(defaultControlNotificationLimit)
	t.Cleanup(func() { _ = queue.Close() })
	closing := make(chan struct{})
	close(closing)
	client := &ControlClient{
		stdout:        io.NopCloser(strings.NewReader(stream)),
		notifications: queue,
		frames:        make(chan controlFrame, 1),
		closing:       closing,
		readDone:      make(chan struct{}),
	}
	client.readStream()
	return client
}

func newRequestLoopTestClient(t *testing.T) (*ControlClient, *bufio.Reader) {
	t.Helper()
	reader, writer := io.Pipe()
	client := &ControlClient{
		stdin:        writer,
		frames:       make(chan controlFrame, 1),
		requests:     make(chan *controlRequest),
		stopRequests: make(chan struct{}),
		requestDone:  make(chan struct{}),
		closing:      make(chan struct{}),
		replyFence: controlReplyFence{
			first:  controlFrameFingerprint{flags: 1, rawStdout: "bad octal escape\n"},
			second: controlFrameFingerprint{flags: 1, rawStdout: "bad unicode escape\n"},
		},
	}
	go client.runRequests()
	t.Cleanup(func() {
		if client.closeRequested.CompareAndSwap(false, true) {
			close(client.stopRequests)
		}
		_ = writer.Close()
		_ = reader.Close()
		select {
		case <-client.requestDone:
		case <-time.After(time.Second):
			t.Error("request loop did not stop")
		}
	})
	return client, bufio.NewReader(reader)
}

func readRequestLoopLine(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read control command: %v", err)
	}
	return line[:len(line)-1]
}

func readRequestLoopFence(t *testing.T, reader *bufio.Reader) {
	t.Helper()
	if got := readRequestLoopLine(t, reader); got != `\400` {
		t.Fatalf("first fence line = %q, want %q", got, `\400`)
	}
	if got := readRequestLoopLine(t, reader); got != `\uZZZZ` {
		t.Fatalf("second fence line = %q, want %q", got, `\uZZZZ`)
	}
}

func replyFenceFrame(fingerprint controlFrameFingerprint) controlFrame {
	return controlFrame{
		flags:     fingerprint.flags,
		rawStdout: []byte(fingerprint.rawStdout),
		failed:    true,
	}
}

func completeControlRequest(client *ControlClient, frames ...controlFrame) {
	for _, frame := range frames {
		frame.flags = 1
		client.frames <- frame
	}
	client.frames <- replyFenceFrame(client.replyFence.first)
	client.frames <- replyFenceFrame(client.replyFence.second)
}

type controlSignalWriteCloser struct {
	wrote chan struct{}
}

func (w *controlSignalWriteCloser) Write(data []byte) (int, error) {
	select {
	case w.wrote <- struct{}{}:
	default:
	}
	return len(data), nil
}

func (w *controlSignalWriteCloser) Close() error { return nil }

type controlRejectWriteCloser struct {
	t *testing.T
}

func (w controlRejectWriteCloser) Write([]byte) (int, error) {
	w.t.Helper()
	w.t.Fatal("control command was written after pre-write cancellation")
	return 0, nil
}

func (controlRejectWriteCloser) Close() error { return nil }
