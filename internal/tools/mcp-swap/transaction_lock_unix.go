//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func openAndLockTransactionFile(path string, create bool) (*os.File, bool, error) {
	flags := syscall.O_RDWR | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	if create {
		flags |= syscall.O_CREAT | syscall.O_EXCL
	}
	descriptor, err := syscall.Open(path, flags, transactionLockMode)
	if err != nil {
		return nil, false, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	if err := syscall.Flock(descriptor, syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, false, err
	}
	return file, create, nil
}

func unlockAndCloseTransactionFile(file *os.File) error {
	return errors.Join(
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN),
		file.Close(),
	)
}

func physicalIdentityForFile(file *os.File) (physicalIdentity, error) {
	var info syscall.Stat_t
	if err := syscall.Fstat(int(file.Fd()), &info); err != nil {
		return physicalIdentity{}, err
	}
	return physicalIdentity{
		Device: fmt.Sprint(info.Dev),
		File:   fmt.Sprint(info.Ino),
	}, nil
}

func fileLinkCount(info os.FileInfo) (uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("transaction lock has unsupported file metadata")
	}
	return uint64(stat.Nlink), nil //nolint:unconvert // Stat_t widths vary by Unix.
}

func fileOwnedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Uid) == uint64(os.Geteuid())
}
