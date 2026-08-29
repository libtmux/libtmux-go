package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

const controlRegistrationPoll = 10 * time.Millisecond

type controlClientProfile uint8

const (
	controlCommands controlClientProfile = iota
	controlNotificationsNoPaneOutput
	controlNotificationsFull
)

func (profile controlClientProfile) retainsNotifications() bool {
	return profile == controlNotificationsNoPaneOutput ||
		profile == controlNotificationsFull
}

func (profile controlClientProfile) receivesPaneOutput() bool {
	return profile == controlNotificationsFull
}

// OpenControl starts a control-mode client attached to session. The startup
// context bounds process start, attach framing, and client registration but
// does not own the returned client's lifetime. Session must belong to the same
// configured server selector.
func (s Server) OpenControl(
	ctx context.Context,
	session Session,
) (*ControlClient, error) {
	return s.openControl(ctx, session, controlNotificationsFull)
}

func (s Server) openControl(
	ctx context.Context,
	session Session,
	profile controlClientProfile,
) (*ControlClient, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := s.stateForUse(); err != nil {
		return nil, err
	}
	if session.ID() == "" {
		return nil, invalidServerCommandRequest(
			"control", "Session", "", "must have a materialized session ID",
		)
	}
	if !session.Server().Equal(s) {
		return nil, invalidServerCommandRequest(
			"control", "Session", "[redacted]", "belongs to a different server selector",
		)
	}
	if session.server.daemon != nil {
		if s.daemon != nil && !sameSnapshotIdentity(*s.daemon, *session.server.daemon) {
			return nil, ErrDaemonReplaced
		}
		s = s.withDaemon(*session.server.daemon)
	}
	if s.connection != nil {
		return nil, s.connection.routeError(ctx, commandProcess)
	}
	version, err := s.Version(ctx)
	if err != nil {
		return nil, err
	}

	attach := []string{"attach-session"}
	flags := (controlDialect{version: version}).clientFlags(profile)
	if len(flags) > 0 {
		attach = append(attach, "-f", strings.Join(flags, ","))
	}
	attach = append(attach, "-t", session.ID().String())
	attach, guard, err := s.guardCommand(attach, false)
	if err != nil {
		return nil, err
	}
	attach = append([]string{"-C"}, attach...)
	return s.startControl(
		ctx,
		session,
		profile,
		attach,
		func(client *ControlClient) error {
			return client.acceptAttach(ctx, guard)
		},
	)
}

// startControl owns process setup shared by existing-session attachment and
// first-session creation. accept consumes the startup command's frames and may
// replace the client's initially unknown server and session provenance.
func (s Server) startControl(
	ctx context.Context,
	session Session,
	profile controlClientProfile,
	startup []string,
	accept func(*ControlClient) error,
) (*ControlClient, error) {
	state, err := s.stateForUse()
	if err != nil {
		return nil, err
	}

	var notifications *controlNotificationQueue
	if profile.retainsNotifications() {
		notifications = newControlNotificationQueue(defaultControlNotificationLimit)
	}
	arguments := s.commandArguments(startup)
	command := exec.Command(state.config.executable, arguments...)
	command.Env = slices.Clone(state.config.processEnvironment)
	command.Dir = state.config.directory
	command.WaitDelay = controlClientStopGrace
	stderr := &controlLockedBuffer{}
	command.Stderr = stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("open control stdin: %w", err),
			notifications.Close(),
		)
	}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("open control stdout: %w", err),
			stdin.Close(),
			notifications.Close(),
		)
	}
	command.Stdout = stdoutWriter
	client := &ControlClient{
		server:        s,
		session:       session,
		command:       command,
		stdin:         stdin,
		stdout:        stdout,
		stderr:        stderr,
		notifications: notifications,
		frames:        make(chan controlFrame, 1),
		requests:      make(chan *controlRequest),
		stopRequests:  make(chan struct{}),
		requestDone:   make(chan struct{}),
		closing:       make(chan struct{}),
		readDone:      make(chan struct{}),
		done:          make(chan struct{}),
		closeDone:     make(chan struct{}),
	}
	if err := command.Start(); err != nil {
		return nil, errors.Join(
			fmt.Errorf("start control process: %w", err),
			stdin.Close(),
			stdout.Close(),
			stdoutWriter.Close(),
			notifications.Close(),
		)
	}
	if err := stdoutWriter.Close(); err != nil {
		killErr := command.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
		return nil, errors.Join(
			fmt.Errorf("close parent control stdout writer: %w", err),
			killErr,
			command.Wait(),
			stdin.Close(),
			stdout.Close(),
			notifications.Close(),
		)
	}
	go client.waitProcess()
	go client.readStream()

	if err := accept(client); err != nil {
		return nil, client.failStartup(err)
	}
	client.stateMu.Lock()
	if client.currentSessionID == "" {
		client.currentSessionID = client.session.ID()
	}
	client.stateMu.Unlock()
	client.dispatching.Store(true)
	if err := client.calibrateReplyFence(ctx); err != nil {
		return nil, client.failStartup(err)
	}
	clientName, err := client.waitForRegistration(ctx)
	if err != nil {
		return nil, client.failStartup(err)
	}
	client.clientName = clientName
	go client.runRequests()
	return client, nil
}

func (c *ControlClient) acceptAttach(
	ctx context.Context,
	guard *daemonCommandGuard,
) error {
	frame, err := c.nextStartupFrame(ctx, guard)
	if err != nil {
		return err
	}
	if frame.failed {
		return fmt.Errorf(
			"control attach failed: %s",
			strings.TrimSpace(string(frame.rawStdout)),
		)
	}
	return nil
}

func (c *ControlClient) nextStartupFrame(
	ctx context.Context,
	guard *daemonCommandGuard,
) (controlFrame, error) {
	if guard != nil {
		// if-shell completes before the startup command it schedules.
		frame, err := c.nextFrame(ctx)
		if err != nil {
			return controlFrame{}, err
		}
		if frame.failed &&
			string(frame.rawStdout) == "unknown command: "+guard.failure+"\n" {
			return controlFrame{}, ErrDaemonReplaced
		}
		if frame.failed {
			return controlFrame{}, fmt.Errorf(
				"control startup guard failed: %s",
				strings.TrimSpace(string(frame.rawStdout)),
			)
		}
	}
	return c.nextFrame(ctx)
}

// Use a printable separator because tmux rewrites control characters for clients
// without a UTF-8 locale. Neither numeric PIDs nor client names contain it.
const registrationFormat = "#{client_pid}" + string(formatFieldSeparator) + "#{client_name}"

func (c *ControlClient) waitForRegistration(ctx context.Context) (ClientName, error) {
	pid := strconv.Itoa(c.command.Process.Pid)
	ticker := time.NewTicker(controlRegistrationPoll)
	defer ticker.Stop()
	for {
		result, err := c.server.Cmd(ctx, "list-clients", "-F", registrationFormat)
		if err != nil {
			return "", err
		}
		if result.ExitCode == 0 {
			for _, line := range result.Stdout {
				clientPID, clientName, ok := strings.Cut(line, string(formatFieldSeparator))
				if ok && clientPID == pid && strings.TrimSpace(clientName) != "" {
					return ClientName(strings.TrimSpace(clientName)), nil
				}
			}
		}
		select {
		case <-c.done:
			return "", c.registrationExitError()
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *ControlClient) failStartup(err error) error {
	return errors.Join(err, c.Close())
}
