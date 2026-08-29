// Package exampletest captures output from executable examples.
package exampletest

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// Output returns work's stdout and fails with partial output when work fails.
func Output(t *testing.T, work func() error) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("exampletest: create pipe: %v", err)
	}
	os.Stdout = writer

	// Drain concurrently so output larger than the pipe buffer cannot block.
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
