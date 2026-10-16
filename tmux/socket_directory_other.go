//go:build !unix

package tmux

import "errors"

func prepareNamedSocketDirectory(string) error {
	return errors.New("prepare tmux socket directory: named sockets require Unix")
}
