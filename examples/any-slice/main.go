// Command any-slice filters a slice of its own with tmuxq, without tmux.
package main

import (
	"fmt"

	"github.com/libtmux/libtmux-go/tmuxq"
)

func main() {
	// docs:tmuxq-slice
	type task struct {
		name string
		done bool
	}
	tasks := []task{{"build", true}, {"test", false}, {"deploy", false}}
	open := func(t *task) bool { return !t.done }

	for _, t := range tmuxq.Where(tasks, open) {
		fmt.Println("open:", t.name)
	}
	first, _ := tmuxq.First(tasks, open)
	fmt.Println("first open:", first.name)
	// docs:end

	// Two tasks are open, so asking for the only one is an error.
	_, err := tmuxq.ExactlyOne(tasks, open)
	fmt.Println("the only open task:", err)
}
