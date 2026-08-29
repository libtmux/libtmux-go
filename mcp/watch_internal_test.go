package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestACoalescedNotificationIsDeferredNotDropped(t *testing.T) {
	const uri = "tmux://panes/%9/content"
	watchers := newWatchers(
		mcp.NewServer(&mcp.Implementation{Name: "coalescing", Version: "1"}, nil),
		mustInternalTmuxServer(t, tmux.ServerOptions{SocketName: "coalescing-unused"}),
	)
	// Subscribed without a watcher: this is about what notify decides, and
	// starting one would open a connection to a server that is not there.
	watchers.subscribed[uri] = 1
	watchers.spelled[uri] = map[string]int{uri: 1}

	ctx := context.Background()
	watchers.notify(ctx, uri)
	first := watchers.at(uri)
	if first.IsZero() {
		t.Fatal("the first notification was not sent")
	}

	// Inside the window, so it is held back.
	watchers.notify(ctx, uri)
	if !watchers.at(uri).Equal(first) {
		t.Fatal("a notification inside the interval was sent rather than held")
	}
	if !watchers.owes(uri) {
		t.Fatal("a notification inside the interval was dropped, not deferred")
	}

	deadline := time.Now().Add(5 * time.Second)
	for watchers.at(uri).Equal(first) {
		if time.Now().After(deadline) {
			t.Fatal("the deferred notification never went out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if watchers.owes(uri) {
		t.Error("the deferral was not cleared once it fired")
	}
}

func TestUnsubscribingDropsThePanesRecord(t *testing.T) {
	const uri = "tmux://panes/%7/content"
	watchers := newWatchers(
		mcp.NewServer(&mcp.Implementation{Name: "pruning", Version: "1"}, nil),
		mustInternalTmuxServer(t, tmux.ServerOptions{SocketName: "pruning-unused"}),
	)
	watchers.subscribed[uri] = 1
	watchers.spelled[uri] = map[string]int{uri: 1}

	ctx := context.Background()
	watchers.notify(ctx, uri) // records when it went out
	watchers.notify(ctx, uri) // inside the window, so one is owed
	if watchers.at(uri).IsZero() || !watchers.owes(uri) {
		t.Fatal("the notification did not leave a record to drop")
	}

	watchers.remove(uri, uri)
	if !watchers.at(uri).IsZero() {
		t.Error("the coalescing window outlived the subscription")
	}
	if watchers.owes(uri) {
		t.Error("the deferral outlived the subscription")
	}
}

// at is when the last notification about one URI went out.
func (w *watchers) at(uri string) time.Time {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.notified[uri]
}

// owes reports a notification held back and not yet sent.
func (w *watchers) owes(uri string) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.owed[uri] != nil
}
