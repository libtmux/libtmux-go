//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package main

import "errors"

func physicalIdentityAt(string, bool) (physicalIdentity, error) {
	return physicalIdentity{}, errors.New("physical file identity is unsupported on this platform")
}
