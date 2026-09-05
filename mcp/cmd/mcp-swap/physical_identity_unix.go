//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"fmt"
	"syscall"
)

func physicalIdentityAt(path string, follow bool) (physicalIdentity, error) {
	var info syscall.Stat_t
	var err error
	if follow {
		err = syscall.Stat(path, &info)
	} else {
		err = syscall.Lstat(path, &info)
	}
	if err != nil {
		return physicalIdentity{}, err
	}
	return physicalIdentity{
		Device: fmt.Sprint(uint64(info.Dev)),
		File:   fmt.Sprint(uint64(info.Ino)),
	}, nil
}
