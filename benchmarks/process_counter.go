package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	countFileEnvironment = "LIBTMUX_BENCH_COUNT_FILE"
	realTmuxEnvironment  = "LIBTMUX_BENCH_REAL_TMUX"
)

var errProcessCounterUnsupported = errors.New(
	"benchmark process counter requires a POSIX shell",
)

const processCounterProxy = `#!/bin/sh
count_file=${LIBTMUX_BENCH_COUNT_FILE:?missing count file}
real_tmux=${LIBTMUX_BENCH_REAL_TMUX:?missing real tmux executable}
printf '1\n' >> "$count_file" || exit 125
unset LIBTMUX_BENCH_COUNT_FILE LIBTMUX_BENCH_REAL_TMUX
exec "$real_tmux" "$@"
`

type processCounter struct {
	executable string
	proxy      string
	records    string
}

func newProcessCounter(directory string) (*processCounter, error) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		return nil, fmt.Errorf(
			"%w: %s has no /bin/sh",
			errProcessCounterUnsupported,
			runtime.GOOS,
		)
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		return nil, fmt.Errorf("%w: %w", errProcessCounterUnsupported, err)
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("make proxy directory absolute: %w", err)
	}
	executable, err := exec.LookPath("tmux")
	if err != nil {
		return nil, fmt.Errorf("resolve real tmux executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf("make real tmux executable absolute: %w", err)
	}

	counter := &processCounter{
		executable: filepath.Clean(executable),
		proxy:      filepath.Join(directory, "tmux-counting-proxy"),
		records:    filepath.Join(directory, "tmux-invocations"),
	}
	if err := os.WriteFile(counter.records, nil, 0o600); err != nil {
		return nil, fmt.Errorf("create tmux invocation record: %w", err)
	}
	if err := os.WriteFile(counter.proxy, []byte(processCounterProxy), 0o700); err != nil {
		return nil, fmt.Errorf("write tmux counting proxy: %w", err)
	}
	return counter, nil
}

func (c *processCounter) environment(base []string) []string {
	environment := make([]string, 0, len(base)+2)
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if name == countFileEnvironment || name == realTmuxEnvironment {
			continue
		}
		environment = append(environment, entry)
	}
	return append(
		environment,
		countFileEnvironment+"="+c.records,
		realTmuxEnvironment+"="+c.executable,
	)
}

func (c *processCounter) reset() error {
	if err := os.WriteFile(c.records, nil, 0o600); err != nil {
		return fmt.Errorf("reset tmux invocation record: %w", err)
	}
	return nil
}

func (c *processCounter) total() (int, error) {
	records, err := os.ReadFile(c.records)
	if err != nil {
		return 0, fmt.Errorf("read tmux invocation record: %w", err)
	}
	if len(records)%2 != 0 {
		return 0, errors.New("read tmux invocation record: truncated record")
	}
	for index := 0; index < len(records); index += 2 {
		if records[index] != '1' || records[index+1] != '\n' {
			return 0, errors.New("read tmux invocation record: invalid record")
		}
	}
	return len(records) / 2, nil
}
