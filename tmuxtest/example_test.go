package tmuxtest_test

import (
	"context"
	"fmt"
	"time"

	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

func ExampleWaitFor() {
	attempts := 0
	err := tmuxtest.WaitFor(context.Background(), time.Millisecond, func(context.Context) (bool, error) {
		attempts++
		return attempts == 3, nil
	})
	fmt.Println(err)
	fmt.Println(attempts)
	// Output:
	// <nil>
	// 3
}
