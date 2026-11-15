#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Run one module's tests, qualifying fixed-output examples by tmux release.

    run_tests.py MODULE [go test flags]

MODULE is `.`, `examples`, `workspace`, `mcp`, `benchmarks` or `internal/tools`.
An `Example` function has no `t.Skip`, so a release that lacks the feature an
example prints is named to `go test -skip` instead. The tmux that decides is
`$LIBTMUX_TMUX`, or `tmux` on PATH.
"""

import os
import re
import subprocess
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

MODULES = (".", "examples", "workspace", "mcp", "benchmarks", "internal/tools")

# Releases below 3.7 lack the control client and the notification streams.
CONTROL_EXAMPLES = (
    "^Example(Server_OpenControl|ControlClient_.*|Session_OpenControl|Pane_OpenObservation"
    "|Server_OpenNotifications|Session_OpenNotifications|Pane_Writer|PaneObservation_.*"
    "|NotificationStream.*|Running_StreamTo)$"
)

# Examples that print a feature the release lacks, by module.
SKIPPED_BEFORE_37 = {
    ".": CONTROL_EXAMPLES,
    "workspace": "^ExampleBuildInto$",
    "mcp": "^Example_watchingAVisibleCommandAcrossTurns$",
}


def skip_pattern(module: str, version: str) -> str:
    """Name the examples `go test -skip` omits for a module on a tmux release.

    >>> skip_pattern("workspace", "3.6")
    '^ExampleBuildInto$'
    >>> skip_pattern("workspace", "3.7")
    ''
    >>> "^ExamplePane_CaptureToFile$" in skip_pattern(".", "3.3a")
    True
    >>> skip_pattern("examples", "3.3a")
    ''
    """
    skip = ""
    if version in ("3.3a", "3.4", "3.5", "3.6"):
        skip = SKIPPED_BEFORE_37.get(module, "")
    if version == "3.3a" and module == ".":
        skip += "|^ExamplePane_CaptureToFile$"
    release = re.fullmatch(r"(\d+)\.(\d+)([a-z]|-rc\d*)?", version)
    if module == "." and release:
        number = (int(release[1]), int(release[2]))
        if number < (3, 8) or (number == (3, 8) and "-rc" in version):
            skip += "|^ExampleSession_(RunProgram|StartProgram)$"
    return skip.removeprefix("|")


def tmux_version(binary: str) -> str:
    """Return the release a tmux executable reports, without the leading name."""
    reported = subprocess.run(
        [binary, "-V"], capture_output=True, text=True, check=True
    ).stdout.strip()
    return reported.removeprefix("tmux ")


def command(module: str, version: str, flags: Sequence[str]) -> tuple[list[str], str]:
    """Build the go command for a module, and the skip pattern it carries.

    >>> command("mcp", "3.8", ["-count=1"])
    (['go', 'test', '-count=1', './...'], '')
    >>> command("workspace", "3.5", ["-race"])[0]
    ['go', 'test', '-skip', '^ExampleBuildInto$', '-race', './...']
    """
    skip = skip_pattern(module, version)
    arguments = ["go", "test"]
    if skip:
        arguments += ["-skip", skip]
    return [*arguments, *flags, "./..."], skip


def run(argv: Sequence[str], environ: Mapping[str, str]) -> int:
    if not argv:
        print("usage: run_tests.py MODULE [go test flags]", file=sys.stderr)
        return 2
    module, flags = argv[0], argv[1:]
    if module not in MODULES:
        print(f"test-module: unknown module: {module}", file=sys.stderr)
        return 1
    try:
        version = tmux_version(environ.get("LIBTMUX_TMUX") or "tmux")
    except (OSError, subprocess.CalledProcessError) as error:
        print(f"test-module: cannot read the tmux version: {error}", file=sys.stderr)
        return 1
    arguments, skip = command(module, version, flags)
    if skip:
        print(f"test-module: tmux {version}; feature examples omitted: {skip}", flush=True)
    os.chdir(ROOT / module)
    os.execvp(arguments[0], arguments)


if __name__ == "__main__":
    sys.exit(run(sys.argv[1:], os.environ))
