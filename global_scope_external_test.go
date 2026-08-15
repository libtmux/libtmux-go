package tmux_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/libtmux/libtmux-go"
)

type globalSessionScopeSignatures interface {
	Options(context.Context) (tmux.SessionOptionValues, error)
	RawOption(context.Context, string) (string, bool, error)
	SetOption(context.Context, string, string, tmux.SetOptionOptions) error
	AppendOption(context.Context, string, string, tmux.SetOptionOptions) error
	UnsetOption(context.Context, string, tmux.UnsetOptionOptions) error
	Hooks(context.Context) (tmux.ServerHookValues, error)
	RawHook(context.Context, string) (string, bool, error)
	SetHook(context.Context, string, string) error
	AppendHook(context.Context, string, string) error
	UnsetHook(context.Context, string) error
	RunHook(context.Context, string) error
	SetHooks(
		context.Context,
		string,
		tmux.SparseArray[string],
		tmux.SetHooksOptions,
	) (tmux.SetHooksResult, error)
}

type globalWindowScopeSignatures interface {
	Options(context.Context) (tmux.WindowOptionValues, error)
	RawOption(context.Context, string) (string, bool, error)
	SetOption(context.Context, string, string, tmux.SetOptionOptions) error
	AppendOption(context.Context, string, string, tmux.SetOptionOptions) error
	UnsetOption(context.Context, string, tmux.UnsetOptionOptions) error
	Hooks(context.Context) (tmux.WindowHookValues, error)
	RawHook(context.Context, string) (string, bool, error)
	SetHook(context.Context, string, string) error
	AppendHook(context.Context, string, string) error
	UnsetHook(context.Context, string) error
	RunHook(context.Context, string) error
	SetHooks(
		context.Context,
		string,
		tmux.SparseArray[string],
		tmux.SetHooksOptions,
	) (tmux.SetHooksResult, error)
}

var (
	_ globalSessionScopeSignatures = tmux.GlobalSessionScope{}
	_ globalWindowScopeSignatures  = tmux.GlobalWindowScope{}
)

func TestServerLegacyGlobalScopeMethodsAreRemoved(t *testing.T) {
	t.Parallel()

	legacy := map[string]bool{
		"GlobalSessionOptions":      true,
		"GlobalWindowOptions":       true,
		"RawGlobalSessionOption":    true,
		"RawGlobalWindowOption":     true,
		"SetGlobalSessionOption":    true,
		"SetGlobalWindowOption":     true,
		"AppendGlobalSessionOption": true,
		"AppendGlobalWindowOption":  true,
		"UnsetGlobalSessionOption":  true,
		"UnsetGlobalWindowOption":   true,
		"Hooks":                     true,
		"RawHook":                   true,
		"SetHook":                   true,
		"AppendHook":                true,
		"UnsetHook":                 true,
		"RunHook":                   true,
		"SetHooks":                  true,
		"GlobalWindowHooks":         true,
		"RawGlobalWindowHook":       true,
		"SetGlobalWindowHook":       true,
		"AppendGlobalWindowHook":    true,
		"UnsetGlobalWindowHook":     true,
		"RunGlobalWindowHook":       true,
		"SetGlobalWindowHooks":      true,
	}
	if len(legacy) != 24 {
		t.Fatalf("legacy global Server method set has %d entries, want 24", len(legacy))
	}

	serverType := reflect.TypeOf((*tmux.Server)(nil))
	for name := range legacy {
		if _, found := serverType.MethodByName(name); found {
			t.Errorf("legacy Server.%s remains", name)
		}
	}
}
