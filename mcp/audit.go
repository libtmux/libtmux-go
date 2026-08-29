package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Audit logs retain tool names, outcomes, duration, and summarized arguments.
// Unclassified strings become their byte length and a truncated SHA-256 digest.

// AuditEnvironmentVariable names the variable that turns the record on and
// says where it goes: "stderr", or a path to append to.
const AuditEnvironmentVariable = "LIBTMUX_AUDIT"

// auditIdentifiers is the cleartext allowlist. User-chosen names remain summarized.
var auditIdentifiers = map[string]bool{
	"paneId": true, "windowId": true, "sessionId": true,
	"direction": true, "scope": true, "layout": true, "withPaneId": true,
	"maxLines": true, "maxBytes": true, "maxPanes": true, "maxMatchesPerPane": true,
	"timeoutSeconds": true, "percentage": true, "width": true, "height": true,
	"index": true, "zoom": true, "regex": true, "matchCase": true,
	"includeHistory": true, "joinWrapped": true, "sinceEntry": true,
	"suppressHistory": true, "literal": true, "bracket": true, "enter": true,
	"kill": true, "confirm": true, "history": true, "unset": true,
	"delete": true, "spread": true, "keepFocus": true, "scrollUp": true,
	"startLine": true, "endLine": true, "tool": true,
}

func auditWriter() (io.Writer, io.Closer) {
	switch destination := strings.TrimSpace(os.Getenv(AuditEnvironmentVariable)); destination {
	case "":
		return nil, nil
	case "stderr":
		return os.Stderr, nil
	default:
		// New files use mode 0600; existing destination permissions are unchanged.
		file, err := os.OpenFile(destination, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "libtmux-mcp: cannot write the audit record to %s: %v\n",
				destination, err)
			return nil, nil
		}
		return file, file
	}
}

// audit records each tool call after summarizeArguments applies the cleartext
// allowlist.
func audit(writer io.Writer) mcp.Middleware {
	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(
			ctx context.Context,
			method string,
			request mcp.Request,
		) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, request)
			}
			params, _ := request.GetParams().(*mcp.CallToolParamsRaw)
			started := time.Now()
			result, err := next(ctx, method, request)

			name := "unknown"
			var arguments json.RawMessage
			if params != nil {
				name = params.Name
				arguments = params.Arguments
			}
			logger.LogAttrs(ctx, slog.LevelInfo, "tool call",
				slog.String("tool", name),
				slog.String("outcome", auditOutcome(result, err)),
				slog.Int64("elapsedMillis", time.Since(started).Milliseconds()),
				slog.Any("arguments", summarizeArguments(arguments)),
			)
			return result, err
		}
	}
}

// MCP tools may report failure in a result rather than an error.
func auditOutcome(result mcp.Result, err error) string {
	if err != nil {
		return "error"
	}
	if call, ok := result.(*mcp.CallToolResult); ok && call.IsError {
		return "refused"
	}
	return "ok"
}

func summarizeArguments(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return map[string]any{"unreadable": true}
	}
	summary := make(map[string]any, len(decoded))
	for key, value := range decoded {
		summary[key] = summarizeValue(key, value)
	}
	return summary
}

func summarizeValue(key string, value any) any {
	switch typed := value.(type) {
	case string:
		if auditIdentifiers[key] {
			return typed
		}
		return digest(typed)
	case bool, float64, nil:
		return typed
	case []any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, summarizeValue(key, item))
		}
		return items
	case map[string]any:
		nested := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			nested[nestedKey] = summarizeValue(nestedKey, nestedValue)
		}
		return nested
	default:
		return map[string]any{"redacted": true}
	}
}

// digest supports correlation but may identify guessable inputs.
func digest(value string) map[string]any {
	sum := sha256.Sum256([]byte(value))
	return map[string]any{
		"len":    len(value),
		"sha256": hex.EncodeToString(sum[:6]),
	}
}

func auditedIdentifierNames() []string {
	return slices.Sorted(maps.Keys(auditIdentifiers))
}
