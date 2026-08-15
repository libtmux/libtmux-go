package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A record of what was asked for, without recording what was said.
//
// This server types into people's terminals. An operator handing one to a
// model has a reasonable question — what did it actually do? — and the honest
// answer needs a log. The dishonest way to provide it is to write down every
// command, because the commands contain the things commands contain: tokens
// pasted into a deploy, a password in a connection string, the contents of a
// file being moved.
//
// So an argument is either an identifier or a payload. Identifiers are named
// here and logged as themselves, because "%3" and "below" are what make a
// record readable and neither is a secret. Everything else is a payload,
// logged as its length and a digest prefix: enough to see that the same thing
// was sent twice, or that a command was long when it should have been short,
// and not enough to recover it.
//
// The list is an allowlist rather than a denylist. A tool added later with a
// field nobody classified is a payload by default, which is the safe way to be
// wrong.
//
// It is off unless an operator asks for it. A server that writes somebody's
// command history to their terminal unbidden has answered a question nobody
// asked, and stderr on a stdio server is where a client shows diagnostics.

// AuditEnvironmentVariable names the variable that turns the record on and
// says where it goes: "stderr", or a path to append to.
const AuditEnvironmentVariable = "LIBTMUX_AUDIT"

// auditIdentifiers are the argument names logged as themselves.
//
// Each is something tmux itself would show anyone looking at the server: an
// object's id, a name it is listed under, a direction, a bound, a flag. None
// carries content a caller supplied for a program to read.
var auditIdentifiers = map[string]bool{
	"paneId": true, "windowId": true, "sessionId": true, "sessionName": true,
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

// auditWriter reports where the record goes, and whether it is on at all.
func auditWriter() io.Writer {
	switch destination := strings.TrimSpace(os.Getenv(AuditEnvironmentVariable)); destination {
	case "":
		return nil
	case "stderr":
		return os.Stderr
	default:
		// Appended to, and never created with permissions a second user could
		// read: a record of what an operator ran is theirs.
		file, err := os.OpenFile(destination, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "libtmux-mcp: cannot write the audit record to %s: %v\n",
				destination, err)
			return nil
		}
		return file
	}
}

// audit records every tool call: what was asked, how it ended, how long it
// took.
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

// auditOutcome says how a call ended in one word.
//
// A tool that failed reports it in its result rather than as an error, so both
// have to be read or every refusal would be recorded as a success.
func auditOutcome(result mcp.Result, err error) string {
	if err != nil {
		return "error"
	}
	if call, ok := result.(*mcp.CallToolResult); ok && call.IsError {
		return "refused"
	}
	return "ok"
}

// summarizeArguments reduces a call's arguments to what is safe to keep.
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

// summarizeValue keeps an identifier and digests anything else.
func summarizeValue(key string, value any) any {
	switch typed := value.(type) {
	case string:
		if auditIdentifiers[key] {
			return typed
		}
		return digest(typed)
	case bool, float64, nil:
		// A number or a flag says how much or whether, never what.
		return typed
	case []any:
		// A list is summarized element by element under the same name, so a
		// batch's calls are readable and the arguments inside them are not.
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

// digest reports a payload's size and a stable prefix of its hash.
//
// The prefix is what makes a record useful without making it a transcript: the
// same command sent twice shows the same digest, so a loop is visible, and no
// digest is the command.
func digest(value string) map[string]any {
	sum := sha256.Sum256([]byte(value))
	return map[string]any{
		"len":    len(value),
		"sha256": hex.EncodeToString(sum[:6]),
	}
}

// auditedIdentifierNames lists what is logged in the clear, for the test that
// keeps this honest as tools are added.
func auditedIdentifierNames() []string {
	names := make([]string, 0, len(auditIdentifiers))
	for name := range auditIdentifiers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
