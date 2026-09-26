package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// sharedMachineCodes are the ten machine codes shared across every
// workspace-cli port. CLI.md documents them as a set rather than one by one,
// so they are exempt from the per-code documentation check below.
var sharedMachineCodes = []string{
	"workspace_not_found", "invalid_workspace", "unsupported_key",
	"session_not_found", "session_mismatch", "tmux_unavailable",
	"tmux_failed", "script_failed", "destination_exists", "usage",
}

// TestCLIReferenceNamesEveryMachineCode: every &failure{"code", ...} literal
// in this package's own source is either one of the ten shared codes or
// named, with its reason, in CLI.md's reference tables. A code added to one
// and not the other is documentation drift -- this is what a reader who
// trusts the docs would miss.
func TestCLIReferenceNamesEveryMachineCode(t *testing.T) {
	pattern := regexp.MustCompile(`&failure\{"([a-z_]+)"`)
	found := map[string]bool{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range pattern.FindAllStringSubmatch(string(data), -1) {
			found[m[1]] = true
		}
	}
	for _, code := range sharedMachineCodes {
		delete(found, code)
	}
	reference, err := os.ReadFile("../../CLI.md")
	if err != nil {
		t.Fatal(err)
	}
	ref := string(reference)
	for code := range found {
		if !strings.Contains(ref, "`"+code+"`") {
			t.Errorf("CLI.md's reference tables do not name machine code %q", code)
		}
	}
}
