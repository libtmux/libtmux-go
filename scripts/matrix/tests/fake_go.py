"""A stand-in go that records its call and fails or misbehaves on request.

Environment variables choose what it does:

- MATRIX_TEST_LOG: append the working directory of the call to this file
- MATRIX_TEST_ENV_LOG: append the TMUX_TMPDIR it saw, and the name of any
  other TMUX* or LIBTMUX_SOCKET* variable, to this file
- MATRIX_TEST_STALE_SOCKET: leave a socket in TMUX_TMPDIR
- MATRIX_TEST_FAIL: fail when the working directory is the module of this name
"""

import os
import socket
import sys
from pathlib import Path


def append(log: str, text: str) -> None:
    """Add a line to a log file."""
    with Path(log).open("a") as handle:
        handle.write(text + "\n")


def main() -> int:
    directory = Path.cwd()
    if log := os.environ.get("MATRIX_TEST_LOG"):
        append(log, str(directory))
    if env_log := os.environ.get("MATRIX_TEST_ENV_LOG"):
        for name, value in os.environ.items():
            if name == "TMUX_TMPDIR":
                append(env_log, f"tmpdir {value}")
            elif name == "LIBTMUX_TEST_TMUX":
                append(env_log, f"test_tmux {value}")
            elif name in ("TMUX", "LIBTMUX_TMUX") or name.startswith(("TMUX_", "LIBTMUX_SOCKET")):
                append(env_log, f"env {name}")
    if os.environ.get("MATRIX_TEST_STALE_SOCKET"):
        stale = socket.socket(socket.AF_UNIX)
        stale.bind(str(Path(os.environ["TMUX_TMPDIR"]) / "stale.sock"))
        stale.close()
    if os.environ.get("MATRIX_TEST_FAIL") == directory.name:
        print("--- FAIL: TestRecorded (0.00s)")
        print("    recorded_test.go:1: failed")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
