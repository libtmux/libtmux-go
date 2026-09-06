package mcp

import "sync"

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
