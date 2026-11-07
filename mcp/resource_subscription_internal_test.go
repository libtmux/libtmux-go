package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// watcher is a client that subscribes to resources and collects what the
// server pushes. Its waits are the notification and the acknowledgement,
// never a timer.
type watcher struct {
	session    *sdk.ClientSession
	version    string
	updates    chan string
	acks       chan struct{}
	refusals   chan error
	disconnect func()
}

func connectWatcher(ctx context.Context, t *testing.T, instance *Instance, version string) *watcher {
	t.Helper()
	w := &watcher{version: version, updates: make(chan string, 64), acks: make(chan struct{}, 8), refusals: make(chan error, 8)}
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := instance.Connect(ctx, AssumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	// The SDK client sends a listen without waiting for its answer, so a
	// refusal never reaches Subscribe. The server's own middleware sees it.
	instance.server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
			result, err := next(ctx, method, request)
			if method == "subscriptions/listen" && err != nil && request.GetSession() == sdk.Session(serverSession.sdk) {
				w.refusals <- err
			}
			return result, err
		}
	})
	client := sdk.NewClient(&sdk.Implementation{Name: "watcher", Version: "1"}, &sdk.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, request *sdk.ResourceUpdatedNotificationRequest) {
			w.updates <- request.Params.URI
		},
	})
	// The client API gives no sight of the acknowledgement that a listen is
	// live, and a listen blocks for its whole life.
	client.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
			if method == "notifications/subscriptions/acknowledged" {
				w.acks <- struct{}{}
			}
			return next(ctx, method, request)
		}
	})
	session, err := client.Connect(ctx, clientTransport, &sdk.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	w.session = session
	w.disconnect = func() {
		_ = session.Close()
		_ = serverSession.Close()
	}
	t.Cleanup(w.disconnect)
	return w
}

// subscribe returns once the subscription is live, or with the refusal. On
// protocol 2026-07-28 a subscription is a listen that never completes, so it
// is live when the server acknowledges it.
func (w *watcher) subscribe(ctx context.Context, uri string) error {
	if w.version != "2026-07-28" {
		return w.session.Subscribe(ctx, &sdk.SubscribeParams{URI: uri})
	}
	go func() { _ = w.session.Subscribe(ctx, &sdk.SubscribeParams{URI: uri}) }()
	select {
	case <-w.acks:
		return nil
	case err := <-w.refusals:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type watchedPanes struct {
	instance *Instance
	target   tmux.Server
	first    tmux.Pane
	second   tmux.Pane
}

// twoPanes starts a server with one window of two panes.
func twoPanes(ctx context.Context, t *testing.T) watchedPanes {
	t.Helper()
	request := tmux.NewSessionRequest{Name: "watch"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	instance, err := NewServer(target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	snapshot, err := target.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	window, err := snapshot.Sessions()[0].ResolveActiveWindow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := window.ResolveActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := first.Split(ctx, tmux.SplitPaneRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return watchedPanes{instance: instance, target: target, first: first, second: second}
}

// A pane's output is announced on the URI the client subscribed with, on both
// protocol generations, and reading it then shows the output.
//
//libtmux:real-tmux
func TestPaneContentSubscription(t *testing.T) {
	for _, version := range resourceProtocols {
		t.Run(version, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			request := tmux.NewSessionRequest{Name: "res"}
			target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
				FixedShell: true, InitialSession: &request,
			})
			tmuxtest.RequireVersion(ctx, t, target, tmux.MinimumConnectionVersion)
			instance, err := NewServer(target)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = instance.Close() })
			w := connectWatcher(ctx, t, instance, version)

			snapshot, err := target.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			window, err := snapshot.Sessions()[0].ResolveActiveWindow(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pane, err := window.ResolveActivePane(ctx)
			if err != nil {
				t.Fatal(err)
			}
			uri := paneContentURI(pane.ID())
			if err := w.subscribe(ctx, uri); err != nil {
				t.Fatal(err)
			}

			command := "echo MARK$((20+22))"
			if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command}); err != nil {
				t.Fatal(err)
			}
			notifications := 0
			for {
				select {
				case got := <-w.updates:
					if got != uri {
						t.Fatalf("updated %q, want %q", got, uri)
					}
					notifications++
				case <-ctx.Done():
					t.Fatalf("no update showing MARK42 after %d notifications", notifications)
				}
				read, err := w.session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
				if err != nil {
					t.Fatal(err)
				}
				// The typed echo holds "MARK$(", only the result holds MARK42.
				if strings.Contains(read.Contents[0].Text, "MARK42") {
					return
				}
			}
		})
	}
}

// One control client serves every session watching a pane, it ends with the
// last of them, and nothing that ends a session leaves it running.
//
//libtmux:real-tmux
func TestPaneWatchLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	panes := twoPanes(ctx, t)
	tmuxtest.RequireVersion(ctx, t, panes.target, tmux.MinimumConnectionVersion)
	instance := panes.instance
	watchOf := func(pane tmux.Pane) *paneWatch {
		instance.tools.watches.mutex.Lock()
		defer instance.tools.watches.mutex.Unlock()
		return instance.tools.watches.panes[pane.ID()]
	}
	ended := func(watch *paneWatch, what string) {
		t.Helper()
		select {
		case <-watch.done:
		case <-ctx.Done():
			t.Fatalf("the watch did not end: %s", what)
		}
	}
	firstURI := paneContentURI(panes.first.ID())

	a := connectWatcher(ctx, t, instance, "2025-11-25")
	b := connectWatcher(ctx, t, instance, "2025-11-25")
	for _, w := range []*watcher{a, b} {
		if err := w.subscribe(ctx, firstURI); err != nil {
			t.Fatal(err)
		}
	}
	shared := watchOf(panes.first)
	if shared == nil {
		t.Fatal("no watch after two subscriptions")
	}
	if n := len(instance.tools.watches.panes); n != 1 {
		t.Fatalf("watches = %d, want one shared", n)
	}
	// One session leaving keeps the watch for the other.
	if err := a.session.Unsubscribe(ctx, &sdk.UnsubscribeParams{URI: firstURI}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-shared.done:
		t.Fatal("the watch ended while another session still subscribed")
	default:
	}
	// A client that just goes away never unsubscribes; the last reference goes
	// with its session.
	b.disconnect()
	ended(shared, "after the last session disconnected")
	if watchOf(panes.first) != nil {
		t.Fatal("an ended watch is still registered")
	}

	// A pane that exits is announced once more, then released.
	c := connectWatcher(ctx, t, instance, "2025-11-25")
	secondURI := paneContentURI(panes.second.ID())
	if err := c.subscribe(ctx, secondURI); err != nil {
		t.Fatal(err)
	}
	dying := watchOf(panes.second)
	exit := "exit"
	if err := panes.second.SendKeys(ctx, tmux.SendKeysRequest{Command: &exit}); err != nil {
		t.Fatal(err)
	}
	for announced := false; !announced; {
		select {
		case got := <-c.updates:
			announced = got == secondURI
		case <-ctx.Done():
			t.Fatal("the pane's exit was never announced")
		}
	}
	ended(dying, "after the pane exited")
	if err := c.subscribe(ctx, secondURI); err == nil {
		t.Fatal("subscribing to a dead pane succeeded")
	} else {
		assertInvalidParams(t, err)
	}
}

// Past the cap a new pane is refused, while a pane already watched is shared
// without spending another control client.
//
//libtmux:real-tmux
func TestPaneWatchesAreCapped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	panes := twoPanes(ctx, t)
	tmuxtest.RequireVersion(ctx, t, panes.target, tmux.MinimumConnectionVersion)
	panes.instance.tools.watches.limit = 1
	a := connectWatcher(ctx, t, panes.instance, "2025-11-25")
	b := connectWatcher(ctx, t, panes.instance, "2025-11-25")

	if err := a.subscribe(ctx, paneContentURI(panes.first.ID())); err != nil {
		t.Fatal(err)
	}
	if err := b.subscribe(ctx, paneContentURI(panes.first.ID())); err != nil {
		t.Fatalf("sharing a watched pane: %v", err)
	}
	err := b.subscribe(ctx, paneContentURI(panes.second.ID()))
	if err == nil {
		t.Fatal("a second pane was watched past the cap")
	}
	assertInvalidParams(t, err)
}

// What cannot be subscribed to is invalid params, on either protocol, rather
// than a failure of the server.
//
//libtmux:real-tmux
func TestSubscribingToWhatIsNotWatchableIsInvalidParams(t *testing.T) {
	for _, version := range resourceProtocols {
		t.Run(version, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			panes := twoPanes(ctx, t)
			tmuxtest.RequireVersion(ctx, t, panes.target, tmux.MinimumConnectionVersion)
			w := connectWatcher(ctx, t, panes.instance, version)
			for name, uri := range map[string]string{
				"a listing":        "tmux://sessions",
				"pane info":        "tmux://panes/" + escapeID(panes.first.ID().String()),
				"an unknown pane":  "tmux://panes/%2599/content",
				"another spelling": "tmux://panes/%25%3" + panes.first.ID().String()[1:] + "/content",
			} {
				if err := w.subscribe(ctx, uri); err == nil {
					t.Errorf("%s: subscribing to %s succeeded", name, uri)
				} else {
					assertInvalidParams(t, err)
				}
			}
			if n := len(panes.instance.tools.watches.panes); n != 0 {
				t.Errorf("refusals left %d watches", n)
			}
		})
	}
}

// Below tmux 3.7 there is no control observation to push from, so a
// subscription is refused with the remedy, and a read still works.
//
//libtmux:real-tmux
func TestSubscribingBelowTheControlFloorIsRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	request := tmux.NewSessionRequest{Name: "old"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	minimum, err := tmux.ParseVersion(tmux.MinimumConnectionVersion)
	if err != nil {
		t.Fatal(err)
	}
	if target.RequireVersion(ctx, minimum) == nil {
		t.Skipf("this tmux is %s or newer; put an older one first in PATH", tmux.MinimumConnectionVersion)
	}
	instance, err := NewServer(target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	w := connectWatcher(ctx, t, instance, "2025-11-25")
	snapshot, err := target.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	window, err := snapshot.Sessions()[0].ResolveActiveWindow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pane, err := window.ResolveActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	uri := paneContentURI(pane.ID())
	err = w.subscribe(ctx, uri)
	if err == nil {
		t.Fatal("subscribing below the floor succeeded")
	}
	assertInvalidParams(t, err)
	if !strings.Contains(err.Error(), "needs tmux "+tmux.MinimumConnectionVersion) ||
		!strings.Contains(err.Error(), "wait_for_text") {
		t.Errorf("refusal = %v, want the floor and the remedy", err)
	}
	if _, err := w.session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri}); err != nil {
		t.Errorf("reading below the floor: %v", err)
	}
}
