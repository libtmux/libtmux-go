package tmux_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// countingWriter counts bytes written through it alongside another writer,
// such as a hash, so a test can assert both a digest and an exact length from
// one streamed pass.
type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// randomPayload returns size pseudorandom bytes from a fixed seed, with NUL
// and newline bytes forced in so the payload cannot survive a line-oriented
// or NUL-terminated path unchanged.
func randomPayload(size int) []byte {
	payload := make([]byte, size)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range payload {
		payload[i] = byte(rng.IntN(256))
	}
	payload[0] = 0
	payload[len(payload)/2] = 0
	payload[len(payload)/2+1] = '\n'
	payload[len(payload)-1] = 0
	return payload
}

//libtmux:real-tmux
func TestBufferStreamRoundTripsBinaryData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server := tmuxtest.NewServer(ctx, t)
	// A snapshot-materialized Server carries daemon identity, so LoadBufferFrom
	// and SaveBufferTo run wrapped in tmux's own if-shell guard the same way a
	// resolved session, window, or pane's commands do.
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	guarded := snapshot.Server()

	payload := randomPayload(8 << 20)
	wantSum := sha256.Sum256(payload)

	name := "pipe-stream-roundtrip"
	if err := guarded.LoadBufferFrom(
		ctx, bytes.NewReader(payload), tmux.LoadBufferFromOptions{Name: &name},
	); err != nil {
		t.Fatalf("LoadBufferFrom() error = %v", err)
	}

	hasher := sha256.New()
	counter := &countingWriter{}
	if err := guarded.SaveBufferTo(
		ctx, io.MultiWriter(hasher, counter), tmux.SaveBufferToOptions{Name: &name},
	); err != nil {
		t.Fatalf("SaveBufferTo() error = %v", err)
	}

	var gotSum [32]byte
	copy(gotSum[:], hasher.Sum(nil))
	if gotSum != wantSum {
		t.Fatalf("SaveBufferTo() digest = %x, want %x", gotSum, wantSum)
	}
	if counter.n != int64(len(payload)) {
		t.Fatalf("SaveBufferTo() wrote %d bytes, want %d", counter.n, len(payload))
	}
}

//libtmux:real-tmux
func TestPaneCaptureToStreamsScreen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// RunInPane resolves its pane through a snapshot, so it too carries daemon
	// identity and CaptureTo runs guarded, the same as TestBufferStreamRoundTripsBinaryData.
	pane := tmuxtest.RunInPane(ctx, t, "printf 'capture-line-one\\ncapture-line-two\\n'")
	tmuxtest.WaitForLine(ctx, t, pane, "capture-line-two")

	want, err := pane.CaptureBytes(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		t.Fatalf("CaptureBytes() error = %v", err)
	}

	var got bytes.Buffer
	if err := pane.CaptureTo(ctx, &got, tmux.CapturePaneRequest{}); err != nil {
		t.Fatalf("CaptureTo() error = %v", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("CaptureTo() = %q, want %q (CaptureBytes on the same static screen)", got.Bytes(), want)
	}
	if !bytes.Contains(got.Bytes(), []byte("capture-line-one")) ||
		!bytes.Contains(got.Bytes(), []byte("capture-line-two")) {
		t.Fatalf("CaptureTo() = %q, want the known screen's two lines", got.Bytes())
	}
}

//libtmux:real-tmux
func TestPaneCaptureToAddressesItsOwnPane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Two panes, and the one under test is deliberately not the active one: a
	// capture that lost its target would read the active pane instead and
	// still look like it worked.
	other := tmuxtest.RunInPane(ctx, t, "printf 'other-pane\n'")
	tmuxtest.WaitForLine(ctx, t, other, "other-pane")
	window, ok := other.Window()
	if !ok {
		t.Fatal("RunInPane's pane carries no window")
	}
	subject, err := window.SplitPane(ctx, tmux.SplitPaneRequest{
		Direction: tmux.PaneDirectionRight, Command: "printf 'subject-pane\n'; sleep 30",
	})
	if err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}
	tmuxtest.WaitForLine(ctx, t, subject, "subject-pane")
	if _, err := other.Select(ctx, tmux.PaneSelectRequest{}); err != nil {
		t.Fatalf("Select() error = %v", err)
	}

	var screen bytes.Buffer
	if err := subject.CaptureTo(ctx, &screen, tmux.CapturePaneRequest{}); err != nil {
		t.Fatalf("CaptureTo() error = %v", err)
	}
	if !bytes.Contains(screen.Bytes(), []byte("subject-pane")) {
		t.Fatalf("CaptureTo() = %q, want the receiver's own screen", screen.String())
	}
	if bytes.Contains(screen.Bytes(), []byte("other-pane")) {
		t.Fatalf("CaptureTo() = %q, want the receiver's screen and not the active pane's", screen.String())
	}
}

//libtmux:real-tmux
func TestBufferStreamUnderConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server := tmuxtest.NewServer(ctx, t)
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	connection, err := session.OpenControl(ctx, tmux.ConnectionOptions{})
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	defer func() { _ = connection.Close() }()

	connectedServer := connection.Server()
	name := "pipe-stream-connection"

	loadErr := connectedServer.LoadBufferFrom(
		ctx, strings.NewReader("connection payload"), tmux.LoadBufferFromOptions{Name: &name},
	)
	t.Logf("LoadBufferFrom() over a connection: %v", loadErr)
	if !errors.Is(loadErr, tmux.ErrConnectionRequiresProcess) {
		t.Fatalf("LoadBufferFrom() error = %v, want ErrConnectionRequiresProcess", loadErr)
	}

	var saved bytes.Buffer
	saveErr := connectedServer.SaveBufferTo(ctx, &saved, tmux.SaveBufferToOptions{Name: &name})
	t.Logf("SaveBufferTo() over a connection: %v", saveErr)
	if !errors.Is(saveErr, tmux.ErrConnectionRequiresProcess) {
		t.Fatalf("SaveBufferTo() error = %v, want ErrConnectionRequiresProcess", saveErr)
	}

	pane, ok, err := connection.Session().ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("ResolveActivePane() = (%#v, %t, %v), want a pane", pane, ok, err)
	}
	var captured bytes.Buffer
	captureErr := pane.CaptureTo(ctx, &captured, tmux.CapturePaneRequest{})
	t.Logf("CaptureTo() over a connection: %v", captureErr)
	if !errors.Is(captureErr, tmux.ErrConnectionRequiresProcess) {
		t.Fatalf("CaptureTo() error = %v, want ErrConnectionRequiresProcess", captureErr)
	}
}

// bufferStreamSizes span the sizes a payload arrives in: well under tmux's
// 16 KiB command limit, past it, and far past it.
var bufferStreamSizes = []struct {
	name string
	size int
}{
	{"8KiB", 8 << 10},
	{"1MiB", 1 << 20},
	{"8MiB", 8 << 20},
}

func BenchmarkLoadBufferFrom(b *testing.B) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	server := tmuxtest.NewServer(ctx, b)
	name := "pipe-stream-bench-load"

	for _, size := range bufferStreamSizes {
		payload := randomPayload(size.size)
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			for b.Loop() {
				if err := server.LoadBufferFrom(
					ctx, bytes.NewReader(payload), tmux.LoadBufferFromOptions{Name: &name},
				); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSaveBufferTo(b *testing.B) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	server := tmuxtest.NewServer(ctx, b)
	name := "pipe-stream-bench-save"

	for _, size := range bufferStreamSizes {
		payload := randomPayload(size.size)
		if err := server.LoadBufferFrom(
			ctx, bytes.NewReader(payload), tmux.LoadBufferFromOptions{Name: &name},
		); err != nil {
			b.Fatal(err)
		}
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			for b.Loop() {
				if err := server.SaveBufferTo(
					ctx, io.Discard, tmux.SaveBufferToOptions{Name: &name},
				); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCaptureScrollback measures the two ways to read a long scrollback
// against each other: one returns it and one writes it.
func BenchmarkCaptureScrollback(b *testing.B) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pane := tmuxtest.RunInPane(ctx, b, "for i in $(seq 1 1500); do printf '%s\\n' \"scrollback line $i\"; done")
	tmuxtest.WaitForLine(ctx, b, pane, "scrollback line 1500")
	whole := tmux.CapturePaneRequest{Start: tmux.CaptureBoundary, End: tmux.CaptureBoundary}

	b.Run("CaptureTo", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := pane.CaptureTo(ctx, io.Discard, whole); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("CaptureBytes", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := pane.CaptureBytes(ctx, whole); err != nil {
				b.Fatal(err)
			}
		}
	})
}
