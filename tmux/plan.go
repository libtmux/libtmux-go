package tmux

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ErrPlan identifies a plan that cannot run as recorded. It is matched by
// errors.Is for [PlanError].
var ErrPlan = errors.New("tmux: plan cannot run")

// PlanError reports a plan this package refused before sending anything to
// tmux. It matches [ErrPlan] through errors.Is; callers can recover Step and
// Reason with errors.As.
type PlanError struct {
	// Step is the zero-based index of the operation at fault.
	Step int
	// Reason describes what makes the plan unrunnable.
	Reason string
}

// Error implements error.
func (e *PlanError) Error() string {
	return fmt.Sprintf("%v: step %d: %s", ErrPlan, e.Step, e.Reason)
}

// Unwrap makes PlanError compatible with ErrPlan.
func (e *PlanError) Unwrap() error { return ErrPlan }

// Ref addresses an existing object or one an earlier [Plan] step will create.
// This permits forward construction without tmux I/O. The zero Ref addresses
// nothing and makes a targeted operation fail validation.
type Ref struct {
	// target is a concrete tmux target for an object that already exists.
	target string
	// step is the one-based index of the step that creates the object, and is
	// zero when target names one that already exists. It is one-based so that
	// the zero Ref is not silently a reference to the first step.
	step int
	// daemon is present only on refs derived from materialized records. Raw ID
	// constructors remain selector-relative.
	daemon *snapshotServerIdentity
}

// Ref returns a [Ref] addressing the receiver.
func (s Session) Ref() Ref { return refFor(s.sessionID.String(), s.server.daemon) }

// Ref returns a [Ref] addressing the receiver.
func (w Window) Ref() Ref { return refFor(w.windowID.String(), w.server.daemon) }

// Ref returns a [Ref] addressing the receiver.
func (p Pane) Ref() Ref { return refFor(p.paneID.String(), p.server.daemon) }

func refFor(target string, daemon *snapshotServerIdentity) Ref {
	ref := Ref{target: target}
	if daemon != nil {
		identity := *daemon
		ref.daemon = &identity
	}
	return ref
}

// SessionRef returns a selector-relative [Ref] addressing a session by ID, for
// a caller holding an identifier rather than a materialized record.
func SessionRef(id SessionID) Ref { return Ref{target: id.String()} }

// WindowRef returns a selector-relative [Ref] addressing a window by ID, for a
// caller holding an identifier rather than a materialized record.
func WindowRef(id WindowID) Ref { return Ref{target: id.String()} }

// PaneRef returns a selector-relative [Ref] addressing a pane by ID, for a
// caller holding an identifier rather than a materialized record.
func PaneRef(id PaneID) Ref { return Ref{target: id.String()} }

// String returns the target a Ref resolves to, or a placeholder naming the step
// that will produce it.
func (r Ref) String() string {
	if r.step == 0 {
		if r.target == "" {
			return "{no target}"
		}
		return r.target
	}
	return fmt.Sprintf("{step %d}", r.step-1)
}

// Op is one tmux command a [Plan] has recorded but not run. Build one with a
// [Plan] method rather than directly; the zero value records nothing.
type Op struct {
	name   string
	target Ref
	// source is a command's optional -s object.
	source Ref
	// build renders without I/O from resolved targets and the run-local context.
	build func(target, source string, render planRenderContext) ([]string, error)
	// creates means stdout contains an ID for later Refs.
	creates   bool
	captures  bool
	untargets bool
	// needsVersion requests the otherwise-avoided version probe.
	needsVersion bool
}

// Chainable reports whether the operation may share a tmux invocation with
// others, which is what a [Planner] groups on.
func (o Op) Chainable() bool { return o.chainable() }

// Command returns the tmux command the operation records.
func (o Op) Command() string { return o.name }

// Output-producing operations run alone because command-list stdout has no boundaries.
func (o Op) chainable() bool { return !o.captures && !o.creates }

// Plan records ordered tmux commands for later execution. It groups commands
// that need no answer and lets later steps reference objects earlier steps create.
//
// Recording touches nothing. [Plan.Preview] renders what would be sent and
// [Plan.Explain] says how it would be grouped, both without a server;
// [Plan.Run] is the only method that reaches tmux.
//
// A Plan is not safe for concurrent use.
type Plan struct {
	ops []Op
}

// planRenderContext freezes the inputs that may change how an operation is
// rendered. Passing it by value keeps one run's policy out of the recorded plan.
type planRenderContext struct {
	version        Version
	unsupported    UnsupportedPolicy
	warningHandler WarningHandler
}

// NewPlan returns an empty [Plan].
func NewPlan() *Plan { return &Plan{} }

func (p *Plan) needsVersion() bool {
	for _, op := range p.ops {
		if op.needsVersion {
			return true
		}
	}
	return false
}

// Len returns how many operations the plan has recorded.
func (p *Plan) Len() int { return len(p.ops) }

// Ops returns the recorded operations, in order, for a [Planner] to group.
func (p *Plan) Ops() []Op { return slices.Clone(p.ops) }

func (p *Plan) add(op Op) Ref {
	p.ops = append(p.ops, op)
	return Ref{step: len(p.ops)}
}

func (p *Plan) resolve(ref Ref, created map[int]string, step int) (string, error) {
	if ref.step == 0 {
		if ref.target == "" {
			return "", &PlanError{Step: step, Reason: "operation has no target"}
		}
		return ref.target, nil
	}
	target, ok := created[ref.step-1]
	if !ok {
		return "", &PlanError{
			Step: step,
			Reason: fmt.Sprintf(
				"targets step %d, which did not report an object it created",
				ref.step-1,
			),
		}
	}
	return target, nil
}

func (p *Plan) render(
	index int,
	created map[int]string,
	render planRenderContext,
) ([]string, error) {
	op := p.ops[index]
	if op.build == nil {
		return nil, &PlanError{Step: index, Reason: "operation records no command"}
	}
	target := ""
	if !op.untargets {
		resolved, err := p.resolve(op.target, created, index)
		if err != nil {
			return nil, err
		}
		target = resolved
	}
	source := ""
	if op.source != (Ref{}) {
		resolved, err := p.resolve(op.source, created, index)
		if err != nil {
			return nil, err
		}
		source = resolved
	}
	argv, err := op.build(target, source, render)
	if err != nil {
		// Add the step here while preserving the request builder's typed error.
		return nil, fmt.Errorf("step %d: %s: %w", index, op.name, err)
	}
	return argv, nil
}

func targetedArguments(command, target string, rest ...string) ([]string, error) {
	if err := validateServerCommandArgument(
		command, "Target", target, true,
	); err != nil {
		return nil, err
	}
	for _, argument := range rest {
		if err := validateServerCommandArgument(
			command, "Arguments", argument, true,
		); err != nil {
			return nil, err
		}
	}
	arguments := make([]string, 0, len(rest)+3)
	arguments = append(arguments, command, "-t", target)
	return append(arguments, rest...), nil
}

func untargetedArguments(command string, rest ...string) ([]string, error) {
	for _, argument := range rest {
		if err := validateServerCommandArgument(
			command, "Arguments", argument, true,
		); err != nil {
			return nil, err
		}
	}
	return append([]string{command}, rest...), nil
}

// Dispatch is one tmux invocation and the operations it carries. A dispatch
// holding more than one operation is sent as a tmux command list.
type Dispatch struct {
	// Ops are the indices of the operations this dispatch carries, in order.
	Ops []int
	// Reason says why the dispatch ends where it does. It is "chained" for a
	// command list, "creates" for an operation whose new object's ID a later
	// step needs, "captures" for one whose output the caller reads, and "alone"
	// for a chainable operation with nothing beside it to chain to.
	Reason string
}

// Planner groups a plan's operations into tmux invocations.
//
// Dispatches must contain every operation exactly once and in recorded order.
// [Plan.Run] validates this before reaching tmux.
//
// Operations with [Op.Chainable] false must run alone because command-list
// stdout cannot be attributed to an individual operation.
type Planner interface {
	// Plan returns the ordered dispatches for ops.
	Plan(ops []Op) []Dispatch
}

// Sequential sends every operation as its own tmux invocation. It is the
// simplest correct planner, and useful as a baseline: a plan produces the same
// results through it as through [Folding], at one invocation per operation.
type Sequential struct{}

// Plan returns one dispatch per operation.
func (Sequential) Plan(ops []Op) []Dispatch {
	dispatches := make([]Dispatch, len(ops))
	for index, op := range ops {
		reason := "alone"
		switch {
		case op.creates:
			reason = "creates"
		case op.captures:
			reason = "captures"
		}
		dispatches[index] = Dispatch{Ops: []int{index}, Reason: reason}
	}
	return dispatches
}

// Folding groups each run of operations that neither answer nor create into one
// tmux command list, and sends the rest alone. It is what [Plan.Run] uses.
type Folding struct{}

// Plan returns the dispatches for ops, grouping consecutive chainable runs.
func (Folding) Plan(ops []Op) []Dispatch {
	var dispatches []Dispatch
	for index := 0; index < len(ops); {
		dispatch := foldFrom(ops, index)
		dispatches = append(dispatches, dispatch)
		index = dispatch.Ops[len(dispatch.Ops)-1] + 1
	}
	return dispatches
}

func foldFrom(ops []Op, index int) Dispatch {
	if !ops[index].chainable() {
		reason := "captures"
		if ops[index].creates {
			reason = "creates"
		}
		return Dispatch{Ops: []int{index}, Reason: reason}
	}
	end := index
	for end < len(ops) && ops[end].chainable() {
		end++
	}
	group := make([]int, end-index)
	for cursor := range group {
		group[cursor] = index + cursor
	}
	reason := "chained"
	if len(group) == 1 {
		reason = "alone"
	}
	return Dispatch{Ops: group, Reason: reason}
}

// Explain reports how [Plan.Run] would group the recorded operations into tmux
// commands, and why each group ends where it does. It runs no commands.
//
// The number of dispatches is the number of tmux invocations.
func (p *Plan) Explain() []Dispatch { return p.ExplainWith(Folding{}) }

// ExplainWith reports how planner would group the recorded operations.
func (p *Plan) ExplainWith(planner Planner) []Dispatch {
	return planner.Plan(p.Ops())
}

// Preview renders every recorded operation's argument vector without running
// anything, for the tmux version given.
//
// An entry is nil when it references an object an earlier step has not created.
//
// Other render failures return the entries completed before the error. Preview
// catches them before a non-atomic run can partially mutate tmux.
func (p *Plan) Preview(version Version) ([][]string, error) {
	render := planRenderContext{version: version}
	rendered := make([][]string, len(p.ops))
	for index := range p.ops {
		if p.awaitsEarlierStep(index) {
			continue
		}
		argv, err := p.render(index, nil, render)
		if err != nil {
			return rendered, err
		}
		rendered[index] = argv
	}
	return rendered, nil
}

func (p *Plan) awaitsEarlierStep(index int) bool {
	op := p.ops[index]
	if !op.untargets && op.target.step != 0 {
		return true
	}
	return op.source.step != 0
}

// PlanResult reports what running a [Plan] did, one entry per recorded
// operation, in the order they were recorded.
type PlanResult struct {
	// Ops holds one result per recorded operation, in the order recorded.
	Ops []OpResult
}

// OK reports whether every operation completed.
func (r PlanResult) OK() bool {
	for _, result := range r.Ops {
		if result.Status != OpComplete {
			return false
		}
	}
	return true
}

// Err returns the first failed or indeterminate operation's error, or nil when
// every operation completed.
func (r PlanResult) Err() error {
	for _, result := range r.Ops {
		if result.Status == OpFailed || result.Status == OpIndeterminate {
			return result.Err
		}
	}
	return nil
}

// OpStatus reports what became of one operation in a plan.
type OpStatus uint8

const (
	// OpComplete is an operation tmux ran successfully.
	OpComplete OpStatus = iota
	// OpFailed is an operation tmux refused. Only an operation sent alone can
	// receive this status.
	OpFailed
	// OpIndeterminate is an operation dispatched without enough reply evidence
	// to prove whether tmux applied or rejected it.
	OpIndeterminate
	// OpSkipped is an operation the plan did not dispatch because an earlier
	// dispatch failed or became indeterminate.
	OpSkipped
)

// String implements fmt.Stringer.
func (s OpStatus) String() string {
	switch s {
	case OpComplete:
		return "complete"
	case OpFailed:
		return "failed"
	case OpIndeterminate:
		return "indeterminate"
	case OpSkipped:
		return "skipped"
	default:
		return "OpStatus(" + strconv.Itoa(int(s)) + ")"
	}
}

// OpResult is what one operation in a [Plan] produced.
type OpResult struct {
	// Command is the tmux command the operation recorded.
	Command string
	// Status reports what became of the operation.
	Status OpStatus
	// Created is the ID tmux reported for an object this operation brought into
	// being, and is empty for every other operation.
	Created string
	// Stdout holds the operation's output, for an operation that captures it.
	Stdout []string
	// Err is the failure or uncertainty for a failed or indeterminate operation.
	Err error
}

// Run sends the recorded operations through server and returns one result per step.
//
// Operations that need no answer travel together in one tmux command list;
// [Plan.Explain] reports the grouping. Run stops at the first failed or
// indeterminate dispatch; later operations are [OpSkipped].
//
// Tmux refusals appear in results. Transport, context, and [PlanError] failures
// are returned. Run is not atomic; completed mutations remain after failure.
func (p *Plan) Run(ctx context.Context, server Server) (PlanResult, error) {
	return p.RunWith(ctx, server, Folding{})
}

// RunWith runs the plan with planner deciding dispatch grouping. Successful
// execution produces planner-independent results, but every operation in a
// failed grouped dispatch is [OpIndeterminate]. Use [Sequential] for exact
// attribution.
func (p *Plan) RunWith(
	ctx context.Context,
	server Server,
	planner Planner,
) (PlanResult, error) {
	results := make([]OpResult, len(p.ops))
	for index, op := range p.ops {
		results[index] = OpResult{Command: op.name, Status: OpSkipped}
	}
	state, err := server.stateForUse()
	if err != nil {
		return PlanResult{Ops: results}, err
	}
	if len(p.ops) == 0 {
		return PlanResult{Ops: results}, nil
	}
	expected, err := p.expectedDaemon()
	if err != nil {
		return PlanResult{Ops: results}, err
	}
	if expected != nil {
		server = server.withDaemon(*expected)
	}

	var version Version
	if p.needsVersion() {
		probed, err := server.Version(ctx)
		if err != nil {
			return PlanResult{Ops: results}, err
		}
		version = probed
	}
	render := planRenderContext{
		version:        version,
		unsupported:    state.config.unsupported,
		warningHandler: state.config.warningHandler,
	}

	dispatches := planner.Plan(p.Ops())
	if err := p.checkGrouping(dispatches); err != nil {
		return PlanResult{Ops: results}, err
	}

	created := map[int]string{}
	for _, dispatch := range dispatches {
		argv, err := p.renderDispatch(dispatch, created, render)
		if err != nil {
			return PlanResult{Ops: results}, err
		}
		result, err := server.dispatchCommandList(ctx, argv)
		if err != nil {
			if errors.Is(err, ErrOutcomeUnknown) {
				for _, index := range dispatch.Ops {
					results[index].Status = OpIndeterminate
					results[index].Err = err
				}
			}
			return PlanResult{Ops: results}, err
		}
		if !p.attribute(dispatch, result, results, created) {
			return PlanResult{Ops: results}, nil
		}
	}
	return PlanResult{Ops: results}, nil
}

func (p *Plan) expectedDaemon() (*snapshotServerIdentity, error) {
	var expected *snapshotServerIdentity
	for index, op := range p.ops {
		for _, ref := range []Ref{op.target, op.source} {
			if ref.daemon == nil {
				continue
			}
			if expected != nil && !sameSnapshotIdentity(*expected, *ref.daemon) {
				return nil, &PlanError{
					Step:   index,
					Reason: "references different tmux server instances",
				}
			}
			identity := *ref.daemon
			expected = &identity
		}
	}
	return expected, nil
}

// checkGrouping rejects omitted, repeated, or reordered operations before I/O.
func (p *Plan) checkGrouping(dispatches []Dispatch) error {
	expected := 0
	for _, dispatch := range dispatches {
		if len(dispatch.Ops) == 0 {
			return &PlanError{
				Step:   expected,
				Reason: "planner produced a dispatch carrying no operation",
			}
		}
		for _, index := range dispatch.Ops {
			if index != expected {
				return &PlanError{
					Step: expected,
					Reason: fmt.Sprintf(
						"planner grouped step %d here, and a plan runs its operations "+
							"in the order they were recorded", index,
					),
				}
			}
			expected++
		}
	}
	if expected != len(p.ops) {
		return &PlanError{
			Step: expected,
			Reason: fmt.Sprintf(
				"planner grouped %d of %d operations", expected, len(p.ops),
			),
		}
	}
	return nil
}

func (s Server) dispatchCommandList(
	ctx context.Context,
	argv []string,
) (CommandResult, error) {
	result, _, err := s.dispatch(ctx, true, argv...)
	return result, err
}

func (p *Plan) renderDispatch(
	dispatch Dispatch,
	created map[int]string,
	render planRenderContext,
) ([]string, error) {
	if len(dispatch.Ops) == 1 {
		return p.render(dispatch.Ops[0], created, render)
	}
	var argv []string
	for position, index := range dispatch.Ops {
		if err := p.refuseIfGrouped(index); err != nil {
			return nil, err
		}
		rendered, err := p.render(index, created, render)
		if err != nil {
			return nil, err
		}
		if position != 0 {
			argv = append(argv, ";")
		}
		argv = append(argv, rendered...)
	}
	return argv, nil
}

// refuseIfGrouped fails closed on an operation a planner put in a dispatch with
// others when tmux cannot report it separately.
//
// tmux merges a command list into one stdout, so an operation that prints an ID
// or output cannot be told apart from its neighbours; grouping one would report
// a result this package cannot stand behind. Every path that builds a command
// list asks this, marked or not.
func (p *Plan) refuseIfGrouped(index int) error {
	op := p.ops[index]
	if op.chainable() {
		return nil
	}
	prints := "prints output"
	if op.creates {
		prints = "creates an object"
	}
	return &PlanError{
		Step: index,
		Reason: fmt.Sprintf(
			"%s was grouped with other commands, but it %s, "+
				"which tmux reports without saying which command produced it",
			op.name, prints,
		),
	}
}

// attribute maps one dispatch result to its operations. A failed command list
// cannot identify its failing command, so every grouped operation is indeterminate.
func (p *Plan) attribute(
	dispatch Dispatch,
	result CommandResult,
	results []OpResult,
	created map[int]string,
) bool {
	if result.ExitCode != 0 {
		if len(dispatch.Ops) != 1 {
			err := newCommandError("command list", result)
			for _, index := range dispatch.Ops {
				results[index].Status = OpIndeterminate
				results[index].Err = err
			}
			return false
		}
		results[dispatch.Ops[0]].Status = OpFailed
		results[dispatch.Ops[0]].Err = newCommandError(p.ops[dispatch.Ops[0]].name, result)
		return false
	}
	for _, index := range dispatch.Ops {
		results[index].Status = OpComplete
	}
	if len(dispatch.Ops) != 1 {
		return true
	}

	index := dispatch.Ops[0]
	op := p.ops[index]
	if op.captures {
		results[index].Stdout = slices.Clone(result.Stdout)
	}
	if !op.creates {
		return true
	}
	id := ""
	if len(result.Stdout) != 0 {
		id = strings.TrimSpace(result.Stdout[0])
	}
	if id == "" {
		results[index].Status = OpFailed
		results[index].Err = &PlanError{
			Step:   index,
			Reason: "tmux reported no ID for the object it created",
		}
		return false
	}
	created[index] = id
	results[index].Created = id
	return true
}

// Cmd records raw tmux arguments targeted at target.
//
// It can target an object an earlier step creates. A zero [Ref] records an
// untargeted server command.
//
// The recorded command is assumed to neither print output the caller reads nor
// create an object, which is what lets it share a dispatch with its neighbours.
// Use [Plan.CmdCapture] for one whose output is the point.
func (p *Plan) Cmd(target Ref, args ...string) {
	p.add(rawOp(target, false, args))
}

// CmdCapture records raw tmux arguments whose output is returned in the step's
// [OpResult], and is otherwise [Plan.Cmd].
//
// Reading output is what stops an operation sharing a dispatch, so this is sent
// on its own; [Plan.Explain] reports that.
func (p *Plan) CmdCapture(target Ref, args ...string) {
	p.add(rawOp(target, true, args))
}

func rawOp(target Ref, captures bool, args []string) Op {
	arguments := slices.Clone(args)
	name := ""
	if len(arguments) != 0 {
		name = arguments[0]
	}
	return Op{
		name:      name,
		target:    target,
		captures:  captures,
		untargets: target == Ref{},
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			if len(arguments) == 0 {
				return nil, ErrMissingSubcommand
			}
			if resolved == "" {
				return untargetedArguments(arguments[0], arguments[1:]...)
			}
			return targetedArguments(arguments[0], resolved, arguments[1:]...)
		},
	}
}
