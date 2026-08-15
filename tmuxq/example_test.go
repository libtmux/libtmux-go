package tmuxq_test

import (
	"errors"
	"fmt"
	"slices"

	"github.com/tmux-python/libtmux/golang/tmuxq"
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
