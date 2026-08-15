package tmuxtest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"sync"
	"testing"
	"time"
)

// PTYProcess is a test subprocess that owns an isolated controlling terminal
// and a memory-backed transcript. It comes from [StartPTYProcess]. Output may
// be read concurrently through [PTYProcess.Output]; writes are serialized.
type PTYProcess struct {
	command *exec.Cmd
	master  *os.File
	done    chan struct{}
	drained chan struct{}
	write   chan struct{}

	output    ptyProcessOutput
	waitErr   error
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

type ptyProcessOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (o *ptyProcessOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buffer.Write(data)
}

func (o *ptyProcessOutput) snapshot() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return bytes.Clone(o.buffer.Bytes())
}

// StartPTYProcess starts binary with a harness-owned controlling terminal and
// registers bounded cleanup with t. Its start context owns the child lifetime:
// canceling ctx terminates the child. Arguments and environment are copied. A
// nil environment inherits the current process environment; a non-nil empty
// environment remains empty. binary runs directly without a shell. The PTY
// backend is supported on Linux only; unsupported platforms call
// [testing.TB.Fatal]. Setup failures also call [testing.TB.Fatal].
func StartPTYProcess(
	ctx context.Context,
	t testing.TB,
	binary string,
	arguments []string,
	environment []string,
) *PTYProcess {
	t.Helper()
	command := exec.CommandContext(ctx, binary, slices.Clone(arguments)...)
	command.Env = slices.Clone(environment)
	master, slave, err := preparePTYProcessCommand(command)
	if err != nil {
		t.Fatal(harnessFailure("open PTY", err))
	}
	process := &PTYProcess{
		command:   command,
		master:    master,
		done:      make(chan struct{}),
		drained:   make(chan struct{}),
		write:     make(chan struct{}, 1),
		closeDone: make(chan struct{}),
	}
	process.write <- struct{}{}
	if err := command.Start(); err != nil {
		_ = slave.Close()
		_ = master.Close()
		t.Fatal(harnessFailure("start PTY process", err))
	}
	if err := slave.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = master.Close()
		t.Fatal(harnessFailure("close parent PTY slave", err))
	}
	go func() {
		process.waitErr = command.Wait()
		close(process.done)
	}()
	go func() {
		_, _ = io.Copy(&process.output, master)
		close(process.drained)
	}()
	t.Cleanup(func() {
		if err := process.Close(); err != nil {
			t.Error(harnessFailure("cleanup PTY process", err))
		}
	})
	return process
}

// Done is closed when the subprocess exits. It can close before the terminal
// reader has added the final bytes to [PTYProcess.Output].
func (p *PTYProcess) Done() <-chan struct{} { return p.done }

// Write sends terminal input to the subprocess within ctx. Writes are
// serialized and can be partial. A waiting caller may cancel before acquiring
// the writer, and an active write observes ctx cancellation.
func (p *PTYProcess) Write(ctx context.Context, input []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	select {
	case <-p.write:
		defer func() { p.write <- struct{}{} }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	deadline, _ := ctx.Deadline()
	if err := p.master.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	interruptDone := make(chan struct{})
	stopInterrupt := context.AfterFunc(ctx, func() {
		_ = p.master.SetWriteDeadline(time.Now())
		close(interruptDone)
	})

	n, writeErr := p.master.Write(input)
	if !stopInterrupt() {
		<-interruptDone
	}
	resetErr := p.master.SetWriteDeadline(time.Time{})
	if ctxErr := ctx.Err(); ctxErr != nil && writeErr != nil {
		return n, ctxErr
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline &&
		errors.Is(writeErr, os.ErrDeadlineExceeded) {
		return n, context.DeadlineExceeded
	}
	return n, errors.Join(writeErr, resetErr)
}

// Wait waits for process completion and the final output drain, or ctx
// cancellation. A successful Wait guarantees that [PTYProcess.Output] contains
// the completed transcript.
func (p *PTYProcess) Wait(ctx context.Context) error {
	select {
	case <-p.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-p.drained:
		return p.waitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Output returns a fresh copy of terminal output collected so far. A successful
// [PTYProcess.Wait] guarantees that the returned transcript includes the final
// drain.
func (p *PTYProcess) Output() []byte { return p.output.snapshot() }

// Close stops the subprocess if needed and releases the PTY master. It is safe
// to call concurrently and more than once.
func (p *PTYProcess) Close() error {
	ctx, cancel := context.WithTimeout(
		context.Background(), 2*cleanupTimeout+2*controlStopGrace,
	)
	defer cancel()
	return p.CloseContext(ctx)
}

// CloseContext starts an idempotent shutdown and waits within ctx. Concurrent
// calls join the same shutdown. If ctx ends after shutdown starts, shutdown
// continues and a later call may wait for completion; an already-ended ctx does
// not start shutdown.
func (p *PTYProcess) CloseContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.closeOnce.Do(func() { go p.close() })
	select {
	case <-p.closeDone:
		return p.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *PTYProcess) close() {
	defer close(p.closeDone)
	select {
	case <-p.done:
	case <-time.After(controlStopGrace):
		killErr := p.command.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
		p.closeErr = errors.Join(p.closeErr, killErr)
		select {
		case <-p.done:
		case <-time.After(cleanupTimeout):
			p.closeErr = errors.Join(
				p.closeErr, errors.New("PTY process did not exit after kill"),
			)
		}
	}
	p.closeErr = errors.Join(p.closeErr, p.master.Close())
	select {
	case <-p.drained:
	case <-time.After(cleanupTimeout):
		p.closeErr = errors.Join(
			p.closeErr, errors.New("PTY output did not drain after close"),
		)
	}
}
