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
}

// Ref returns a [Ref] addressing the receiver.
func (s Session) Ref() Ref { return SessionRef(s.sessionID) }

// Ref returns a [Ref] addressing the receiver.
func (w Window) Ref() Ref { return WindowRef(w.windowID) }

// Ref returns a [Ref] addressing the receiver.
func (p Pane) Ref() Ref { return PaneRef(p.paneID) }

// SessionRef returns a [Ref] addressing a session by ID, for a caller holding
// an identifier rather than a record.
func SessionRef(id SessionID) Ref { return Ref{target: id.String()} }

// WindowRef returns a [Ref] addressing a window by ID, for a caller holding an
// identifier rather than a record.
func WindowRef(id WindowID) Ref { return Ref{target: id.String()} }

// PaneRef returns a [Ref] addressing a pane by ID, for a caller holding an
// identifier rather than a record.
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
	// build renders without I/O from resolved targets and the tmux version.
	build func(target, source string, version Version) ([]string, error)
	// creates means stdout contains an ID for later Refs.
	creates   bool
	captures  bool
	untargets bool
	// marks means a creation leaves its pane active for tmux's {marked} register.
	marks bool
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
	// unsupported is the policy the run applies to a step naming a capability
	// the running tmux does not have. [Plan.RunWith] sets it from the server it
	// was given; [Plan.Preview] leaves the zero value, so a preview refuses
	// what a default server would refuse, which is what a preview is for.
	unsupported UnsupportedPolicy
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
	version Version,
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
	argv, err := op.build(target, source, version)
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
	// Marked reports that this dispatch names the object its first operation
	// creates through tmux's {marked} register, so the operations after it can
	// share the command list rather than waiting for the created ID.
	Marked bool
	// Reason says why the dispatch ends where it does. It is "chained" for a
	// command list, "creates" for an operation whose new object's ID a later
	// step needs, "captures" for one whose output the caller reads, "alone" for
	// a chainable operation with nothing beside it to chain to, and "marked"
	// for a creation carrying the operations that decorate it.
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

// foldFrom is shared by Folding and Marked for unmarked operations.
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

// Marked groups an active pane creation with operations that reference it. It
// marks the new pane, addresses it as {marked}, then clears the mark. Detached
// creations run alone; everything else follows [Folding].
type Marked struct{}

// Plan returns the dispatches for ops, folding a marked creation with the
// operations that name it.
func (Marked) Plan(ops []Op) []Dispatch {
	var dispatches []Dispatch
	for index := 0; index < len(ops); {
		var dispatch Dispatch
		if decorates := markedDecorates(ops, index); len(decorates) != 0 {
			dispatch = Dispatch{
				Ops:    append([]int{index}, decorates...),
				Marked: true,
				Reason: "marked",
			}
		} else {
			dispatch = foldFrom(ops, index)
		}
		dispatches = append(dispatches, dispatch)
		index = dispatch.Ops[len(dispatch.Ops)-1] + 1
	}
	return dispatches
}

func markedDecorates(ops []Op, index int) []int {
	creator := ops[index]
	if !creator.creates || !creator.marks {
		return nil
	}
	var decorates []int
	for cursor := index + 1; cursor < len(ops); cursor++ {
		op := ops[cursor]
		if !op.chainable() || op.target != (Ref{step: index + 1}) {
			break
		}
		decorates = append(decorates, cursor)
	}
	return decorates
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
	rendered := make([][]string, len(p.ops))
	for index := range p.ops {
		if p.awaitsEarlierStep(index) {
			continue
		}
		argv, err := p.render(index, nil, version)
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

// Err returns the first failed operation's error, or nil when every operation
// completed.
func (r PlanResult) Err() error {
	for _, result := range r.Ops {
		if result.Status == OpFailed {
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
	// OpFailed is an operation tmux refused, or the first operation of a
	// dispatch one of whose commands tmux refused. tmux reports one status for
	// a command list without naming the command that produced it, so this is
	// exact only for a dispatch carrying a single operation, which the plan's
	// Explain method reports.
	OpFailed
	// OpSkipped is an operation tmux never saw. tmux abandons the rest of a
	// command list once one of its commands fails, and a plan stops at a failed
	// dispatch rather than sending later ones.
	OpSkipped
)

// String implements fmt.Stringer.
func (s OpStatus) String() string {
	switch s {
	case OpComplete:
		return "complete"
	case OpFailed:
		return "failed"
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
	// Err is the failure, for a failed operation.
	Err error
}

// Run sends the recorded operations through server and returns one result per step.
//
// Operations that need no answer travel together in one tmux command list;
// [Plan.Explain] reports the grouping. Run stops at the first failed dispatch;
// later operations are [OpSkipped].
//
// Tmux refusals appear in results. Transport, context, and [PlanError] failures
// are returned. Run is not atomic; completed mutations remain after failure.
func (p *Plan) Run(ctx context.Context, server Server) (PlanResult, error) {
	return p.RunWith(ctx, server, Folding{})
}

// RunWith runs the plan with planner deciding how operations are grouped.
//
// The results are the same whichever planner is used; only the number of tmux
// invocations changes, which is what makes two planners comparable. Passing
// [Sequential] is how a caller isolates a failure that grouping made ambiguous,
// since a dispatch carrying one operation attributes exactly.
func (p *Plan) RunWith(
	ctx context.Context,
	server Server,
	planner Planner,
) (PlanResult, error) {
	p.unsupported = server.connectionState().options.Unsupported
	results := make([]OpResult, len(p.ops))
	for index, op := range p.ops {
		results[index] = OpResult{Command: op.name, Status: OpSkipped}
	}
	if len(p.ops) == 0 {
		return PlanResult{Ops: results}, nil
	}

	var version Version
	if p.needsVersion() {
		probed, err := server.Version(ctx)
		if err != nil {
			return PlanResult{Ops: results}, err
		}
		version = probed
	}

	dispatches := planner.Plan(p.Ops())
	if err := p.checkGrouping(dispatches); err != nil {
		return PlanResult{Ops: results}, err
	}

	created := map[int]string{}
	for _, dispatch := range dispatches {
		argv, err := p.renderDispatch(dispatch, created, version)
		if err != nil {
			results[dispatch.Ops[0]].Status = OpFailed
			results[dispatch.Ops[0]].Err = err
			return PlanResult{Ops: results}, err
		}
		result, err := server.dispatchCommandList(ctx, argv)
		if err != nil {
			results[dispatch.Ops[0]].Status = OpFailed
			results[dispatch.Ops[0]].Err = err
			return PlanResult{Ops: results}, err
		}
		if !p.attribute(dispatch, result, results, created) {
			return PlanResult{Ops: results}, nil
		}
	}
	return PlanResult{Ops: results}, nil
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
	version Version,
) ([]string, error) {
	if dispatch.Marked {
		return p.renderMarked(dispatch, created, version)
	}
	if len(dispatch.Ops) == 1 {
		return p.render(dispatch.Ops[0], created, version)
	}
	var argv []string
	for position, index := range dispatch.Ops {
		if err := p.refuseIfGrouped(index); err != nil {
			return nil, err
		}
		rendered, err := p.render(index, created, version)
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

// renderMarked addresses the new pane through {marked}, clears the mark, and
// resolves any source Ref normally. Grouped operations must remain chainable.
func (p *Plan) renderMarked(
	dispatch Dispatch,
	created map[int]string,
	version Version,
) ([]string, error) {
	creation := p.ops[dispatch.Ops[0]]
	if !creation.creates || !creation.marks {
		return nil, &PlanError{
			Step: dispatch.Ops[0],
			Reason: fmt.Sprintf(
				"dispatch is marked, but %s does not leave a new pane active "+
					"for select-pane -m to name", creation.name,
			),
		}
	}
	argv, err := p.render(dispatch.Ops[0], created, version)
	if err != nil {
		return nil, err
	}
	argv = append(argv, ";", "select-pane", "-m")
	for _, index := range dispatch.Ops[1:] {
		if err := p.refuseIfGrouped(index); err != nil {
			return nil, err
		}
		op := p.ops[index]
		source := ""
		if op.source != (Ref{}) {
			resolved, err := p.resolve(op.source, created, index)
			if err != nil {
				return nil, err
			}
			source = resolved
		}
		rendered, err := op.build("{marked}", source, version)
		if err != nil {
			return nil, err
		}
		argv = append(argv, ";")
		argv = append(argv, rendered...)
	}
	return append(argv, ";", "select-pane", "-M"), nil
}

// attribute maps a successful dispatch to every operation. A failed command
// list cannot identify its failing command, so it fails the first and skips the rest.
func (p *Plan) attribute(
	dispatch Dispatch,
	result CommandResult,
	results []OpResult,
	created map[int]string,
) bool {
	if result.ExitCode != 0 {
		results[dispatch.Ops[0]].Status = OpFailed
		results[dispatch.Ops[0]].Err = newCommandError(p.ops[dispatch.Ops[0]].name, result)
		return false
	}
	for _, index := range dispatch.Ops {
		results[index].Status = OpComplete
	}
	if len(dispatch.Ops) != 1 && !dispatch.Marked {
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
		build: func(resolved, _ string, _ Version) ([]string, error) {
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
