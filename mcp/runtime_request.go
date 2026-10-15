package mcp

import (
	"context"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type acquiredServerKey struct{}

func withAcquiredServer(
	ctx context.Context,
	acquired *runtimeAcquisition,
) context.Context {
	return context.WithValue(ctx, acquiredServerKey{}, acquired)
}

func acquiredServerFromContext(ctx context.Context) *runtimeAcquisition {
	acquired, _ := ctx.Value(acquiredServerKey{}).(*runtimeAcquisition)
	return acquired
}

func releaseAcquiredServer(ctx context.Context) {
	acquiredServerFromContext(ctx).release()
}

func (t *tools) tmux(ctx context.Context) tmux.Server {
	if acquired := acquiredServerFromContext(ctx); acquired != nil {
		if acquired.unbound && acquired.released.Load() {
			return t.runtime.current()
		}
		return acquired.server
	}
	return t.runtime.current()
}

func (t *tools) acquireRequestRuntime(
	ctx context.Context,
) (context.Context, *runtimeAcquisition, error) {
	acquired, err := t.runtime.acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	return withAcquiredServer(ctx, acquired), acquired, nil
}

// withRequestRuntime leases one provenance-bound server for the full handler
// and makes terminal failures visible without retrying acted work.
//
// A tool that only reads is tried once more when its first attempt finds the
// runtime's connection already lost, as it is on the first call after the
// tmux server exits: the read did nothing, and the retry runs on a fresh
// acquisition, which reports a missing server as the read tools document.
func withRequestRuntime[In, Out any](
	t *tools,
	readOnly bool,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
	attempt := func(
		ctx context.Context,
		request *mcp.CallToolRequest,
		input In,
	) (*mcp.CallToolResult, Out, error) {
		requestCtx, acquired, err := t.acquireRequestRuntime(ctx)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		defer acquired.release()
		result, output, err := handler(requestCtx, request, input)
		t.runtime.observe(err)
		return result, output, err
	}
	return func(
		ctx context.Context,
		request *mcp.CallToolRequest,
		input In,
	) (*mcp.CallToolResult, Out, error) {
		result, output, err := attempt(ctx, request, input)
		if readOnly && err != nil && isIntrinsicTerminalRuntimeError(err) && ctx.Err() == nil {
			return attempt(ctx, request, input)
		}
		return result, output, err
	}
}
