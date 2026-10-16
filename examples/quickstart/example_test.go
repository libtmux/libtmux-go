package main

import (
	"testing"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
)

func TestQuickstart(t *testing.T) {
	exampletest.Program(t, run)
}
