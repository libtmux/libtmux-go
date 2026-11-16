"""A time limit stops the command and every process it started."""

import ast
import os
import select
import subprocess
import sys
from pathlib import Path

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


STARTERS = {"run", "Popen", "check_output", "check_call", "call", "getoutput", "getstatusoutput"}
ASYNC_STARTERS = {"create_subprocess_exec", "create_subprocess_shell"}


def starts_outside_helper(call: ast.Call) -> bool:
    """Report whether a call starts a process without the helper's own session."""
    function = call.func
    if not (isinstance(function, ast.Attribute) and isinstance(function.value, ast.Name)):
        return False
    if function.value.id == "subprocess":
        return function.attr in STARTERS
    if function.value.id == "asyncio":
        own_session = any(keyword.arg == "start_new_session" for keyword in call.keywords)
        return function.attr in ASYNC_STARTERS and not own_session
    return False


def unbounded_starts(source: str) -> list[int]:
    """Return the lines where ``source`` starts a process without the helper.

    An asyncio start is allowed when it asks for its own session, which is what
    lets its owner kill the group.
    """
    lines = []
    for node in ast.walk(ast.parse(source)):
        if isinstance(node, ast.ImportFrom) and node.module == "subprocess":
            lines.extend(node.lineno for alias in node.names if alias.name in STARTERS)
        elif isinstance(node, ast.Call) and starts_outside_helper(node):
            lines.append(node.lineno)
    return lines


def test_the_scan_finds_an_unbounded_start() -> None:
    """The negative control: each spelling of a start outside the helper is reported."""
    assert unbounded_starts("import subprocess\nsubprocess.run(['x'])\n") == [2]
    assert unbounded_starts("from subprocess import Popen, PIPE\n") == [1]
    assert unbounded_starts("import asyncio\nasyncio.create_subprocess_exec('x')\n") == [2]
    assert unbounded_starts("import subprocess\nsubprocess.CompletedProcess\n") == []
    assert (
        unbounded_starts(
            "import asyncio\nasyncio.create_subprocess_exec('x', start_new_session=True)\n"
        )
        == []
    )


def test_no_script_starts_a_process_without_the_helper() -> None:
    """A direct ``subprocess.run`` would leave a timed-out command's children running."""
    scripts = Path(__file__).resolve().parents[2]
    found = [
        f"{path.relative_to(scripts)}:{line}"
        for path in sorted(scripts.glob("*/*.py"))
        if path.name != "bounded.py"
        for line in unbounded_starts(path.read_text())
    ]
    assert not found, found
