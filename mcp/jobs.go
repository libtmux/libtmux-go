package mcp

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Detached commands keep server-owned state; handles never expose file paths.
// Completed results are collected idempotently while retained.

// jobsRetained bounds retained handles and results. Adding a job evicts the
// oldest retained job and removes any remaining temporary files.
const jobsRetained = 32

type job struct {
	id        string
	paneID    tmux.PaneID
	channel   string
	command   string
	directory string
	openedAt  string
	closedAt  string
	statusAt  string
	started   time.Time

	// Completed results outlive their released temporary files.
	finished   bool
	exitStatus int
	output     []string
	// atCeiling preserves loss before later caller-specific bounds.
	atCeiling truncation
	ended     time.Time
}

type jobs struct {
	mutex sync.Mutex
	byID  map[string]*job
	// order is oldest first for eviction.
	order  []string
	closed bool
}

func newJobs() *jobs {
	return &jobs{byID: map[string]*job{}}
}

func (j *jobs) keep(entry *job) bool {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	if j.closed {
		return false
	}
	stored := *entry
	stored.output = slices.Clone(entry.output)
	j.byID[entry.id] = &stored
	j.order = append(j.order, entry.id)
	for len(j.order) > jobsRetained {
		oldest := j.order[0]
		j.order = j.order[1:]
		if dropped, ok := j.byID[oldest]; ok {
			delete(j.byID, oldest)
			_ = os.RemoveAll(dropped.directory)
		}
	}
	return true
}

func (j *jobs) find(id string) (job, bool) {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	entry, ok := j.byID[id]
	if !ok {
		return job{}, false
	}
	result := *entry
	result.output = slices.Clone(entry.output)
	return result, true
}

// settle caches the result before releasing its files.
func (j *jobs) settle(
	id string,
	status int,
	output []string,
	atCeiling truncation,
	ended time.Time,
) {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	entry, ok := j.byID[id]
	if !ok || entry.finished {
		return
	}
	entry.finished = true
	entry.exitStatus = status
	entry.output = slices.Clone(output)
	entry.atCeiling = atCeiling
	entry.ended = ended
	_ = os.RemoveAll(entry.directory)
}

func (j *jobs) close() {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	j.closed = true
	for id, entry := range j.byID {
		delete(j.byID, id)
		_ = os.RemoveAll(entry.directory)
	}
	j.order = nil
}

func unknownJob(id string) error {
	return fmt.Errorf(
		"%q is not a job owned by this MCP session: only its last %d are "+
			"kept, and older ones are dropped as newer commands are started",
		id, jobsRetained)
}

type getJobInput struct {
	JobID string `json:"jobId" jsonschema:"the handle a detached run_command returned"`
	// TimeoutSeconds zero polls without waiting.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty" jsonschema:"wait up to this long for the command to finish; zero reports whether it has and returns at once"`
	MaxLines       int `json:"maxLines,omitempty" jsonschema:"how many lines of output to return at most, keeping the last ones"`
	MaxBytes       int `json:"maxBytes,omitempty" jsonschema:"how many bytes to return at most, keeping the last lines"`
}

type getJobOutput struct {
	JobID          string  `json:"jobId"`
	PaneID         string  `json:"paneId"`
	Command        string  `json:"command"`
	Finished       bool    `json:"finished"`
	ExitStatus     *int    `json:"exitStatus,omitempty"`
	ElapsedSeconds float64 `json:"elapsedSeconds"`
	// Running is what the pane is running now, reported while the command has
	// not finished. A shell may mean interruption prevented status recording.
	Running                 string   `json:"running,omitempty"`
	Output                  []string `json:"output,omitempty"`
	EffectiveTimeoutSeconds int      `json:"effectiveTimeoutSeconds,omitempty"`
	TimeoutClamped          bool     `json:"timeoutClamped,omitempty"`
	truncation
}

// getJob caches completed results before deleting their temporary files.
func (t *tools) getJob(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input getJobInput,
) (*mcp.CallToolResult, getJobOutput, error) {
	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, getJobOutput{}, err
	}
	owned, err := t.sessionJobs(request)
	if err != nil {
		return nil, getJobOutput{}, err
	}
	entry, ok := owned.find(input.JobID)
	if !ok {
		return nil, getJobOutput{}, unknownJob(input.JobID)
	}
	output := getJobOutput{
		JobID:   entry.id,
		PaneID:  entry.paneID.String(),
		Command: entry.command,
	}

	if entry.finished {
		status := entry.exitStatus
		output.Finished = true
		output.ExitStatus = &status
		output.ElapsedSeconds = entry.ended.Sub(entry.started).Seconds()
		output.Output, output.truncation = limits.apply(entry.output)
		output.truncation = addTruncation(output.truncation, entry.atCeiling)
		return nil, output, nil
	}

	if input.TimeoutSeconds > 0 {
		timeout, clamped := resolveWaitTimeout(input.TimeoutSeconds)
		output.EffectiveTimeoutSeconds = int(timeout.Seconds())
		output.TimeoutClamped = clamped
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		reporter := newProgressReporter(request, timeout, "waiting for the command to finish")
		defer reporter.stop()
		// A blocking wait must not occupy a pooled control connection.
		waiter := t.tmux().WithEngine(t.tmux().SubprocessEngine())
		_ = waiter.WaitFor(waitCtx, tmux.WaitForRequest{Channel: entry.channel})
	}

	output.ElapsedSeconds = time.Since(entry.started).Seconds()
	recorded, readErr := os.ReadFile(entry.statusAt)
	if readErr != nil {
		// Status read failures take the unfinished path.
		if pane, paneErr := t.tmux().Pane(ctx, entry.paneID); paneErr == nil {
			output.Running, _ = pane.Formats().PaneCurrentCommand()
		}
		return nil, output, nil
	}
	status, convertErr := strconv.Atoi(strings.TrimSpace(string(recorded)))
	if convertErr != nil {
		return nil, output, fmt.Errorf("unreadable exit status %q", recorded)
	}
	output.Finished = true
	output.ExitStatus = &status

	// Collect at the ceiling so later calls may request more than this one.
	var collected runCommandOutput
	if pane, paneErr := t.tmux().Pane(ctx, entry.paneID); paneErr == nil {
		attachCommandOutput(ctx, pane, entry.openedAt, entry.closedAt,
			bounds{lines: ceilingMaxLines, bytes: ceilingMaxBytes}, &collected)
	}
	ended := time.Now()
	owned.settle(entry.id, status, collected.Output, collected.truncation, ended)
	output.ElapsedSeconds = ended.Sub(entry.started).Seconds()
	output.Output, output.truncation = limits.apply(collected.Output)
	output.truncation = addTruncation(output.truncation, collected.truncation)
	return nil, output, nil
}

func (t *tools) sessionJobs(request *mcp.CallToolRequest) (*jobs, error) {
	if request == nil || request.Session == nil {
		return nil, ErrInstanceClosed
	}
	scope, err := t.instance.scope(request.Session)
	if err != nil {
		return nil, err
	}
	return scope.jobs, nil
}

// addTruncation combines loss from collection and caller-specific bounds.
func addTruncation(caller, earlier truncation) truncation {
	caller.TruncatedLines += earlier.TruncatedLines
	caller.TruncatedBytes += earlier.TruncatedBytes
	caller.Truncated = caller.TruncatedLines > 0 || caller.TruncatedBytes > 0
	return caller
}
