package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSubscriptionHandlersRejectIncompleteRequests(t *testing.T) {
	toolset := &tools{capabilities: newCapabilitySet(allCapabilities)}
	serverSession := &sdk.ServerSession{}

	subscribe := []struct {
		name    string
		request *sdk.SubscribeRequest
	}{
		{name: "nil request"},
		{name: "nil params", request: &sdk.SubscribeRequest{Session: serverSession}},
		{
			name: "nil session",
			request: &sdk.SubscribeRequest{
				Params: &sdk.SubscribeParams{URI: resourceSessions},
			},
		},
	}
	for _, test := range subscribe {
		t.Run("subscribe "+test.name, func(t *testing.T) {
			if err := toolset.subscribe(t.Context(), test.request); !errors.Is(err, ErrInstanceClosed) {
				t.Fatalf("subscribe() error = %v, want ErrInstanceClosed", err)
			}
		})
	}

	unsubscribe := []struct {
		name    string
		request *sdk.UnsubscribeRequest
	}{
		{name: "nil request"},
		{name: "nil params", request: &sdk.UnsubscribeRequest{Session: serverSession}},
		{
			name: "nil session",
			request: &sdk.UnsubscribeRequest{
				Params: &sdk.UnsubscribeParams{URI: resourceSessions},
			},
		},
	}
	for _, test := range unsubscribe {
		t.Run("unsubscribe "+test.name, func(t *testing.T) {
			if err := toolset.unsubscribe(t.Context(), test.request); !errors.Is(err, ErrInstanceClosed) {
				t.Fatalf("unsubscribe() error = %v, want ErrInstanceClosed", err)
			}
		})
	}
}

func TestSubscribeRejectsURIOutsideTheServedHierarchyBeforeRuntimeUse(t *testing.T) {
	toolset := &tools{capabilities: newCapabilitySet(allCapabilities)}
	request := &sdk.SubscribeRequest{
		Session: &sdk.ServerSession{},
		Params:  &sdk.SubscribeParams{URI: "tmux://not-a-resource/1"},
	}

	err := toolset.subscribe(t.Context(), request)
	if err == nil || !strings.Contains(err.Error(), "not a tmux resource") {
		t.Fatalf("subscribe() error = %v, want served-resource refusal", err)
	}
}

func TestSubscribableResourceURIRecognizesOnlyServedShapes(t *testing.T) {
	for _, uri := range []string{
		resourceSessions,
		"tmux://sessions/work",
		"tmux://sessions/spaced%20name/windows",
		"tmux://windows/1",
		"tmux://windows/%401/panes",
		"tmux://panes/1",
		"tmux://panes/%1/content",
		"tmux://panes/%251/content",
	} {
		if !subscribableResourceURI(uri) {
			t.Errorf("subscribableResourceURI(%q) = false, want true", uri)
		}
	}
	for _, uri := range []string{
		"",
		"tmux://sessions/",
		"tmux://sessions/work/other",
		"tmux://windows/1/content",
		"tmux://panes/1/other",
		"tmux://panes/1/content?view=full",
		"tmux://nonsense/1",
	} {
		if subscribableResourceURI(uri) {
			t.Errorf("subscribableResourceURI(%q) = true, want false", uri)
		}
	}
}

func TestSubscriptionSpellingAdmissionTracksSDKRetainedKeys(t *testing.T) {
	watchers := admissionTestWatchers()

	for index := range watchSubscriptionSpellingLimit {
		uri := fmt.Sprintf("tmux://sessions/session-%d", index)
		if _, err := watchers.add(uri, uri); err != nil {
			t.Fatalf("subscription %d error = %v", index+1, err)
		}
	}
	rejected := "tmux://sessions/one-too-many"
	if _, err := watchers.add(rejected, rejected); err == nil {
		t.Fatalf("subscription %d succeeded, want retained-key limit",
			watchSubscriptionSpellingLimit+1)
	}

	first := "tmux://sessions/session-0"
	watchers.removeExplicit(first, first)
	if _, err := watchers.add(rejected, rejected); err != nil {
		t.Fatalf("new spelling after explicit unsubscribe: %v", err)
	}
	watchers.removeExplicit(rejected, rejected)
	if _, err := watchers.add(first, first); err != nil {
		t.Fatalf("resubscribe after explicit unsubscribe: %v", err)
	}
}

func TestDisconnectDoesNotReclaimSDKRetainedSpellings(t *testing.T) {
	watchers := admissionTestWatchers()
	for index := range watchSubscriptionSpellingLimit {
		uri := fmt.Sprintf("tmux://sessions/disconnected-%d", index)
		if _, err := watchers.add(uri, uri); err != nil {
			t.Fatalf("subscription %d error = %v", index+1, err)
		}
		watchers.remove(uri, uri)
	}

	replacement := "tmux://sessions/after-disconnect"
	if _, err := watchers.add(replacement, replacement); err == nil {
		t.Fatal("new spelling after disconnect succeeded beyond retained-key limit")
	}
}

func admissionTestWatchers() *watchers {
	return &watchers{
		subscribed: map[string]int{},
		spelled:    map[string]map[string]int{},
		notified:   map[string]time.Time{},
		owed:       map[string]*time.Timer{},
		active: &watchGeneration{
			cancel: func() {},
			done:   make(chan struct{}),
			ready:  make(chan struct{}),
		},
	}
}

func TestRebuildSupersedesAnOwedCoalescedNotification(t *testing.T) {
	const uri = "tmux://panes/9/content"
	target := mustInternalTmuxServer(t, tmux.ServerOptions{SocketName: "rebuild-coalescing-unused"})
	runtimeCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	watchers := newWatchers(newRuntime(runtimeCtx, target, func(error) { cancel() }))
	t.Cleanup(watchers.close)
	watchers.subscribed[uri] = 1
	watchers.spelled[uri] = map[string]int{uri: 1}

	watchers.notify(uri)
	watchers.notify(uri)
	if !watchers.owes(uri) {
		t.Fatal("notification inside the coalescing interval was not deferred")
	}
	watchers.tellEveryone()
	rebuilt := watchers.at(uri)
	if rebuilt.IsZero() {
		t.Fatal("rebuild did not invalidate the resource")
	}

	time.Sleep(watchNotifyInterval + 100*time.Millisecond)
	if got := watchers.at(uri); !got.Equal(rebuilt) {
		t.Fatalf("stale timer sent after rebuild at %v, rebuild sent at %v", got, rebuilt)
	}
	if watchers.owes(uri) {
		t.Fatal("stale coalescing timer survived rebuild")
	}
}

func TestWatcherCloseUsesOneGlobalJoinBudget(t *testing.T) {
	target := mustInternalTmuxServer(t, tmux.ServerOptions{SocketName: "close-budget-unused"})
	runtimeCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	watchers := newWatchers(newRuntime(runtimeCtx, target, func(error) { cancel() }))
	const budget = 20 * time.Millisecond
	watchers.shutdownWait = budget
	watchers.active = &watchGeneration{
		cancel: func() {},
		done:   make(chan struct{}),
		ready:  make(chan struct{}),
	}

	started := time.Now()
	watchers.close()
	elapsed := time.Since(started)
	if elapsed < budget/2 {
		t.Fatalf("close returned in %v without waiting for its join budget %v", elapsed, budget)
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("close took %v, want one %v global join budget", elapsed, budget)
	}
	watchers.mutex.Lock()
	watchers.active = nil
	watchers.mutex.Unlock()
}
