package mcp

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

//go:embed minimal.conf
var shippedMinimalConfig []byte

// Content equality cannot prove provenance: an operator may supply the same bytes.
var materializedMinimalConfigs sync.Map

// MaterializeMinimalConfig writes the embedded dedicated-server configuration
// for tmux's -f option. The caller keeps it until the MCP process stops.
func MaterializeMinimalConfig() (string, func() error, error) {
	file, err := os.CreateTemp("", "libtmux-mcp-minimal-*.conf")
	if err != nil {
		return "", nil, fmt.Errorf("create minimal tmux config: %w", err)
	}
	path := file.Name()
	cleanup := func() error {
		materializedMinimalConfigs.Delete(filepath.Clean(path))
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = cleanup()
		return "", nil, fmt.Errorf("protect minimal tmux config: %w", err)
	}
	if _, err := file.Write(shippedMinimalConfig); err != nil {
		_ = file.Close()
		_ = cleanup()
		return "", nil, fmt.Errorf("write minimal tmux config: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = cleanup()
		return "", nil, fmt.Errorf("close minimal tmux config: %w", err)
	}
	materializedMinimalConfigs.Store(filepath.Clean(path), struct{}{})
	return path, cleanup, nil
}

func isShippedMinimalConfig(path string) (bool, error) {
	if path == "" || path == os.DevNull {
		return false, nil
	}
	if _, owned := materializedMinimalConfigs.Load(filepath.Clean(path)); !owned {
		return false, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read selected tmux config: %w", err)
	}
	return bytes.Equal(contents, shippedMinimalConfig), nil
}
