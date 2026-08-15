// Package exampletest gives each example's test the output that example
// printed.
//
// An example's output is what it claims to show. A test that only checks the
// error it returned asserts that the program did not crash, which every example
// would pass while printing nothing at all.
package exampletest

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// Output runs work with os.Stdout replaced by a pipe and returns everything
// printed to it. It fails the test if work returns an error, quoting whatever
// had been printed by then, because that is usually the part of the example
// that got far enough to say something.
func Output(t *testing.T, work func() error) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("exampletest: create pipe: %v", err)
	}
	os.Stdout = writer

	// Drained on another goroutine: a pipe holds a fixed amount, and an example
	// that printed more than that would block partway through instead of
	// finishing.
	var printed bytes.Buffer
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(&printed, reader)
	}()

	workErr := work()

	os.Stdout = original
	if err := writer.Close(); err != nil {
		t.Fatalf("exampletest: close writer: %v", err)
	}
	<-drained
	if err := reader.Close(); err != nil {
		t.Fatalf("exampletest: close reader: %v", err)
	}

	if workErr != nil {
		t.Fatalf("run() error = %v; printed so far:\n%s", workErr, printed.String())
	}
	return printed.String()
}
