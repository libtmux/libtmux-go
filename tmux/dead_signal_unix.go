//go:build unix

package tmux

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// signalNames holds the names tmux reports for a signal where the C library
// names them, as on macOS and the BSDs. Elsewhere, Linux among them, tmux
// reports the number instead.
var signalNames = map[string]syscall.Signal{
	"abrt":   syscall.SIGABRT,
	"alrm":   syscall.SIGALRM,
	"bus":    syscall.SIGBUS,
	"chld":   syscall.SIGCHLD,
	"cont":   syscall.SIGCONT,
	"fpe":    syscall.SIGFPE,
	"hup":    syscall.SIGHUP,
	"ill":    syscall.SIGILL,
	"int":    syscall.SIGINT,
	"io":     syscall.SIGIO,
	"kill":   syscall.SIGKILL,
	"pipe":   syscall.SIGPIPE,
	"prof":   syscall.SIGPROF,
	"quit":   syscall.SIGQUIT,
	"segv":   syscall.SIGSEGV,
	"stop":   syscall.SIGSTOP,
	"sys":    syscall.SIGSYS,
	"term":   syscall.SIGTERM,
	"trap":   syscall.SIGTRAP,
	"tstp":   syscall.SIGTSTP,
	"ttin":   syscall.SIGTTIN,
	"ttou":   syscall.SIGTTOU,
	"urg":    syscall.SIGURG,
	"usr1":   syscall.SIGUSR1,
	"usr2":   syscall.SIGUSR2,
	"vtalrm": syscall.SIGVTALRM,
	"winch":  syscall.SIGWINCH,
	"xcpu":   syscall.SIGXCPU,
	"xfsz":   syscall.SIGXFSZ,
}

// platformSignal returns the signal tmux reported, by number or by name.
func platformSignal(reported string) (os.Signal, bool) {
	if number, err := strconv.Atoi(reported); err == nil && number > 0 {
		return syscall.Signal(number), true
	}
	if signal, ok := signalNames[strings.ToLower(reported)]; ok {
		return signal, true
	}
	return nil, false
}
