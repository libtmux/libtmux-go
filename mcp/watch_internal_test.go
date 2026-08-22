package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestACoalescedNotificationIsDeferredNotDropped covers the tail of a burst.
//
// Two notifications about one pane inside the interval are one notification,
// which is the point of the interval. Dropping the second is not: a client
// re-reads when it is told, and a write that landed after that read and before
// the window expired was never mentioned again, so a pane that then went quiet
// left the client stale with nothing coming to correct it.
//
// Driven here rather than through a pane, because how many times tmux reports
// a write and how far apart is not something a test of the coalescing can
// control -- one written through the harness passed whether the deferral was
// there or not, which is a gate that cannot fail.
func TestACoalescedNotificationIsDeferredNotDropped(t *testing.T) {
	const uri = "tmux://panes/%9/content"
	watchers := newWatchers(
		mcp.NewServer(&mcp.Implementation{Name: "coalescing", Version: "1"}, nil),
		tmux.NewServer(tmux.ServerOptions{SocketName: "coalescing-unused"}),
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

// TestUnsubscribingDropsThePanesRecord covers the two maps keyed by URI.
//
// Neither is large and nothing fails when they grow, which is why they grew: a
// server watching panes that come and go kept one entry per pane it had ever
// watched, for as long as the process lived.
func TestUnsubscribingDropsThePanesRecord(t *testing.T) {
	const uri = "tmux://panes/%7/content"
	watchers := newWatchers(
		mcp.NewServer(&mcp.Implementation{Name: "pruning", Version: "1"}, nil),
		tmux.NewServer(tmux.ServerOptions{SocketName: "pruning-unused"}),
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
	return w.owed[uri]
}
