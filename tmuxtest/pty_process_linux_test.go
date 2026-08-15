//go:build linux

package tmuxtest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmuxtest"
)

func TestPTYProcessReturnsLiveAndFinalOutput(t *testing.T) {
	ready, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for helper readiness: %v", err)
	}
	t.Cleanup(func() { _ = ready.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	process := tmuxtest.StartPTYProcess(
		ctx,
		t,
		os.Args[0],
		[]string{"-test.run=^TestPTYProcessOutputHelper$"},
		append(
			os.Environ(),
			"LIBTMUX_PTY_OUTPUT_HELPER=1",
			"LIBTMUX_PTY_OUTPUT_READY="+ready.LocalAddr().String(),
		),
	)

	if err := ready.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set readiness deadline: %v", err)
	}
	var notification [1]byte
	_, helperAddress, err := ready.ReadFrom(notification[:])
	if err != nil {
		t.Fatalf("await helper readiness: %v", err)
	}

	liveDeadline := time.NewTimer(2 * time.Second)
	defer liveDeadline.Stop()
	for {
		liveOutput := make(chan []byte, 1)
		go func() { liveOutput <- process.Output() }()
		select {
		case output := <-liveOutput:
			if bytes.Contains(output, []byte("live\r\n")) {
				goto liveOutputReady
			}
		case <-time.After(250 * time.Millisecond):
			t.Fatal("Output blocked while helper remained alive")
		}
		select {
		case <-liveDeadline.C:
			t.Fatal("live output was not collected")
		case <-time.After(5 * time.Millisecond):
		}
	}

liveOutputReady:

	if _, err := ready.WriteTo([]byte{1}, helperAddress); err != nil {
		t.Fatalf("release helper: %v", err)
	}
	if err := process.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	finalOutput := process.Output()
	if !bytes.Contains(finalOutput, []byte("final\r\n")) {
		t.Fatalf("final output = %q, want final marker", finalOutput)
	}
	wantOutput := bytes.Clone(finalOutput)
	finalOutput[0] ^= 0xff
	if next := process.Output(); !bytes.Equal(next, wantOutput) {
		t.Fatalf("Output() after mutating prior result = %q, want %q", next, wantOutput)
	}
}

func TestPTYProcessCloseContextCanRetryAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	process := tmuxtest.StartPTYProcess(
		ctx,
		t,
		os.Args[0],
		[]string{"-test.run=^TestPTYProcessCloseHelper$"},
		append(os.Environ(), "LIBTMUX_PTY_CLOSE_HELPER=1"),
	)

	canceled, stopCanceled := context.WithCancel(context.Background())
	stopCanceled()
	if err := process.CloseContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseContext(canceled) error = %v, want context canceled", err)
	}
	if err := process.CloseContext(ctx); err != nil {
		t.Fatalf("CloseContext(retry) error = %v", err)
	}
}

func TestPTYProcessStartContextOwnsChildLifetime(t *testing.T) {
	startupCtx, cancel := context.WithCancel(context.Background())
	process := tmuxtest.StartPTYProcess(
		startupCtx,
		t,
		os.Args[0],
		[]string{"-test.run=^TestPTYProcessCloseHelper$"},
		append(os.Environ(), "LIBTMUX_PTY_CLOSE_HELPER=1"),
	)
	cancel()

	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := process.Wait(ctx); errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() error = %v, child outlived its start context", err)
	}
	select {
	case <-process.Done():
	default:
		t.Fatal("Done() remains open after Wait()")
	}
}

func TestPTYProcessCloseAndWaitAreConcurrent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	process := tmuxtest.StartPTYProcess(
		ctx,
		t,
		os.Args[0],
		[]string{"-test.run=^TestPTYProcessCloseHelper$"},
		append(os.Environ(), "LIBTMUX_PTY_CLOSE_HELPER=1"),
	)

	const callers = 8
	errs := make(chan error, 2*callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(2)
		go func() {
			defer group.Done()
			errs <- process.CloseContext(ctx)
		}()
		go func() {
			defer group.Done()
			errs <- process.Wait(ctx)
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Fatalf("concurrent CloseContext or Wait error = %v", err)
		}
	}
}

func TestPTYProcessWriteHonorsContextWhileChildDoesNotRead(t *testing.T) {
	processCtx, stopProcess := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopProcess()
	process := tmuxtest.StartPTYProcess(
		processCtx,
		t,
		os.Args[0],
		[]string{"-test.run=^TestPTYProcessCloseHelper$"},
		append(os.Environ(), "LIBTMUX_PTY_CLOSE_HELPER=1"),
	)

	writeCtx, stopWrite := context.WithTimeout(processCtx, 100*time.Millisecond)
	defer stopWrite()
	input := make([]byte, 8<<20)
	started := time.Now()
	written, err := process.Write(writeCtx, input)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Write() error = %v, want context deadline exceeded", err)
	}
	if written <= 0 || written >= len(input) {
		t.Fatalf("Write() wrote %d of %d bytes, want a partial write", written, len(input))
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Write() cancellation took %s, want at most 1s", elapsed)
	}
}

func TestPTYProcessCloseHelper(_ *testing.T) {
	if os.Getenv("LIBTMUX_PTY_CLOSE_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func TestPTYProcessOutputHelper(t *testing.T) {
	if os.Getenv("LIBTMUX_PTY_OUTPUT_HELPER") != "1" {
		return
	}
	connection, err := net.Dial("udp", os.Getenv("LIBTMUX_PTY_OUTPUT_READY"))
	if err != nil {
		t.Fatalf("connect to parent: %v", err)
	}
	defer func() { _ = connection.Close() }()

	_, _ = fmt.Fprintln(os.Stdout, "live")
	if _, err := connection.Write([]byte{1}); err != nil {
		t.Fatalf("notify parent: %v", err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set release deadline: %v", err)
	}
	var release [1]byte
	if _, err := connection.Read(release[:]); err != nil {
		t.Fatalf("await release: %v", err)
	}
	_, _ = fmt.Fprintln(os.Stdout, "final")
}
