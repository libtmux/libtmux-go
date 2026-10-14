package main

import (
	"testing"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
)

func TestQueries(t *testing.T) {
	exampletest.Program(t, run)
}
