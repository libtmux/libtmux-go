package main

import "testing"

func TestLifecycleProgram(t *testing.T) {
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
