package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/mattn/go-shellwords"
	"github.com/spf13/cobra"
)

const captureLimit = 1024 * 1024

type processResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Status    int    `json:"child_status"`
	Truncated bool   `json:"truncated"`
	Encoding  string `json:"encoding"`
}
type captureWriter struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	truncated bool
	stream    string
	r         *invocation
	emit      bool
	cancel    context.CancelFunc
	log       io.Writer
	pending   []byte
	writeErr  error
}

func (w *captureWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(data)
	remaining := captureLimit - w.buffer.Len()
	if remaining < n {
		w.truncated = true
	}
	if remaining > 0 {
		_, _ = w.buffer.Write(data[:min(remaining, n)])
	}
	if w.log != nil {
		if _, err := w.log.Write(data); err != nil {
			w.writeErr = err
			w.cancel()
			return 0, err
		}
	}
	if w.emit {
		if err := w.emitBytes(data, false); err != nil {
			w.writeErr = err
			w.cancel()
			return 0, err
		}
	}
	return n, nil
}

func (w *captureWriter) emitBytes(data []byte, final bool) error {
	data = append(w.pending, data...)
	var decoded strings.Builder
	for len(data) > 0 {
		if !final && !utf8.FullRune(data) {
			break
		}
		value, size := utf8.DecodeRune(data)
		decoded.WriteRune(value)
		data = data[size:]
	}
	w.pending = append(w.pending[:0], data...)
	text := decoded.String()
	if text != "" {
		if w.r.ndjson {
			if err := w.r.event("script-output", map[string]any{"stream": w.stream, "text": text, "encoding": "utf-8-replacement"}); err != nil {
				return err
			}
		} else if !w.r.machine() {
			if w.r.progress != nil {
				return w.r.progress.script(w.stream, text)
			}
			writer := w.r.err
			if w.stream == "stdout" {
				writer = w.r.out
			}
			if _, err := io.WriteString(writer, text); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *invocation) process(argv []string, cwd string, input io.Reader, emit bool, log io.Writer) (processResult, error) {
	result := processResult{Encoding: "utf-8-replacement"}
	if len(argv) == 0 {
		return result, errors.New("empty child command")
	}
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	superviseProcess(cmd)
	cmd.Dir = cwd
	cmd.Stdin = input
	cmd.WaitDelay = 2 * time.Second
	out := &captureWriter{r: r, stream: "stdout", emit: emit, cancel: cancel, log: log}
	diagnostic := &captureWriter{r: r, stream: "stderr", emit: emit, cancel: cancel, log: log}
	cmd.Stdout, cmd.Stderr = out, diagnostic
	err := cmd.Run()
	if emit {
		err = errors.Join(err, out.emitBytes(nil, true), diagnostic.emitBytes(nil, true))
	}
	result.Stdout = strings.ToValidUTF8(out.buffer.String(), "\uFFFD")
	result.Stderr = strings.ToValidUTF8(diagnostic.buffer.String(), "\uFFFD")
	result.Truncated = out.truncated || diagnostic.truncated
	if writeErr := errors.Join(out.writeErr, diagnostic.writeErr); writeErr != nil {
		return result, writeErr
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.Status = exit.ExitCode()
			if result.Status < 0 {
				result.Status = 130
			}
		} else {
			return result, err
		}
	}
	return result, nil
}

func pythonExecutable() string {
	if path := os.Getenv("TMUX_WORKSPACE_PYTHON"); path != "" {
		return path
	}
	return "python3"
}

func (r *invocation) checkPython(tmuxpRequired bool) error {
	script := "import sys\nif sys.version_info < (3, 10) or sys.version_info.major != 3: raise RuntimeError('Python 3.10 or newer required')\n"
	if tmuxpRequired {
		script += "from importlib.metadata import version\nif version('tmuxp') != '" + referenceVersion + "': raise RuntimeError('tmuxp " + referenceVersion + " required')\n"
	}
	result, err := r.process([]string{pythonExecutable(), "-c", script}, "", nil, false, nil)
	if err != nil {
		return &failure{"compatibility_runtime", fmt.Sprintf("Python compatibility runtime unavailable: %v; set TMUX_WORKSPACE_PYTHON", err), 1}
	}
	if result.Status != 0 {
		return &failure{"compatibility_runtime", "Python compatibility version check failed: " + strings.TrimSpace(result.Stderr), 1}
	}
	return nil
}

func bridgeArgv(args []string) []string {
	return append([]string{pythonExecutable(), "-u", "-c", "from tmuxp.cli import cli; import sys; cli(sys.argv[1:])"}, args...)
}

func appendBridgeArgv(session tmux.Session, o *options, path, name string) ([]string, error) {
	pid, hasPID := session.Formats().PID()
	started, hasStart := session.Formats().StartTime()
	if !hasPID || !hasStart {
		return nil, errors.New("append context is missing the borrowed daemon identity")
	}
	request := map[string]any{
		"path": path, "name": name, "socket": session.Server().SocketPath(),
		"session": session.ID().String(), "identity": fmt.Sprintf("%d:%d", pid, started.Unix()),
	}
	if o.tmuxConfig != "" {
		request["config_file"] = o.tmuxConfig
	}
	if o.colors256 {
		request["colors"] = 256
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	const script = `import json, sys
from pathlib import Path
from libtmux import Server
from tmuxp._internal import config_reader
from tmuxp.cli.load import load_plugins
from tmuxp.cli._colors import Colors, ColorMode
from tmuxp.workspace import loader
from tmuxp.workspace.builder import prepended_sys_path, resolve_builder_class, resolve_builder_paths

request = json.loads(sys.argv[1])
server = Server(socket_path=request['socket'], config_file=request.get('config_file'), colors=request.get('colors'))
def append_target():
    borrowed = server.sessions.get(session_id=request['session'], default=None)
    if borrowed is None:
        raise RuntimeError('append_context: borrowed session no longer exists')
    identity = server.cmd('display-message', '-p', '-t', borrowed.session_id, '#{pid}:#{start_time}')
    if identity.returncode != 0 or identity.stdout != [request['identity']]:
        raise RuntimeError('append_context: borrowed daemon identity changed')
    return borrowed

append_target()
path = Path(request['path'])
config = loader.trickle(loader.expand(config_reader.ConfigReader._from_file(path), cwd=str(path.parent)))
config['session_name'] = request['name']
with prepended_sys_path(resolve_builder_paths(config, path)):
    builder = resolve_builder_class(config)(session_config=config, server=server, plugins=load_plugins(config, colors=Colors(ColorMode.NEVER)))
    borrowed = append_target()
    builder.build(borrowed, append=True)
    for plugin in builder.plugins:
        plugin.before_script(builder.session)
`
	return []string{pythonExecutable(), "-u", "-c", script, string(payload)}, nil
}

func (r *invocation) shell(cmd *cobra.Command, o *options, args []string) error {
	count := 0
	for _, backend := range []string{"best", "pdb", "code", "ptipython", "ptpython", "ipython", "bpython"} {
		if cmd.Flags().Changed(backend) {
			count++
		}
	}
	if count > 1 {
		return usage("Python shell selectors are mutually exclusive")
	}
	if err := r.checkPython(true); err != nil {
		return err
	}
	argv := []string{"--color", "never", "shell"}
	if o.socketPath != "" {
		argv = append(argv, "-S", o.socketPath)
	} else if o.socketName != "" {
		argv = append(argv, "-L", o.socketName)
	}
	argv = append(argv, "--"+o.backend)
	if o.pythonrc {
		argv = append(argv, "--use-pythonrc")
	} else {
		argv = append(argv, "--no-startup")
	}
	if o.vi {
		argv = append(argv, "--use-vi-mode")
	} else {
		argv = append(argv, "--no-vi-mode")
	}
	if cmd.Flags().Changed("python-code") {
		argv = append(argv, "-c", o.code)
	}
	argv = append(argv, args...)
	if !cmd.Flags().Changed("python-code") {
		return r.interactiveProcess(bridgeArgv(argv), "Python shell")
	}
	if err := r.event("started", nil); err != nil {
		return err
	}
	result, err := r.process(bridgeArgv(argv), "", nil, true, nil)
	if err != nil {
		_ = r.event("failed", map[string]any{"message": err.Error()})
		return err
	}
	value := map[string]any{"status": "ok", "stdout": result.Stdout, "stderr": result.Stderr, "child_status": result.Status, "truncated": result.Truncated, "encoding": result.Encoding, "runtime": "tmuxp " + referenceVersion}
	if result.Status != 0 {
		value["status"] = "error"
	}
	if r.ndjson {
		event := "completed"
		if result.Status != 0 {
			event = "failed"
		}
		err = r.event(event, value)
	} else if r.json {
		err = r.result(value)
	}
	if err != nil {
		return err
	}
	if result.Status != 0 {
		return &failure{"child_failed", "Python shell exited unsuccessfully", result.Status}
	}
	return nil
}

func (r *invocation) interactiveProcess(argv []string, label string) error {
	terminalFile, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return &failure{"terminal_required", label + " requires a controlling terminal", 2}
	}
	defer func() { _ = terminalFile.Close() }()
	cmd := exec.CommandContext(r.ctx, argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = terminalFile, terminalFile, terminalFile
	err = cmd.Run()
	status := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return err
		}
		status = exit.ExitCode()
	}
	if r.machine() {
		if err := r.result(map[string]any{"status": statusWord(status), "child_status": status}); err != nil {
			return err
		}
	}
	if status != 0 {
		return &failure{"child_failed", label + " exited unsuccessfully", status}
	}
	return nil
}

func statusWord(status int) string {
	if status == 0 {
		return "ok"
	}
	return "error"
}

func (r *invocation) edit(_ *cobra.Command, _ *options, args []string) error {
	path, err := resolveFile(args[0], "")
	if err != nil {
		return err
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	argv, err := shellwords.Parse(editor)
	if err != nil || len(argv) == 0 {
		return usage("invalid EDITOR argument quoting")
	}
	argv = append(argv, path)
	var result processResult
	if r.machine() {
		result, err = r.process(argv, "", nil, false, nil)
	} else {
		cmd := exec.CommandContext(r.ctx, argv[0], argv[1:]...)
		cmd.Stdin = r.in
		cmd.Stdout = r.out
		cmd.Stderr = r.err
		err = cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.Status = exit.ExitCode()
			err = nil
		}
	}
	if err != nil {
		return err
	}
	if r.machine() {
		if err := r.result(map[string]any{"status": statusWord(result.Status), "path": privatePath(path), "child_status": result.Status, "stdout": result.Stdout, "stderr": result.Stderr, "truncated": result.Truncated}); err != nil {
			return err
		}
	}
	if result.Status != 0 {
		return &failure{"editor_failed", "editor exited unsuccessfully", result.Status}
	}
	return nil
}

func (r *invocation) debugInfo(_ *cobra.Command, _ *options, _ []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	dirs, _ := globalDirectories()
	result := map[string]any{"port": "go", "go_version": runtime.Version(), "platform": runtime.GOOS, "architecture": runtime.GOARCH, "tmuxp_compatibility": referenceVersion, "cwd": privatePath(cwd), "global_workspace_dirs": dirs, "python_bridge": privatePath(pythonExecutable()), "tmux_available": false}
	server, err := tmux.NewServer(tmux.ServerOptions{})
	if err != nil {
		result["tmux_error"] = privatePath(err.Error())
	} else {
		result["tmux_executable"] = privatePath(server.Executable())
		result["socket_path"] = privatePath(server.SocketPath())
		version, e := server.Version(r.ctx)
		if e != nil {
			result["tmux_error"] = e.Error()
		} else {
			result["tmux_available"] = true
			result["tmux_version"] = version.String()
		}
	}
	if r.machine() {
		return r.encode(result)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(r.out, r.style("heading", "Runtime diagnostics")+"\n"+string(data))
	return err
}
