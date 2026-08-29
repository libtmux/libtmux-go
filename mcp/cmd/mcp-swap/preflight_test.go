package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const preflightHelperEnvironment = "MCP_SWAP_PREFLIGHT_HELPER"

func TestMain(m *testing.M) {
	if scenario := os.Getenv(preflightHelperEnvironment); scenario != "" {
		os.Exit(runPreflightHelper(scenario))
	}
	os.Exit(m.Run())
}

func runPreflightHelper(scenario string) int {
	switch scenario {
	case "hold-stderr":
		time.Sleep(3 * time.Second)
		return 0
	case "stderr-descendant":
		child := exec.Command(os.Args[0])
		child.Env = replaceHelperEnvironment(os.Environ(), "hold-stderr")
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		return 0
	case "stderr-tail":
		fmt.Fprintln(os.Stderr, "BEGIN-OF-DISCARDED-STDERR")
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("x"), 2<<20))
		fmt.Fprintln(os.Stderr, "END-OF-KEPT-STDERR")
		return 0
	case "timeout":
		child := exec.Command(os.Args[0])
		child.Env = replaceHelperEnvironment(os.Environ(), "timeout-descendant")
		if err := child.Start(); err != nil {
			return 2
		}
		time.Sleep(3 * time.Second)
		return 0
	case "timeout-descendant":
		heartbeat, err := os.OpenFile(
			os.Getenv("MCP_SWAP_PREFLIGHT_HEARTBEAT"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND,
			0o600,
		)
		if err != nil {
			return 2
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := heartbeat.Write([]byte("x")); err != nil {
				return 2
			}
			if err := heartbeat.Sync(); err != nil {
				return 2
			}
			time.Sleep(10 * time.Millisecond)
		}
		return 0
	}

	if scenario == "environment" &&
		(os.Getenv("MCP_SWAP_INHERITED") != "kept" ||
			os.Getenv("MCP_SWAP_ENTRY") != "set") {
		fmt.Fprintln(os.Stderr, "preflight environment was not merged")
		return 2
	}

	type request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"params"`
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return 2
	}
	var initialize request
	if err := json.Unmarshal(scanner.Bytes(), &initialize); err != nil ||
		initialize.Method != "initialize" {
		return 2
	}

	if scenario == "malformed" {
		if _, err := fmt.Fprintln(os.Stdout, `{this is not JSON}`); err != nil {
			return 2
		}
		return 0
	}
	result := map[string]any{
		"protocolVersion": initialize.Params.ProtocolVersion,
		"capabilities":    map[string]any{},
		"serverInfo": map[string]any{
			"name": "libtmux", "version": "test",
		},
	}
	switch scenario {
	case "null":
		result = nil
	case "missing-capabilities":
		delete(result, "capabilities")
	case "missing-server-info":
		delete(result, "serverInfo")
	case "wrong-name":
		result["serverInfo"] = map[string]any{
			"name": "another-server", "version": "test",
		}
	case "empty-version":
		result["serverInfo"] = map[string]any{
			"name": "libtmux", "version": "",
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      initialize.ID,
		"result":  result,
	}); err != nil {
		return 2
	}
	if scenario == "malformed" || scenario == "null" {
		return 0
	}

	if !scanner.Scan() {
		return 2
	}
	var initialized request
	if err := json.Unmarshal(scanner.Bytes(), &initialized); err != nil ||
		initialized.Method != "notifications/initialized" {
		return 2
	}
	if !scanner.Scan() {
		return 2
	}
	var ping request
	if err := json.Unmarshal(scanner.Bytes(), &ping); err != nil || ping.Method != "ping" {
		return 2
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      ping.ID,
		"result":  map[string]any{},
	}); err != nil {
		return 2
	}

	marker := os.Getenv("MCP_SWAP_PREFLIGHT_MARKER")
	if scenario == "handshake" {
		if err := os.WriteFile(marker, []byte("initialized\nping\n"), 0o600); err != nil {
			return 2
		}
	}
	for scanner.Scan() {
	}
	if scenario == "graceful-eof" {
		time.Sleep(75 * time.Millisecond)
		if err := os.WriteFile(marker, []byte("eof\n"), 0o600); err != nil {
			return 2
		}
	}
	return 0
}

func replaceHelperEnvironment(environment []string, scenario string) []string {
	prefix := preflightHelperEnvironment + "="
	replaced := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			replaced = append(replaced, item)
		}
	}
	return append(replaced, prefix+scenario)
}

func preflightHelperEntry(t *testing.T, scenario string) map[string]any {
	t.Helper()
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv(preflightHelperEnvironment, scenario)
	return map[string]any{"command": os.Args[0], "args": []any{}}
}

func TestPreflightReportsACommandThatCannotLaunch(t *testing.T) {
	t.Parallel()
	entry := map[string]any{
		"command": "libtmux-mcp-does-not-exist",
		"args":    []any{},
	}
	if reason := preflight(entry); !strings.Contains(reason, "could not launch") {
		t.Errorf("preflight said %q, want it to name the launch failure", reason)
	}
}

func TestPreflightReadsStderrWhileTheProcessDrains(t *testing.T) {
	t.Parallel()
	entry := map[string]any{
		"command": "sh",
		"args": []any{"-c", `
(exec 1>&-; i=0; while [ "$i" -lt 200 ]; do printf x >&2; sleep 0.001; i=$((i+1)); done) &
sleep 0.02
`},
	}
	if reason := preflight(entry); reason == "" {
		t.Fatal("preflight accepted a process that never answered initialize")
	}
}

func TestPreflightRejectsInvalidInitializeResults(t *testing.T) {
	for _, scenario := range []string{
		"malformed",
		"null",
		"missing-capabilities",
		"missing-server-info",
		"wrong-name",
		"empty-version",
	} {
		t.Run(scenario, func(t *testing.T) {
			entry := preflightHelperEntry(t, scenario)
			if reason := preflight(entry); reason == "" {
				t.Fatalf("preflight accepted the %s initialize result", scenario)
			}
		})
	}
}

func TestPreflightCompletesTheMCPHandshake(t *testing.T) {
	entry := preflightHelperEntry(t, "handshake")
	marker := filepath.Join(t.TempDir(), "handshake")
	t.Setenv("MCP_SWAP_PREFLIGHT_MARKER", marker)

	if reason := preflight(entry); reason != "" {
		t.Fatalf("preflight failed: %s", reason)
	}
	contents, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("preflight did not send initialized then ping: %v", err)
	}
	if got := string(contents); got != "initialized\nping\n" {
		t.Fatalf("handshake marker = %q", got)
	}
}

func TestPreflightClosesServerInputGracefully(t *testing.T) {
	entry := preflightHelperEntry(t, "graceful-eof")
	marker := filepath.Join(t.TempDir(), "closed")
	t.Setenv("MCP_SWAP_PREFLIGHT_MARKER", marker)

	if reason := preflight(entry); reason != "" {
		t.Fatalf("preflight failed: %s", reason)
	}
	contents, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("server did not finish after input closed: %v", err)
	}
	if got := string(contents); got != "eof\n" {
		t.Fatalf("close marker = %q", got)
	}
}

func TestPreflightMergesEntryEnvironment(t *testing.T) {
	entry := preflightHelperEntry(t, "environment")
	t.Setenv("MCP_SWAP_INHERITED", "kept")
	entry["env"] = map[string]any{"MCP_SWAP_ENTRY": "set"}

	if reason := preflight(entry); reason != "" {
		t.Fatalf("preflight failed with entry environment: %s", reason)
	}
}

func TestPreflightDoesNotWaitForAStderrDescendant(t *testing.T) {
	entry := preflightHelperEntry(t, "stderr-descendant")
	started := time.Now()
	reason := preflight(entry)
	if reason == "" {
		t.Fatal("preflight accepted a server that never answered initialize")
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("preflight waited %s for a descendant holding stderr", elapsed)
	}
}

func TestPreflightKeepsOnlyABoundedStderrTail(t *testing.T) {
	entry := preflightHelperEntry(t, "stderr-tail")
	reason := preflight(entry)
	if reason == "" {
		t.Fatal("preflight accepted a server that never answered initialize")
	}
	if len(reason) > 70<<10 {
		t.Fatalf("preflight returned %d bytes of stderr", len(reason))
	}
	if !strings.Contains(reason, "END-OF-KEPT-STDERR") {
		t.Fatalf("preflight dropped the stderr tail: %q", reason)
	}
	if strings.Contains(reason, "BEGIN-OF-DISCARDED-STDERR") {
		t.Fatal("preflight kept the beginning of oversized stderr")
	}
}
