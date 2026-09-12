// Package cli implements the tmux-workspace process boundary.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const referenceVersion = "1.74.0"

type failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Exit    int    `json:"-"`
}

func (e *failure) Error() string { return e.Message }
func usage(format string, args ...any) error {
	return &failure{"usage", fmt.Sprintf(format, args...), 2}
}

type invocation struct {
	ctx                      context.Context
	in                       io.Reader
	out, err                 io.Writer
	json, ndjson             bool
	color, logLevel, command string
	mu                       sync.Mutex
	sequence                 int
	writeErr                 error
	dispatched               bool
}

// Run executes one fresh command tree and returns its process exit status.
func Run(ctx context.Context, args []string, in io.Reader, out, diagnostic io.Writer) int {
	r := &invocation{ctx: ctx, in: in, out: out, err: diagnostic, color: "auto", logLevel: "warning"}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--json" || arg == "--json=true" {
			r.json = true
		}
		if arg == "--ndjson" || arg == "--ndjson=true" {
			r.ndjson = true
		}
	}
	cmd := r.tree()
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	if err != nil && !r.dispatched {
		var specific *failure
		if !errors.As(err, &specific) {
			err = usage("%s", err)
		}
	}
	if ctx.Err() != nil {
		err = &failure{"interrupted", "operation interrupted", 130}
	}
	if err == nil {
		err = r.writeErr
	}
	if err == nil {
		return 0
	}
	f := &failure{"operation_failed", err.Error(), 1}
	var specific *failure
	if errors.As(err, &specific) {
		f = specific
	}
	if r.machine() {
		if encodeErr := json.NewEncoder(diagnostic).Encode(f); encodeErr != nil {
			return f.Exit
		}
	} else {
		_, _ = fmt.Fprintln(diagnostic, r.style("error", "error:")+" "+f.Message)
	}
	return f.Exit
}

func (r *invocation) machine() bool { return r.json || r.ndjson }
func (r *invocation) encode(value any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writeErr != nil {
		return r.writeErr
	}
	r.writeErr = json.NewEncoder(r.out).Encode(value)
	if flusher, ok := r.out.(interface{ Flush() error }); ok && r.writeErr == nil {
		r.writeErr = flusher.Flush()
	}
	return r.writeErr
}

func (r *invocation) event(event string, data map[string]any) error {
	if !r.ndjson {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writeErr != nil {
		return r.writeErr
	}
	r.sequence++
	if data == nil {
		data = map[string]any{}
	}
	data["schema_version"], data["command"], data["event"], data["sequence"] = 1, r.command, event, r.sequence
	r.writeErr = json.NewEncoder(r.out).Encode(data)
	if f, ok := r.out.(interface{ Flush() error }); ok && r.writeErr == nil {
		r.writeErr = f.Flush()
	}
	return r.writeErr
}

func (r *invocation) result(value map[string]any) error {
	value["schema_version"], value["command"] = 1, r.command
	return r.encode(value)
}

func (r *invocation) style(role, value string) string {
	if r.machine() || os.Getenv("NO_COLOR") != "" || r.color == "never" {
		return value
	}
	force := r.color == "always" || os.Getenv("FORCE_COLOR") != "" || (os.Getenv("CLICOLOR_FORCE") != "" && os.Getenv("CLICOLOR_FORCE") != "0")
	if !force && (os.Getenv("CLICOLOR") == "0" || !terminal(r.out)) {
		return value
	}
	codes := map[string]string{"heading": "1;96", "subject": "1;35", "info": "36", "success": "32", "warning": "33", "error": "31", "secondary": "2"}
	return "\x1b[" + codes[role] + "m" + value + "\x1b[0m"
}

func terminal(w any) bool {
	f, ok := w.(*os.File)
	return ok && (isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()))
}

type options struct {
	socketName, socketPath, tmuxConfig, session     string
	yes, detached, append, colors256, colors88      bool
	logFile, progressFormat                         string
	progressLines                                   int
	noProgress                                      bool
	tree, full                                      bool
	fields                                          []string
	ignoreCase, smartCase, fixed, word, invert, any bool
	format, saveTo                                  string
	quiet, force                                    bool
	code, backend                                   string
	pythonrc, vi                                    bool
}

func (r *invocation) tree() *cobra.Command {
	var version, metadata bool
	var completion string
	root := &cobra.Command{Use: "tmux-workspace", Short: "manage tmuxp workspaces with native Go services", SilenceErrors: true, SilenceUsage: true, CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true}}
	root.SetIn(r.in)
	root.SetOut(r.out)
	root.SetErr(r.err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usage("%s", err) })
	root.PersistentFlags().BoolVar(&r.json, "json", r.json, "emit JSON; machine mode never prompts")
	root.PersistentFlags().BoolVar(&r.ndjson, "ndjson", r.ndjson, "emit flushed NDJSON; takes precedence over --json")
	root.PersistentFlags().StringVar(&r.color, "color", "auto", "color policy: auto, always, never (default auto)")
	root.PersistentFlags().StringVar(&r.logLevel, "log-level", "warning", "diagnostic level: debug, info, warning, error, critical (default warning)")
	root.Flags().BoolVarP(&version, "version", "V", false, "show native and compatibility versions")
	root.Flags().BoolVar(&metadata, "command-tree", false, "export the command metadata tree as JSON")
	root.Flags().StringVar(&completion, "generate-completion", "", "write bash, zsh, fish or powershell completion (default empty: disabled)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		r.dispatched = true
		r.command = strings.TrimPrefix(cmd.CommandPath(), "tmux-workspace ")
		if !contains([]string{"auto", "always", "never"}, r.color) {
			return usage("invalid color policy %q", r.color)
		}
		if !contains([]string{"debug", "info", "warning", "error", "critical"}, r.logLevel) {
			return usage("invalid log level %q", r.logLevel)
		}
		return nil
	}
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return usage("unknown command %q", args[0])
		}
		if version {
			if r.machine() {
				return r.encode(map[string]any{"program": "tmux-workspace", "port": "go", "tmuxp_compatibility": referenceVersion})
			}
			_, err := fmt.Fprintln(r.out, "tmux-workspace (Go), tmuxp compatibility "+referenceVersion)
			return err
		}
		if metadata {
			return r.encode(commandMetadata(root))
		}
		switch completion {
		case "":
			return cmd.Help()
		case "bash":
			return root.GenBashCompletionV2(r.out, true)
		case "zsh":
			return root.GenZshCompletion(r.out)
		case "fish":
			return root.GenFishCompletion(r.out, true)
		case "powershell":
			return root.GenPowerShellCompletionWithDesc(r.out)
		default:
			return usage("invalid completion shell %q", completion)
		}
	}
	add := func(name, synopsis, description string, minimumArgs, maximumArgs int, action func(*cobra.Command, *options, []string) error) (*cobra.Command, *options) {
		o := &options{progressLines: 3, pythonrc: true}
		cmd := &cobra.Command{Use: name + synopsis, Short: description, Args: func(_ *cobra.Command, args []string) error {
			if len(args) < minimumArgs || (maximumArgs >= 0 && len(args) > maximumArgs) {
				return usage("%s expects %d..%s operands", name, minimumArgs, maximum(maximumArgs))
			}
			return nil
		}}
		cmd.Annotations = map[string]string{"positional_min": strconv.Itoa(minimumArgs), "positional_max": strconv.Itoa(maximumArgs)}
		cmd.RunE = func(cmd *cobra.Command, args []string) error { return action(cmd, o, args) }
		root.AddCommand(cmd)
		return cmd, o
	}
	load, l := add("load", " workspace-file...", "create, reuse or append a workspace", 1, -1, r.load)
	sockets(load, l)
	f := load.Flags()
	f.StringVarP(&l.tmuxConfig, "tmux-config", "f", "", "tmux configuration file (default empty: tmux default)")
	f.StringVarP(&l.session, "session-name", "s", "", "override session name (default empty: document name)")
	f.BoolVarP(&l.yes, "yes", "y", false, "answer yes to yes/no prompts")
	f.BoolVarP(&l.detached, "detached", "d", false, "load without attaching")
	f.BoolVarP(&l.append, "append", "a", false, "append windows to the current session")
	f.BoolVarP(&l.colors256, "256-colors", "2", false, "request 256 terminal colors")
	f.BoolVarP(&l.colors88, "88-colors", "8", false, "request 88 terminal colors")
	f.StringVar(&l.logFile, "log-file", "", "write diagnostics and script output to a log file (default empty: disabled)")
	f.StringVar(&l.progressFormat, "progress-format", "", "progress preset or token format; TMUXP_PROGRESS_FORMAT (default default)")
	f.IntVar(&l.progressLines, "progress-lines", 3, "script panel lines; 0 direct output, -1 terminal height; TMUXP_PROGRESS_LINES (default 3)")
	f.BoolVar(&l.noProgress, "no-progress", false, "disable animation; TMUXP_PROGRESS=0")
	ls, list := add("ls", "", "list local and global workspace files", 0, 0, r.list)
	ls.Flags().BoolVar(&list.tree, "tree", false, "show directory and configuration trees")
	ls.Flags().BoolVar(&list.full, "full", false, "include complete configuration documents")
	search, s := add("search", " [query-term...]", "search workspace fields with Python expressions", 0, -1, r.search)
	f = search.Flags()
	f.StringArrayVarP(&s.fields, "field", "f", nil, "repeat field: name/n, session/s, path/p, window/w, pane (default all)")
	f.BoolVarP(&s.ignoreCase, "ignore-case", "i", false, "ignore case")
	f.BoolVarP(&s.smartCase, "smart-case", "S", false, "ignore case unless the pattern contains uppercase")
	f.BoolVarP(&s.fixed, "fixed-strings", "F", false, "treat patterns as literal strings")
	f.BoolVarP(&s.word, "word-regexp", "w", false, "match whole words")
	f.BoolVarP(&s.invert, "invert-match", "v", false, "select workspaces that do not match")
	f.BoolVar(&s.any, "any", false, "match any query term (default all)")
	add("edit", " workspace-file", "open a workspace with EDITOR", 1, 1, r.edit)
	freeze, fr := add("freeze", " [session-name]", "capture a live session; machine mode returns the document", 0, 1, r.freeze)
	sockets(freeze, fr)
	writeFlags(freeze, fr, true)
	freeze.Flags().BoolVarP(&fr.yes, "yes", "y", false, "answer yes to yes/no prompts; does not authorize overwrite")
	freeze.Flags().BoolVarP(&fr.quiet, "quiet", "q", false, "suppress status text; does not suppress prompts")
	convert, co := add("convert", " workspace-file", "convert a complete YAML or JSON document", 1, 1, r.convert)
	writeFlags(convert, co, false)
	convert.Flags().BoolVarP(&co.yes, "yes", "y", false, "answer yes to conversion prompts")
	imp := &cobra.Command{Use: "import", Short: "import teamocil or tmuxinator documents", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		return usage("import requires teamocil or tmuxinator and a source file")
	}}
	root.AddCommand(imp)
	for _, kind := range []string{"teamocil", "tmuxinator"} {
		o := &options{}
		child := &cobra.Command{Use: kind + " source-file", Short: "import a " + kind + " document", Annotations: map[string]string{"positional_min": "1", "positional_max": "1"}, Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("import requires one source file")
			}
			return nil
		}}
		writeFlags(child, o, false)
		child.RunE = func(cmd *cobra.Command, args []string) error { return r.importDocument(cmd, o, args, kind) }
		imp.AddCommand(child)
	}
	shell, sh := add("shell", " [session-name] [window-name]", "open the version-checked Python tmuxp shell", 0, 2, r.shell)
	sockets(shell, sh)
	shell.Flags().StringVarP(&sh.code, "python-code", "c", "", "execute Python code (default empty: interactive shell)")
	sh.backend = "best"
	for _, backend := range []string{"best", "pdb", "code", "ptipython", "ptpython", "ipython", "bpython"} {
		shell.Flags().Var(&constantValue{target: &sh.backend, value: backend}, backend, "select "+backend+" Python shell (mutually exclusive; default best)")
		shell.Flags().Lookup(backend).NoOptDefVal = "true"
	}
	pair(shell, "use-pythonrc", "no-startup", &sh.pythonrc, "load Python startup file (last occurrence wins; default enabled)")
	pair(shell, "use-vi-mode", "no-vi-mode", &sh.vi, "enable vi editing (last occurrence wins; default disabled)")
	add("debug-info", "", "show redacted runtime and tmux diagnostics", 0, 0, r.debugInfo)
	return root
}

func maximum(value int) string {
	if value < 0 {
		return "many"
	}
	return strconv.Itoa(value)
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func sockets(cmd *cobra.Command, o *options) {
	cmd.Flags().StringVarP(&o.socketName, "socket-name", "L", "", "tmux socket name (default empty: environment/default)")
	cmd.Flags().StringVarP(&o.socketPath, "socket-path", "S", "", "tmux socket path; takes precedence over -L (default empty: named socket)")
}

func writeFlags(cmd *cobra.Command, o *options, freeze bool) {
	if freeze {
		cmd.Flags().StringVarP(&o.format, "workspace-format", "f", "", "document format: yaml or json (default yaml)")
		cmd.Flags().StringVarP(&o.saveTo, "save-to", "o", "", "destination (default empty: machine stdout or human prompt)")
	} else {
		cmd.Flags().StringVar(&o.format, "workspace-format", "", "document format: yaml or json (default opposite source for convert, yaml for import)")
		cmd.Flags().StringVar(&o.saveTo, "save-to", "", "destination (default empty: machine stdout or human prompt)")
	}
	cmd.Flags().BoolVar(&o.force, "force", false, "authorize replacing an existing destination")
}

type constantValue struct {
	target *string
	value  string
}

func (v *constantValue) String() string { return "false" }
func (v *constantValue) Type() string   { return "bool" }
func (v *constantValue) Set(s string) error {
	b, e := strconv.ParseBool(s)
	if e == nil && b {
		*v.target = v.value
	}
	return e
}

type toggleValue struct {
	target   *bool
	positive bool
}

func (v *toggleValue) String() string { return strconv.FormatBool(*v.target == v.positive) }
func (v *toggleValue) Type() string   { return "bool" }
func (v *toggleValue) Set(s string) error {
	b, e := strconv.ParseBool(s)
	if e == nil {
		*v.target = b == v.positive
	}
	return e
}

func pair(cmd *cobra.Command, positive, negative string, target *bool, help string) {
	for _, entry := range []struct {
		name     string
		positive bool
	}{{positive, true}, {negative, false}} {
		cmd.Flags().Var(&toggleValue{target, entry.positive}, entry.name, help)
		cmd.Flags().Lookup(entry.name).NoOptDefVal = "true"
	}
}

func commandMetadata(cmd *cobra.Command) map[string]any {
	flags := []map[string]any{}
	cmd.InitDefaultHelpFlag()
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		flags = append(flags, map[string]any{"name": f.Name, "shorthand": f.Shorthand, "type": f.Value.Type(), "default": f.DefValue, "description": f.Usage, "no_option_value": f.NoOptDefVal})
	})
	inherited := []map[string]any{}
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		inherited = append(inherited, map[string]any{"name": f.Name, "type": f.Value.Type(), "default": f.DefValue})
	})
	children := []map[string]any{}
	for _, child := range cmd.Commands() {
		children = append(children, commandMetadata(child))
	}
	return map[string]any{"name": cmd.Name(), "path": cmd.CommandPath(), "usage": cmd.Use, "description": cmd.Short, "aliases": cmd.Aliases, "positionals": cmd.Annotations, "flags": flags, "inherited_flags": inherited, "children": children}
}
