// Command any-slice filters a slice of its own with tmuxq, without tmux.
package main

import (
	"fmt"

	"github.com/libtmux/libtmux-go/tmuxq"
)

type task struct {
	name string
	done bool
}

func main() {
	tasks := []task{{"build", true}, {"test", false}, {"deploy", false}}
	open := func(task *task) bool { return !task.done }

	for _, task := range tmuxq.Where(tasks, open) {
		fmt.Println("open:", task.name)
	}
	first, _ := tmuxq.First(tasks, open)
	fmt.Println("first open:", first.name)

	// Two tasks are open, so asking for the only one is an error.
	_, err := tmuxq.ExactlyOne(tasks, open)
	fmt.Println("the only open task:", err)
}
