"""Run external commands under a time limit that also stops the processes they start."""

import contextlib
import os
import signal
import subprocess
import threading
from collections.abc import Callable, Mapping, Sequence
from pathlib import Path
from typing import Any

# Seconds a command that only prints a version may run. README.md records what
# it sits above.
PROBE = 10

type Command = Sequence[str | os.PathLike[str]]


def stop_group(pid: int) -> None:
    """Kill every process in the process group a bounded command leads."""
    with contextlib.suppress(ProcessLookupError):
        os.killpg(pid, signal.SIGKILL)


def run(
    args: Command,
    *,
    timeout: float,
    check: bool = False,
    capture_output: bool = False,
    text: bool = False,
    errors: str | None = None,
    cwd: Path | None = None,
    env: Mapping[str, str] | None = None,
    stdout: int | None = None,
    stderr: int | None = None,
    pass_fds: Sequence[int] = (),
) -> subprocess.CompletedProcess[Any]:
    """Run ``args`` like ``subprocess.run``, in its own process group.

    A timeout, an interrupt or any other error kills the whole group, so a test
    binary below ``go`` cannot outlive the limit. ``subprocess.run`` kills only
    the direct child.
    """
    if capture_output:
        stdout = stderr = subprocess.PIPE
    with subprocess.Popen(
        args,
        start_new_session=True,
        text=text,
        errors=errors,
        cwd=cwd,
        env=env,
        stdout=stdout,
        stderr=stderr,
        pass_fds=pass_fds,
    ) as process:
        try:
            out, err = process.communicate(timeout=timeout)
        except BaseException:
            stop_group(process.pid)
            raise
    result = subprocess.CompletedProcess(args, process.returncode, out, err)
    if check:
        result.check_returncode()
    return result


def stream(
    args: Command,
    *,
    timeout: float,
    on_line: Callable[[str], None],
    cwd: Path | None = None,
    env: Mapping[str, str] | None = None,
    pass_fds: Sequence[int] = (),
) -> tuple[int, list[str]]:
    """Run ``args`` in its own process group, hand each output line over as it arrives.

    Return the exit status and every line. Standard error is not captured. After
    ``timeout`` seconds the whole group is killed and ``TimeoutExpired`` raised.
    """
    process = subprocess.Popen(
        args,
        cwd=cwd,
        env=env,
        stdout=subprocess.PIPE,
        text=True,
        errors="replace",
        start_new_session=True,
        pass_fds=pass_fds,
    )
    expired = threading.Event()

    def expire() -> None:
        expired.set()
        stop_group(process.pid)

    timer = threading.Timer(timeout, expire)
    timer.start()
    lines: list[str] = []
    try:
        assert process.stdout is not None
        for line in process.stdout:
            on_line(line)
            lines.append(line)
    except BaseException:
        stop_group(process.pid)
        raise
    finally:
        timer.cancel()
        status = process.wait()
    if expired.is_set():
        raise subprocess.TimeoutExpired(args, timeout)
    return status, lines
