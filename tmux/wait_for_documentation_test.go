package tmux_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestWaitForLockDocumentsThePermanentWedge pins GO2-2/X2-1: a cancelled or
// timed-out ctx while queued for a wait-for lock permanently wedges that
// channel for every future locker, on every supported tmux version
// (cmd-wait-for.c cmd_wait_for_unlock hands the mutex to the next queued
// locker regardless of whether its client is still there). This is a tmux
// limitation WaitForModeLock and Server.WaitFor cannot work around, and
// their doc comments already discuss cancellation without ever naming it -
// this asserts that gap stays closed.
func TestWaitForLockDocumentsThePermanentWedge(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "server_exec.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	lockDoc := ""
	waitForDoc := ""
	for _, declaration := range file.Decls {
		switch general := declaration.(type) {
		case *ast.GenDecl:
			if general.Tok != token.CONST {
				continue
			}
			for _, specification := range general.Specs {
				valueSpec, ok := specification.(*ast.ValueSpec)
				if !ok || len(valueSpec.Names) != 1 || valueSpec.Names[0].Name != "WaitForModeLock" {
					continue
				}
				if valueSpec.Doc != nil {
					lockDoc = valueSpec.Doc.Text()
				}
			}
		case *ast.FuncDecl:
			if general.Name.Name != "WaitFor" || general.Recv == nil {
				continue
			}
			if general.Doc != nil {
				waitForDoc = general.Doc.Text()
			}
		}
	}

	if lockDoc == "" {
		t.Fatal("WaitForModeLock has no doc comment")
	}
	if waitForDoc == "" {
		t.Fatal("Server.WaitFor has no doc comment")
	}
	for name, text := range map[string]string{"WaitForModeLock": lockDoc, "Server.WaitFor": waitForDoc} {
		if !strings.Contains(text, "wedge") {
			t.Errorf("%s doc comment = %q, want it to name the permanent wedge hazard", name, text)
		}
	}
	if !strings.Contains(lockDoc, "no client-side recovery") &&
		!strings.Contains(lockDoc, "tmux limitation") {
		t.Errorf("WaitForModeLock doc comment = %q, want it to say this is a tmux limitation", lockDoc)
	}
}
