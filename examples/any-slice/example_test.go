package main

import (
	"testing"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
)

func TestAnySlice(t *testing.T) {
	printed := exampletest.Output(t, func() error {
		main()
		return nil
	})

	exampletest.MatchesFile(t, printed, "output.txt")
}
