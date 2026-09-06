package mcp

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
)

type paneInputEndpoint struct {
	id   uint64
	info os.FileInfo
}

type paneInputEndpointRegistry struct {
	mutex   sync.Mutex
	next    uint64
	entries []paneInputEndpoint
}

var processPaneInputEndpoints paneInputEndpointRegistry

func (r *paneInputEndpointRegistry) identify(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for _, entry := range r.entries {
		if os.SameFile(info, entry.info) {
			return entry.id, nil
		}
	}
	r.next++
	r.entries = append(r.entries, paneInputEndpoint{id: r.next, info: info})
	return r.next, nil
}

var processPaneRuns paneRunCoordinator

// paneRunCoordinator permits one run_shell_command setup per pane at a time.
// The key includes the frozen socket route so pane ids on distinct daemons do
// not contend.
type paneRunCoordinator struct {
	mutex  sync.Mutex
	next   uint64
	active map[paneRunKey]uint64
}

type paneRunKey struct {
	socket string
	paneID string
}

type paneRunLease struct {
	key   paneRunKey
	token uint64
}

func (c *paneRunCoordinator) acquire(key paneRunKey) (paneRunLease, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.active == nil {
		c.active = make(map[paneRunKey]uint64)
	}
	if _, exists := c.active[key]; exists {
		return paneRunLease{}, false
	}
	c.next++
	c.active[key] = c.next
	return paneRunLease{key: key, token: c.next}, true
}

func (c *paneRunCoordinator) owns(lease paneRunLease, key paneRunKey) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return lease.key == key && c.active[key] == lease.token
}

func (c *paneRunCoordinator) release(lease paneRunLease) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.active[lease.key] == lease.token {
		delete(c.active, lease.key)
	}
}

type paneInputReservationKind uint8

const (
	paneInputReservationInput paneInputReservationKind = iota
	paneInputReservationRun
)

type paneInputReservation struct {
	token uint64
	kind  paneInputReservationKind
}

type paneInputCoordinator struct {
	mutex  sync.Mutex
	next   uint64
	active map[paneInputIdentity]paneInputReservation
}

var processPaneInputs paneInputCoordinator

type paneInputLease struct {
	identities []paneInputIdentity
	token      uint64
	kind       paneInputReservationKind
}

func (c *paneInputCoordinator) acquire(
	identities []paneInputIdentity,
	kind paneInputReservationKind,
	tool string,
) (paneInputLease, error) {
	wanted := slices.Clone(identities)
	slices.SortFunc(wanted, comparePaneInputIdentity)
	if len(wanted) == 0 || tool == "" {
		return paneInputLease{}, errors.New("pane input reservation identity is invalid")
	}
	for index, identity := range wanted {
		if identity.endpointID == 0 || identity.serverPID == 0 ||
			identity.serverStartTime == 0 || !canonicalPaneID(identity.paneID) ||
			(index > 0 && identity == wanted[index-1]) {
			return paneInputLease{}, errors.New("pane input reservation identity is invalid")
		}
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.active == nil {
		c.active = make(map[paneInputIdentity]paneInputReservation)
	}
	for _, identity := range wanted {
		if active, found := c.active[identity]; found {
			if active.kind == paneInputReservationRun {
				return paneInputLease{}, fmt.Errorf(
					"%s refused for pane %s: an earlier run_shell_command is still active",
					tool, identity.paneID,
				)
			}
			return paneInputLease{}, fmt.Errorf(
				"%s refused for pane %s: another pane-input dispatch is active",
				tool, identity.paneID,
			)
		}
	}
	c.next++
	for _, identity := range wanted {
		c.active[identity] = paneInputReservation{token: c.next, kind: kind}
	}
	return paneInputLease{identities: wanted, token: c.next, kind: kind}, nil
}

func (c *paneInputCoordinator) owns(lease paneInputLease, identities []paneInputIdentity) bool {
	wanted := slices.Clone(identities)
	slices.SortFunc(wanted, comparePaneInputIdentity)
	if !slices.Equal(wanted, lease.identities) {
		return false
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	for _, identity := range wanted {
		active, found := c.active[identity]
		if !found || active.token != lease.token || active.kind != lease.kind {
			return false
		}
	}
	return true
}

func (c *paneInputCoordinator) release(lease paneInputLease) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	for _, identity := range lease.identities {
		active, found := c.active[identity]
		if found && active.token == lease.token && active.kind == lease.kind {
			delete(c.active, identity)
		}
	}
}

func comparePaneInputIdentity(left, right paneInputIdentity) int {
	if left.endpointID != right.endpointID {
		return cmp.Compare(left.endpointID, right.endpointID)
	}
	if left.serverPID != right.serverPID {
		return cmp.Compare(left.serverPID, right.serverPID)
	}
	if left.serverStartTime != right.serverStartTime {
		return cmp.Compare(left.serverStartTime, right.serverStartTime)
	}
	return strings.Compare(left.paneID, right.paneID)
}
