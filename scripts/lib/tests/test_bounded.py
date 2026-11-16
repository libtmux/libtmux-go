"""A time limit stops the command and every process it started."""

import os
import select
import subprocess
import sys

import pytest

import bounded

# Starts a sleeping child that inherits the descriptor named in argv, then sleeps itself.
PARENT = """
import subprocess, sys, time
inherited = int(sys.argv[1])
subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"], pass_fds=[inherited])
print("started", flush=True)
time.sleep(60)
"""


def children_died(reader: int, writer: int) -> bool:
    """Report whether every holder of the pipe's write end, bar ours, has gone."""
    os.close(writer)
    ready, _, _ = select.select([reader], [], [], 5)
    return bool(ready) and os.read(reader, 1) == b""


def test_a_timeout_stops_the_children_of_the_command() -> None:
    """Both processes hold the pipe's write end; it reaches end-of-file only if both die."""
    reader, writer = os.pipe()
    try:
        with pytest.raises(subprocess.TimeoutExpired):
            bounded.run([sys.executable, "-c", PARENT, str(writer)], timeout=0.5, pass_fds=[writer])
        assert children_died(reader, writer), "a child of the timed-out command is still running"
    finally:
        os.close(reader)


def test_a_streamed_command_echoes_lines_and_a_timeout_stops_its_children() -> None:
    """The line printed before the limit arrives; the limit then kills the group."""
    reader, writer = os.pipe()
    seen: list[str] = []
    try:
        with pytest.raises(subprocess.TimeoutExpired):
            bounded.stream(
                [sys.executable, "-c", PARENT, str(writer)],
                timeout=0.5,
                on_line=seen.append,
                pass_fds=[writer],
            )
        assert seen == ["started\n"]
        assert children_died(reader, writer), "a child of the timed-out command is still running"
    finally:
        os.close(reader)
