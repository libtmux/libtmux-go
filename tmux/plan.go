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

// Ref addresses a tmux object a [Plan] will act on. It is either an object that
// already exists, from [Session.Ref], [Window.Ref], or [Pane.Ref], or the one a
// recorded step is going to create, from the [Plan] method that recorded it.
//
// A ref to something that does not exist yet is what lets a plan be built in
// one pass: a split can be recorded, and keys sent to the pane it will create,
// before tmux has been asked for anything.
//
// The zero Ref addresses nothing, and a plan holding one refuses to run rather
// than guessing what was meant.
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
	// name is the tmux command, used in explanations and errors.
	name string
	// target is what the command acts on, resolved when the plan runs.
	target Ref
	// source is the second object a command that moves something between two
	// places reads from, as tmux's -s is to its -t. It is the zero Ref for the
	// commands that name only one object.
	source Ref
	// build renders the command's argument vector. It receives the resolved
	// target, the resolved source, and the tmux version, and must not perform
	// I/O: a plan renders itself for Preview without a server.
	build func(target, source string, version Version) ([]string, error)
	// creates reports that the command prints the ID of an object it brought
	// into being, which is what a later step targets through its [Ref]. Which
	// kind of object it is does not matter here: the ID names it.
	creates bool
	// captures reports that the command prints output the caller reads.
	captures bool
	// untargets reports that the command acts on the server rather than on an
	// object, so it takes no -t and its target Ref is not consulted.
	untargets bool
	// marks reports that the command leaves the object it created as the active
	// pane, so tmux's {marked} register can name it inside the same command
	// list. A creation that detaches does not, and [Marked] leaves it alone.
	marks bool
	// needsVersion reports that build consults the tmux version, so a plan
	// containing this operation has to probe for it. Most do not, and probing
	// costs a tmux process of its own -- which is the cost a plan exists to
	// avoid, so it is not paid unless an operation asks.
	needsVersion bool
}

// Chainable reports whether the operation may share a tmux invocation with
// others, which is what a [Planner] groups on.
func (o Op) Chainable() bool { return o.chainable() }

// Command returns the tmux command the operation records.
func (o Op) Command() string { return o.name }

// chainable reports whether the operation may share a dispatch with others.
//
// tmux merges a command list into one stdout with no boundary between the
// commands, so an operation whose result is its output cannot be told apart
// from its neighbours. Those dispatch alone. That covers a creation too: the ID
// it prints is what a later step targets.
func (o Op) chainable() bool { return !o.captures && !o.creates }

// Plan records tmux commands without running them, then runs them together.
//
// A plan exists for two reasons. It cuts the cost of a build: commands that
// need no answer travel in one tmux command list rather than one tmux process
// each, which is most of the cost of creating a workspace. And it lets a build
// be written in one pass, because a step can address what an earlier step is
// going to create before tmux has created it.
//
// Recording touches nothing. [Plan.Preview] renders what would be sent and
// [Plan.Explain] says how it would be grouped, both without a server;
// [Plan.Run] is the only method that reaches tmux.
//
// A Plan is not safe for concurrent use. Build one from a single goroutine and
// run it; the commands inside it are ordered, so there is nothing to gain by
// sharing it.
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

// needsVersion reports whether any recorded operation renders differently by
// tmux version, and so requires the probe that learns it.
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

// add records one operation and returns a [Ref] to what it creates. The ref is
// meaningful only when the operation creates something; for the rest it names a
// step whose target no later step can address.
func (p *Plan) add(op Op) Ref {
	p.ops = append(p.ops, op)
	return Ref{step: len(p.ops)}
}

// resolve returns the concrete tmux target a Ref names, given what earlier
// steps produced.
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

// render returns the argument vector for one operation.
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
		// Which step is what a caller needs, and the error a request builder
		// returns cannot know it: the same builder serves the method that runs
		// the command immediately, where there is no step to name. Wrapping
		// keeps errors.Is and errors.As reaching the reason underneath.
		return nil, fmt.Errorf("step %d: %s: %w", index, op.name, err)
	}
	return argv, nil
}

// targetedArguments renders the shape nearly every tmux command takes: the
// command, the object it acts on, and whatever flags follow. It is the same
// argument vector the object API builds when it sends the command itself.
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

// untargetedArguments renders a tmux command that acts on the server rather
// than on an object, so it takes no target.
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

// Planner decides how a plan's operations are grouped into tmux invocations.
//
// It is pure policy: the same operations produce the same results whichever
// planner groups them, and only the number of times tmux is invoked changes.
// Planners are values rather than names in a registry, so selecting one is a
// compiler-checked expression and a caller can supply their own.
//
// A planner decides how many tmux invocations there are. It does not decide
// what runs: its dispatches must carry every operation exactly once, in the
// order it was recorded, because reordering two tmux commands changes what they
// do. [Plan.Run] refuses a grouping that does not, before anything reaches
// tmux.
//
// A planner must also not group an operation whose [Op.Chainable] is false.
// tmux answers a command list with one merged stdout, so grouping one that
// prints something would attribute its output to whichever operation the plan
// asked about; [Plan.Run] refuses such a dispatch rather than reporting a
// result it cannot stand behind.
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

// foldFrom returns the one dispatch [Folding] makes starting at index: the run
// of operations from there that may share a tmux invocation, or the single
// operation that may not. [Marked] uses it for every step it does not mark, so
// a change to what folds reaches both planners.
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

// Marked folds a pane creation together with the operations that decorate it,
// which [Folding] cannot: those operations name the new pane, and its ID is not
// known until the creation has run.
//
// It uses tmux's own answer. The creation leaves its pane active, so
// select-pane -m marks it, the operations after it address {marked}, and a
// final select-pane -M clears the mark -- all in one command list. Everything
// else folds as [Folding] does.
//
// It applies only to a creation that leaves its new pane active. A detached one
// does not, so marking would name whichever pane was already active, and those
// creations are left to dispatch alone.
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

// markedDecorates returns the operations following index that name the pane it
// creates, and can therefore address it through {marked}.
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
// Grouping hides which operation produced what, so this is how a caller sees
// the shape of what will be sent: the number of dispatches is the number of
// times tmux is invoked.
func (p *Plan) Explain() []Dispatch { return p.ExplainWith(Folding{}) }

// ExplainWith reports how planner would group the recorded operations.
func (p *Plan) ExplainWith(planner Planner) []Dispatch {
	return planner.Plan(p.Ops())
}

// Preview renders every recorded operation's argument vector without running
// anything, for the tmux version given.
//
// An entry is nil when the operation names an object an earlier step has not
// created yet. That ID does not exist until the plan runs, and being able to
// write the step anyway is what a plan is for, so it is not an error.
//
// Everything else that would stop an operation rendering is one, and is
// returned: arguments tmux would refuse, and the zero [Ref], which addresses
// nothing. Catching those here is the point. A plan is not atomic, so an
// argument only rejected at step seven is rejected after six steps have already
// changed tmux; this is where that is found instead. The entries rendered
// before the error are returned with it.
func (p *Plan) Preview(version Version) ([][]string, error) {
	rendered := make([][]string, len(p.ops))
	for index := range p.ops {
		if p.awaitsEarlierStep(index) {
			continue
		}
		// No step has run, so nothing has reported an ID. The steps that would
		// have needed one were skipped above.
		argv, err := p.render(index, nil, version)
		if err != nil {
			return rendered, err
		}
		rendered[index] = argv
	}
	return rendered, nil
}

// awaitsEarlierStep reports that the operation names an object a step before it
// is going to create. Nothing has run, so no step has reported an ID, and a ref
// to one cannot resolve until it does.
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

// Run sends the recorded operations to tmux through server and returns one
// result per operation.
//
// Operations that need no answer travel together in one tmux command list;
// [Plan.Explain] reports that grouping ahead of time. tmux abandons a command
// list at its first failure, so Run stops there too: the failed operation
// carries the error and every operation after it is [OpSkipped].
//
// A tmux refusal is in the result rather than in a returned error, the way it
// is everywhere else in this package. A returned error is something else: a
// transport or context failure, or a [PlanError] for a plan that could not run
// as recorded, which [Plan.Preview] would have reported first.
//
// Run is not atomic, because tmux has no transaction. What ran before a failure
// stays; the results say exactly how far it got.
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

// checkGrouping reports a planner that did not carry every operation exactly
// once, in the order they were recorded.
//
// [Planner] is an extension point, so a caller's own planner decides what runs
// as much as this package's do. A grouping that skips a step, repeats one, or
// reorders them is a mistake in that planner, and one this package can see
// before anything reaches tmux -- so it says so, rather than indexing past the
// end of its own results or running a plan that means something the caller did
// not write.
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

// dispatchCommandList runs one plan dispatch. A step holding several operations is
// a command list, which every transport renders from a standalone ";".
func (s Server) dispatchCommandList(
	ctx context.Context,
	argv []string,
) (CommandResult, error) {
	result, _, err := s.dispatch(ctx, true, argv...)
	return result, err
}

// renderDispatch renders one dispatch, joining a multi-operation one with the
// standalone ";" that separates two tmux commands.
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

// renderMarked renders a creation and the operations naming its new pane as one
// command list, addressing that pane through tmux's {marked} register.
//
// The mark is set after the creation, which left its pane active, and cleared
// at the end, so the register is not left pointing at a pane the caller did not
// ask to mark.
//
// Only the target is replaced. An operation that names a second object still
// resolves that one the ordinary way, because {marked} names the pane this
// dispatch created and the other object is somewhere else entirely.
//
// A marked dispatch is still a command list, so the operations riding with the
// creation are held to what any grouped operation is held to. The creation
// itself has to be one that leaves its new pane active, or there is nothing for
// select-pane -m to mark and the register would name whichever pane already
// was.
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

// attribute records one dispatch's outcome against the operations it carried,
// and reports whether the plan may continue.
//
// A dispatch that succeeded is unambiguous: tmux ran every command in it.
//
// A failed one is not. tmux answers a command list with a single exit status
// and one merged stdout, and says nothing about which of its commands failed,
// so the operations grouped into that dispatch cannot be told apart. This
// blames the first and skips the rest, which is the outcome shaped like what a
// caller can act on -- the plan stopped, here is the tmux error, nothing after
// this point ran. It is exact whenever the dispatch held one operation, which
// is every creation and every read; [Plan.Explain] reports which dispatches
// those are.
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

// Cmd records raw tmux arguments targeted at the object target names, the way
// [Session.Cmd], [Window.Cmd], and [Pane.Cmd] send them immediately.
//
// It is the escape hatch: a tmux command this package has no recorder for can
// still be part of a plan, and a [Ref] still names what it acts on, so it can
// target something an earlier step created. Pass the zero [Ref] for a command
// that names no object.
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

// rawOp records one raw tmux command, targeted or not.
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
