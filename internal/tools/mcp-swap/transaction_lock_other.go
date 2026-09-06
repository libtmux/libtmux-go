//go:build aix || solaris || windows || (!darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd)

package main

import (
	"errors"
	"os"
)

func openAndLockTransactionFile(string, bool) (*os.File, bool, error) {
	return nil, false, errors.New("transaction locking is unsupported on this platform")
}

func unlockAndCloseTransactionFile(file *os.File) error { return file.Close() }

func physicalIdentityForFile(*os.File) (physicalIdentity, error) {
	return physicalIdentity{}, errors.New("transaction locking is unsupported on this platform")
}

func fileLinkCount(os.FileInfo) (uint64, error) {
	return 0, errors.New("transaction locking is unsupported on this platform")
}

func fileOwnedByCurrentUser(os.FileInfo) bool { return false }
