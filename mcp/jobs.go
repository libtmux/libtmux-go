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

// A command can outlive the call that started it.
//
// run_command waits, which is right when the answer is the point: a caller
// that needs an exit status before it can do anything else should not have to
// invent a polling loop to get one. It is wrong when the command is a build
// and the caller has reading to do meanwhile. Waiting is then not the cost of
// the answer but the cost of asking, and it is paid in the caller's turn,
// which is the one thing a model cannot get more of.
//
// So the wait is optional. Detaching returns as soon as the command is typed,
// with a handle; get_job collects it later, either at once or waiting a
// bounded while. Nothing about the command changes -- the same wrapper records
// the same exit status against the same tmux channel -- so a detached run and
// an attached one answer identically, and the difference is only who waits.
//
// The state a handle needs is small and lives here rather than in the handle,
// because the files it names are this process's own. A handle that carried
// them would be a caller-supplied path this server later reads.
//
// Collecting is idempotent. The first read that finds a status keeps it and
// releases the files; every later read is answered from what was kept. A
// handle that stopped answering once it had been used would punish the
// ordinary thing a caller does -- ask again -- with an error that reads like
// the command was lost, and asking twice is how a caller checks on something.

// jobsRetained bounds how many uncollected commands are kept. A caller that
// detaches and never collects would otherwise hold a temporary directory per
// command for the life of the process; the oldest is dropped instead, which
// costs that caller its output and nothing else.
const jobsRetained = 32

// job is one command left running in a pane.
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

	// finished, exitStatus, and output are what the first successful read
	// kept, so that later reads answer without the files it released.
	finished   bool
	exitStatus int
	output     []string
	// atCeiling is what the ceiling dropped before the output was kept. A
	// later read applies the caller's own bounds to what is left, and would
	// otherwise report only that second cut, understating the loss.
	atCeiling truncation
	ended     time.Time
}

// jobs holds the commands that were detached and not yet collected.
type jobs struct {
	mutex sync.Mutex
	byID  map[string]*job
	// order is the ids oldest first, which is the order they are dropped in.
	order []string
}

func newJobs() *jobs {
	return &jobs{byID: map[string]*job{}}
}

// keep records a detached command, dropping the oldest if there are too many.
func (j *jobs) keep(entry *job) {
	j.mutex.Lock()
	defer j.mutex.Unlock()
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
}

// find reports the command a handle names.
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

// settle records how a command ended and releases the files it used, keeping
// the answer so that later reads do not need them.
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

// close drops everything, which the server does when it stops.
func (j *jobs) close() {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	for id, entry := range j.byID {
		delete(j.byID, id)
		_ = os.RemoveAll(entry.directory)
	}
	j.order = nil
}

// unknownJob explains a handle this server is not holding, distinguishing the
// two reasons rather than asserting the more likely one.
//
// A handle names the process that issued it, so a handle from a previous run
// is recognisable. Saying that newer commands crowded it out, when in fact the
// server restarted, sends a caller looking for commands it never started.
func unknownJob(id string) error {
	if issuer, ok := jobIssuer(id); ok && issuer != os.Getpid() {
		return fmt.Errorf(
			"%q was issued by a different run of this server, which has since "+
				"restarted; a handle does not outlive the process that made it, "+
				"and the command it named may still be running in its pane", id)
	}
	return fmt.Errorf(
		"%q is not a job this server is holding: only the last %d are kept, "+
			"and older ones are dropped as newer commands are started",
		id, jobsRetained)
}

// jobIssuer reads the process id out of a handle this server made.
func jobIssuer(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, "libtmux-mcp-")
	if !ok {
		return 0, false
	}
	issuer, _, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, false
	}
	pid, err := strconv.Atoi(issuer)
	if err != nil {
		return 0, false
	}
	return pid, true
}

// getJobInput collects a command that was left running.
type getJobInput struct {
	// JobID is the handle run_command returned when it detached.
	JobID string `json:"jobId" jsonschema:"the handle a detached run_command returned"`
	// TimeoutSeconds waits that long for the command to finish. Zero asks
	// whether it has finished and returns either way, which is what a caller
	// checking on it between other work wants.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty" jsonschema:"wait up to this long for the command to finish; zero reports whether it has and returns at once"`
	// MaxLines caps the returned output, keeping the last lines.
	MaxLines int `json:"maxLines,omitempty" jsonschema:"how many lines of output to return at most, keeping the last ones"`
	// MaxBytes caps the returned output's size, keeping the last lines.
	MaxBytes int `json:"maxBytes,omitempty" jsonschema:"how many bytes to return at most, keeping the last lines"`
}

// getJobOutput reports how a detached command is doing, or how it ended.
type getJobOutput struct {
	// JobID is the handle asked about.
	JobID string `json:"jobId"`
	// PaneID is the pane the command runs in.
	PaneID string `json:"paneId"`
	// Command is what was run, so a caller holding several handles does not
	// have to remember which is which.
	Command string `json:"command"`
	// Finished reports whether the command has ended.
	Finished bool `json:"finished"`
	// ExitStatus is its status, present only once it has ended.
	ExitStatus *int `json:"exitStatus,omitempty"`
	// ElapsedSeconds is how long it has been running, or how long it ran.
	ElapsedSeconds float64 `json:"elapsedSeconds"`
	// Running is what the pane is running now, reported while the command has
	// not finished. A shell here means the command ended without recording a
	// status, which is what interrupting it looks like.
	Running string `json:"running,omitempty"`
	// Output is what the command wrote, read from where the pane stood when
	// it started. It is reported once the command has finished.
	Output []string `json:"output,omitempty"`
	// EffectiveTimeoutSeconds is the wait this call ran, reported only when it
	// was asked to wait at all.
	EffectiveTimeoutSeconds int `json:"effectiveTimeoutSeconds,omitempty"`
	// TimeoutClamped reports that the server's ceiling shortened the wait.
	TimeoutClamped bool `json:"timeoutClamped,omitempty"`
	// truncation reports what the bounds dropped from Output.
	truncation
}

// getJob collects a detached command, waiting a bounded while if asked.
//
// The first read that finds a status keeps it and releases the files behind
// it, so a later read is answered without them and asking twice costs a map
// lookup.
func (t *tools) getJob(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input getJobInput,
) (*mcp.CallToolResult, getJobOutput, error) {
	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, getJobOutput{}, err
	}
	entry, ok := t.jobs.find(input.JobID)
	if !ok {
		return nil, getJobOutput{}, unknownJob(input.JobID)
	}
	output := getJobOutput{
		JobID:   entry.id,
		PaneID:  entry.paneID.String(),
		Command: entry.command,
	}

	// Already ended and already read. Nothing here touches tmux or the
	// filesystem, so asking again costs a map lookup.
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
		// A handle with no engine, because a command that blocks inside tmux
		// holds a pooled connection for as long as it blocks.
		waiter := t.tmux().WithEngine(t.tmux().SubprocessEngine())
		_ = waiter.WaitFor(waitCtx, tmux.WaitForRequest{Channel: entry.channel})
	}

	output.ElapsedSeconds = time.Since(entry.started).Seconds()
	recorded, readErr := os.ReadFile(entry.statusAt)
	if readErr != nil {
		// No status yet is the ordinary unfinished case rather than a fault,
		// so the reply says what the pane is doing instead of failing.
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

	// Read at the ceiling rather than at this call's bounds, because what is
	// kept has to answer a later call that asks for more. The caller's bounds
	// are applied to the reply below.
	var collected runCommandOutput
	if pane, paneErr := t.tmux().Pane(ctx, entry.paneID); paneErr == nil {
		attachCommandOutput(ctx, pane, entry.openedAt, entry.closedAt,
			bounds{lines: ceilingMaxLines, bytes: ceilingMaxBytes}, &collected)
	}
	ended := time.Now()
	t.jobs.settle(entry.id, status, collected.Output, collected.truncation, ended)
	output.ElapsedSeconds = ended.Sub(entry.started).Seconds()
	output.Output, output.truncation = limits.apply(collected.Output)
	output.truncation = addTruncation(output.truncation, collected.truncation)
	return nil, output, nil
}

// addTruncation reports two cuts as one, so a caller reading a job whose
// output was already bounded once is told the whole loss rather than the
// second half of it.
func addTruncation(caller, earlier truncation) truncation {
	caller.TruncatedLines += earlier.TruncatedLines
	caller.TruncatedBytes += earlier.TruncatedBytes
	// Derived from the totals, as bounds.apply derives it, rather than by
	// combining two flags that were each derived the same way.
	caller.Truncated = caller.TruncatedLines > 0 || caller.TruncatedBytes > 0
	return caller
}
