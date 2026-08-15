//go:build linux

package tmuxtest

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

func preparePTYProcessCommand(command *exec.Cmd) (*os.File, *os.File, error) {
	master, slave, err := openPTYProcessTerminal()
	if err != nil {
		return nil, nil, err
	}
	command.Stdin = slave
	command.Stdout = slave
	command.Stderr = slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	return master, slave, nil
}

type ptyProcessWindowSize struct {
	rows    uint16
	columns uint16
	xpixels uint16
	ypixels uint16
}

func openPTYProcessTerminal() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	locked := int32(0)
	if err := ptyProcessIOCTL(master, syscall.TIOCSPTLCK, unsafe.Pointer(&locked)); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	var number uint32
	if err := ptyProcessIOCTL(master, syscall.TIOCGPTN, unsafe.Pointer(&number)); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	slavePath := "/dev/pts/" + strconv.FormatUint(uint64(number), 10)
	slave, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	size := ptyProcessWindowSize{rows: 24, columns: 80}
	if err := ptyProcessIOCTL(master, syscall.TIOCSWINSZ, unsafe.Pointer(&size)); err != nil {
		_ = slave.Close()
		_ = master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

func ptyProcessIOCTL(file *os.File, request uintptr, argument unsafe.Pointer) error {
	connection, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var ioctlErr error
	if err := connection.Control(func(fd uintptr) {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(argument))
		if errno != 0 {
			ioctlErr = errno
		}
	}); err != nil {
		return err
	}
	return ioctlErr
}
