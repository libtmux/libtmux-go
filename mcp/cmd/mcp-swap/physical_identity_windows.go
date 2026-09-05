//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

func physicalIdentityAt(path string, _ bool) (physicalIdentity, error) {
	file, err := os.Open(path)
	if err != nil {
		return physicalIdentity{}, err
	}
	defer file.Close()
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return physicalIdentity{}, err
	}
	return physicalIdentity{
		Device: fmt.Sprint(info.VolumeSerialNumber),
		File:   fmt.Sprintf("%08x%08x", info.FileIndexHigh, info.FileIndexLow),
	}, nil
}
