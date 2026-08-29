package mcp

import (
	"context"
	"errors"
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

const (
	// jobsRetained bounds retained handles and results. Adding a job evicts the
	// oldest retained job and removes any remaining temporary files.
	jobsRetained = 32
	// jobCompletionPollInterval bounds how long collection waits to observe a
	// command's atomically published completion files.
	jobCompletionPollInterval = 50 * time.Millisecond
)

var (
	errJobCapacity        = errors.New("all retained jobs are currently being collected")
	errJobHandleCollision = errors.New("generated job handle already exists")
)

type job struct {
	id        string
	paneID    tmux.PaneID
	command   string
	directory string
	openedAt  string
	closedAt  string
	statusAt  string
	started   time.Time

	collecting     bool
	collectionDone chan struct{}

	// Completed results outlive their released temporary files.
	finished          bool
	exitStatus        int
	output            []string
	outputUnavailable string
	linesMissed       bool
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

func (j *jobs) keep(entry *job) error {
	j.mutex.Lock()
	if j.closed {
		j.mutex.Unlock()
		return ErrInstanceClosed
	}
	if _, exists := j.byID[entry.id]; exists {
		j.mutex.Unlock()
		return errJobHandleCollision
	}
	stored := *entry
	stored.output = slices.Clone(entry.output)
	j.byID[entry.id] = &stored
	j.order = append(j.order, entry.id)
	directories := make([]string, 0)
	retained := true
	for len(j.order) > jobsRetained {
		dropAt := slices.IndexFunc(j.order, func(id string) bool {
			candidate := j.byID[id]
			return candidate != nil && !candidate.collecting
		})
		// The newly inserted job is not collecting, so it is the fallback when
		// every older job has an active collector.
		if dropAt < 0 {
			dropAt = len(j.order) - 1
		}
		droppedID := j.order[dropAt]
		j.order = slices.Delete(j.order, dropAt, dropAt+1)
		dropped := j.byID[droppedID]
		delete(j.byID, droppedID)
		retained = retained && droppedID != entry.id
		if dropped.directory != "" {
			directories = append(directories, dropped.directory)
			dropped.directory = ""
		}
	}
	j.mutex.Unlock()
	removeJobDirectories(directories)
	if !retained {
		return errJobCapacity
	}
	return nil
}

// discard removes an admitted job whose command was not delivered.
func (j *jobs) discard(id string) {
	j.mutex.Lock()
	entry := j.byID[id]
	if entry == nil || entry.collecting || entry.finished {
		j.mutex.Unlock()
		return
	}
	delete(j.byID, id)
	if at := slices.Index(j.order, id); at >= 0 {
		j.order = slices.Delete(j.order, at, at+1)
	}
	directory := entry.directory
	entry.directory = ""
	j.mutex.Unlock()
	removeJobDirectories([]string{directory})
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

// beginCollection elects one caller to collect and settle a job. Followers
// observe its completion channel instead of touching its temporary files.
func (j *jobs) beginCollection(id string) (job, bool, chan struct{}, bool) {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	entry, ok := j.byID[id]
	if !ok {
		return job{}, false, nil, false
	}
	if entry.finished {
		result := *entry
		result.output = slices.Clone(entry.output)
		return result, false, nil, true
	}
	if entry.collecting {
		result := *entry
		result.output = slices.Clone(entry.output)
		return result, false, entry.collectionDone, true
	}
	entry.collecting = true
	entry.collectionDone = make(chan struct{})
	result := *entry
	result.output = slices.Clone(entry.output)
	return result, true, entry.collectionDone, true
}

func (j *jobs) endCollection(id string, done chan struct{}) {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	entry := j.byID[id]
	if entry == nil || !entry.collecting || entry.collectionDone != done {
		return
	}
	entry.collecting = false
	entry.collectionDone = nil
	close(done)
}

// settle caches the result before releasing its files.
func (j *jobs) settle(
	id string,
	status int,
	output []string,
	atCeiling truncation,
	outputUnavailable string,
	linesMissed bool,
	ended time.Time,
) (job, bool) {
	j.mutex.Lock()
	entry, ok := j.byID[id]
	if !ok {
		j.mutex.Unlock()
		return job{}, false
	}
	if !entry.finished {
		entry.finished = true
		entry.exitStatus = status
		entry.output = slices.Clone(output)
		entry.atCeiling = atCeiling
		entry.outputUnavailable = outputUnavailable
		entry.linesMissed = linesMissed
		entry.ended = ended
	}
	if entry.collecting {
		close(entry.collectionDone)
		entry.collecting = false
		entry.collectionDone = nil
	}
	directory := entry.directory
	entry.directory = ""
	result := *entry
	result.output = slices.Clone(entry.output)
	j.mutex.Unlock()
	removeJobDirectories([]string{directory})
	return result, true
}

func (j *jobs) close() {
	j.mutex.Lock()
	j.closed = true
	directories := make([]string, 0, len(j.byID))
	for id, entry := range j.byID {
		delete(j.byID, id)
		if entry.collecting {
			close(entry.collectionDone)
			entry.collecting = false
			entry.collectionDone = nil
		}
		if entry.directory != "" {
			directories = append(directories, entry.directory)
			entry.directory = ""
		}
	}
	j.order = nil
	j.mutex.Unlock()
	removeJobDirectories(directories)
}

func removeJobDirectories(directories []string) {
	for _, directory := range directories {
		if directory != "" {
			_ = os.RemoveAll(directory)
		}
	}
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
	JobID    string `json:"jobId"`
	PaneID   string `json:"paneId"`
	Command  string `json:"command"`
	Finished bool   `json:"finished"`
	// CollectionPending reports that the command committed but a zero-time poll
	// left its pane output for a later call with a positive timeout.
	CollectionPending bool    `json:"collectionPending,omitempty"`
	ExitStatus        *int    `json:"exitStatus,omitempty"`
	ElapsedSeconds    float64 `json:"elapsedSeconds"`
	// Running is what the pane is running now, reported while the command has
	// not finished. A shell may mean interruption prevented status recording.
	Running                 string   `json:"running,omitempty"`
	Output                  []string `json:"output,omitempty"`
	OutputUnavailable       string   `json:"outputUnavailable,omitempty"`
	LinesMissed             bool     `json:"linesMissed,omitempty"`
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
	if input.TimeoutSeconds < 0 {
		return nil, getJobOutput{}, errors.New("timeoutSeconds must be zero or greater")
	}
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
		return nil, finishJobOutput(output, entry, limits), nil
	}
	if input.TimeoutSeconds == 0 {
		output.ElapsedSeconds = time.Since(entry.started).Seconds()
		status, ready, err := readCompletedJob(entry)
		if err != nil {
			return nil, output, err
		}
		if ready {
			output = pendingJobOutput(output, entry, status)
		}
		return nil, output, nil
	}

	timeout, clamped := resolveWaitTimeout(input.TimeoutSeconds)
	output.EffectiveTimeoutSeconds = int(timeout.Seconds())
	output.TimeoutClamped = clamped
	waitCtx, stopWait := context.WithTimeout(ctx, timeout)
	defer stopWait()
	reporter := newProgressReporter(ctx, request, timeout, "waiting for the command to finish")
	defer reporter.stop()

	var collectionDone chan struct{}
	for {
		if input.TimeoutSeconds > 0 && waitCtx.Err() != nil {
			if ctx.Err() != nil {
				return nil, output, ctx.Err()
			}
			if latest, found := owned.find(input.JobID); !found {
				return nil, getJobOutput{}, unknownJob(input.JobID)
			} else if latest.finished {
				return nil, finishJobOutput(output, latest, limits), nil
			}
			output.ElapsedSeconds = time.Since(entry.started).Seconds()
			return nil, output, nil
		}
		entry, leader, done, found := owned.beginCollection(input.JobID)
		if !found {
			return nil, getJobOutput{}, unknownJob(input.JobID)
		}
		if entry.finished {
			return nil, finishJobOutput(output, entry, limits), nil
		}
		if leader {
			collectionDone = done
			break
		}
		if latest, found := owned.find(input.JobID); !found {
			return nil, getJobOutput{}, unknownJob(input.JobID)
		} else if latest.finished {
			return nil, finishJobOutput(output, latest, limits), nil
		}
		if input.TimeoutSeconds == 0 {
			output.ElapsedSeconds = time.Since(entry.started).Seconds()
			return nil, output, nil
		}
		select {
		case <-done:
			continue
		case <-waitCtx.Done():
			continue
		}
	}
	defer owned.endCollection(entry.id, collectionDone)

	output.ElapsedSeconds = time.Since(entry.started).Seconds()
	status, ready, readErr := readCompletedJob(entry)
	if readErr == nil && !ready && input.TimeoutSeconds > 0 {
		status, ready, readErr = waitForCompletedJob(waitCtx, entry)
	}
	if readErr != nil {
		if t.runtime.isTerminalError(readErr) || !isOwnWaitDeadline(ctx, waitCtx, readErr) {
			t.runtime.observe(readErr)
			return nil, output, readErr
		}
	}
	if ctx.Err() != nil {
		return nil, output, ctx.Err()
	}
	if input.TimeoutSeconds > 0 && waitCtx.Err() != nil {
		if ready {
			return nil, pendingJobOutput(output, entry, status), nil
		}
		output.ElapsedSeconds = time.Since(entry.started).Seconds()
		return nil, output, nil
	}
	if !ready {
		if latest, found := owned.find(input.JobID); !found {
			return nil, getJobOutput{}, unknownJob(input.JobID)
		} else if latest.finished {
			return nil, finishJobOutput(output, latest, limits), nil
		}
		if pane, paneErr := t.tmux(waitCtx).Pane(waitCtx, entry.paneID); paneErr == nil {
			output.Running, _ = pane.Formats().PaneCurrentCommand()
		} else if ctx.Err() != nil {
			return nil, output, ctx.Err()
		} else if input.TimeoutSeconds > 0 && waitCtx.Err() != nil &&
			isContextError(paneErr) {
			output.ElapsedSeconds = time.Since(entry.started).Seconds()
			return nil, output, nil
		} else if t.runtime.isTerminalError(paneErr) || isContextError(paneErr) {
			return nil, output, paneErr
		}
		return nil, output, nil
	}

	// Collect at the ceiling so later calls may request more than this one.
	var collected runCommandOutput
	if pane, paneErr := t.tmux(waitCtx).Pane(waitCtx, entry.paneID); paneErr == nil {
		if outputErr := t.attachCommandOutput(waitCtx, pane, entry.openedAt, entry.closedAt,
			bounds{lines: ceilingMaxLines, bytes: ceilingMaxBytes}, &collected); outputErr != nil {
			if ctx.Err() == nil && input.TimeoutSeconds > 0 && waitCtx.Err() != nil &&
				isContextError(outputErr) {
				return nil, pendingJobOutput(output, entry, status), nil
			}
			return nil, output, outputErr
		}
	} else if ctx.Err() != nil {
		return nil, output, ctx.Err()
	} else if input.TimeoutSeconds > 0 && waitCtx.Err() != nil &&
		isContextError(paneErr) {
		return nil, pendingJobOutput(output, entry, status), nil
	} else if t.runtime.isTerminalError(paneErr) || isContextError(paneErr) {
		return nil, output, paneErr
	} else {
		collected.OutputUnavailable = paneErr.Error()
	}
	settled, ok := owned.settle(
		entry.id,
		status,
		collected.Output,
		collected.truncation,
		collected.OutputUnavailable,
		collected.LinesMissed,
		time.Now(),
	)
	if !ok {
		return nil, getJobOutput{}, unknownJob(input.JobID)
	}
	return nil, finishJobOutput(output, settled, limits), nil
}

func pendingJobOutput(output getJobOutput, entry job, status int) getJobOutput {
	output.Finished = true
	output.CollectionPending = true
	output.ExitStatus = &status
	output.ElapsedSeconds = time.Since(entry.started).Seconds()
	return output
}

func finishJobOutput(output getJobOutput, entry job, limits bounds) getJobOutput {
	status := entry.exitStatus
	output.Finished = true
	output.ExitStatus = &status
	output.ElapsedSeconds = entry.ended.Sub(entry.started).Seconds()
	output.Output, output.truncation = limits.apply(entry.output)
	output.truncation = addTruncation(output.truncation, entry.atCeiling)
	output.OutputUnavailable = entry.outputUnavailable
	output.LinesMissed = entry.linesMissed
	return output
}

func waitForCompletedJob(
	waitCtx context.Context,
	entry job,
) (status int, ready bool, err error) {
	ticker := time.NewTicker(jobCompletionPollInterval)
	defer ticker.Stop()
	for {
		status, ready, err = readCompletedJob(entry)
		if err != nil || ready {
			return status, ready, err
		}
		select {
		case <-ticker.C:
		case <-waitCtx.Done():
			status, ready, err = readCompletedJob(entry)
			if err != nil || ready {
				return status, ready, err
			}
			return 0, false, waitCtx.Err()
		}
	}
}

// readCompletedJob treats the closing cursor mark as the commit record for a
// command result. The wrapper publishes it after the status, so status alone
// is never enough to settle a detached job.
func readCompletedJob(entry job) (int, bool, error) {
	recorded, err := os.ReadFile(entry.statusAt)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read command status: %w", err)
	}
	if _, err := readMark(entry.closedAt); errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	} else if err != nil {
		return 0, false, err
	}
	status, err := strconv.Atoi(strings.TrimSpace(string(recorded)))
	if err != nil {
		return 0, false, fmt.Errorf("unreadable exit status %q", recorded)
	}
	return status, true, nil
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
