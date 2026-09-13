//go:build !unix

package cli

import (
	"bufio"
	"context"
	"os"
)

func promptFileLine(ctx context.Context, input *os.File) (string, error) {
	return bufio.NewReader(&promptInput{ctx: ctx, input: input}).ReadString('\n')
}
