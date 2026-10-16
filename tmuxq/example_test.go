package tmuxq_test

import (
	"errors"
	"fmt"
	"slices"

	"github.com/libtmux/libtmux-go/tmuxq"
)

type pane struct {
	name   string
	active bool
}

func Example() {
	panes := []pane{
		{name: "editor", active: true},
		{name: "logs", active: false},
		{name: "shell", active: true},
	}

	active := tmuxq.Where(panes, func(pane *pane) bool { return pane.active })
	for _, pane := range active {
		fmt.Println(pane.name)
	}

	// Output:
	// editor
	// shell
}

// activeOnly is a filter: anything that can compile itself into a predicate.
// The tmux package generates one for each of its models.
type activeOnly struct{}

func (activeOnly) Predicate() (func(*pane) bool, error) {
	return func(pane *pane) bool { return pane.active }, nil
}

func ExampleMatching() {
	panes := []pane{
		{name: "editor", active: true},
		{name: "logs", active: false},
		{name: "shell", active: true},
	}

	active, err := tmuxq.Matching(panes, activeOnly{})
	if err != nil {
		fmt.Println("invalid filter:", err)
		return
	}
	for _, pane := range active {
		fmt.Println(pane.name)
	}

	// Output:
	// editor
	// shell
}

func ExampleExactlyOne() {
	noActivePanes := []pane{{name: "editor"}, {name: "logs"}}
	_, err := tmuxq.ExactlyOne(noActivePanes, func(pane *pane) bool {
		return pane.active
	})
	if errors.Is(err, tmuxq.ErrNoMatch) {
		fmt.Println("no active pane")
	}

	multipleActivePanes := []pane{
		{name: "editor", active: true},
		{name: "shell", active: true},
	}
	_, err = tmuxq.ExactlyOne(multipleActivePanes, func(pane *pane) bool {
		return pane.active
	})
	if errors.Is(err, tmuxq.ErrMultipleMatches) {
		fmt.Println("multiple active panes")
	}

	// Output:
	// no active pane
	// multiple active panes
}

func ExampleWhereSeq() {
	panes := []pane{
		{name: "editor", active: true},
		{name: "logs", active: false},
		{name: "shell", active: true},
	}

	for pane := range tmuxq.WhereSeq(slices.Values(panes), func(pane *pane) bool {
		return pane.active
	}) {
		fmt.Println(pane.name)
	}

	// Output:
	// editor
	// shell
}
