package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"sync"
	"time"
)

type diagnosticLog struct {
	mu      sync.Mutex
	handler slog.Handler
	file    io.Closer
	err     error
}

func newDiagnosticLog(file io.WriteCloser, level slog.Level) *diagnosticLog {
	return &diagnosticLog{handler: slog.NewJSONHandler(file, &slog.HandlerOptions{Level: level}), file: file}
}

func (d *diagnosticLog) record(ctx context.Context, level slog.Level, command, event string, data map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil || d.file == nil || !d.handler.Enabled(ctx, level) {
		return
	}
	if _, ok := data["scripts"]; ok {
		data = maps.Clone(data)
		delete(data, "scripts")
	}
	record := slog.NewRecord(time.Now(), level, event, 0)
	record.AddAttrs(slog.String("command", command), slog.Any("data", data))
	d.err = d.handler.Handle(ctx, record)
}

func (d *diagnosticLog) close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file == nil {
		return nil
	}
	if err := d.file.Close(); d.err == nil {
		d.err = err
	}
	d.file = nil
	return d.err
}

func (r *invocation) diagnosticLevel() slog.Level {
	switch r.logLevel {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "error":
		return slog.LevelError
	case "critical":
		return slog.LevelError + 4
	default:
		return slog.LevelWarn
	}
}

func (r *invocation) logEvent(event string, data map[string]any) {
	if r.log == nil {
		return
	}
	level := slog.LevelInfo
	switch event {
	case "script-output":
		level = slog.LevelDebug
	case "warning":
		level = slog.LevelWarn
		if data["code"] == "workspace_failed" {
			level = slog.LevelError
		}
	case "failed", "command-failed":
		level = slog.LevelError
	}
	r.log.record(r.ctx, level, r.command, event, data)
}

func (r *invocation) closeLog() {
	if r.log == nil {
		return
	}
	err := r.log.close()
	if err == nil || r.diagnosticLevel() > slog.LevelWarn {
		return
	}
	message := fmt.Sprintf("log file disabled: %v", err)
	if r.machine() {
		if err := json.NewEncoder(r.err).Encode(map[string]string{"code": "log_file_failed", "message": message}); err != nil {
			return
		}
	} else {
		_, _ = fmt.Fprintln(r.err, r.styleFor(r.err, "warning", "warning:")+" "+safeTerminal(message))
	}
}

func openLogFile(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("log destination must be a regular file: %s", privatePath(path))
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	file, err := openAppendFile(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("opened log destination is not a regular file: %s", privatePath(path))
	}
	return file, nil
}
