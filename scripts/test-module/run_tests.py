#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Run one module's tests, qualifying fixed-output examples by tmux release.

    run_tests.py MODULE [go test flags]

MODULE is a directory named in go.work's `use` block, relative to the repository
root. An `Example` function has no `t.Skip`, so a release that lacks the feature an
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
# Importing a shared module must not leave a __pycache__ in the checkout.
sys.dont_write_bytecode = True
sys.path.insert(0, str(ROOT / "scripts" / "lib"))

import bounded


def work_modules(text: str) -> tuple[str, ...]:
    r"""List the module directories a go.work file names, relative to its directory.

    >>> work_modules("go 1.26.0\n\nuse (\n\t.\n\t./mcp // consumer\n\t./internal/tools\n)\n")
    ('.', 'mcp', 'internal/tools')
    >>> work_modules("use ./workspace\n")
    ('workspace',)
    """
    names: list[str] = []
    inside = False
    for raw in text.splitlines():
        line = raw.split("//", 1)[0].strip()
        if inside:
            if line == ")":
                inside = False
            elif line:
                names.append(line)
        elif line == "use (":
            inside = True
        elif line.startswith("use "):
            names.append(line.removeprefix("use ").strip())
    return tuple(name.removeprefix("./") for name in names)


MODULES = work_modules((ROOT / "go.work").read_text())

# Releases below this lack the control client and the notification streams.
CONTROL_FLOOR = (3, 7, "")

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


RELEASE = re.compile(r"(\d+)\.(\d+)([a-z]?)")


def before(version: str, floor: tuple[int, int, str]) -> bool:
    """Report whether a release is older than floor, comparing numbers rather than text.

    A name that is not a release, such as a development build's, is not older.

    >>> before("3.5a", CONTROL_FLOOR), before("3.6", CONTROL_FLOOR), before("3.3", CONTROL_FLOOR)
    (True, True, True)
    >>> before("3.7", CONTROL_FLOOR), before("3.7a", CONTROL_FLOOR), before("3.10", CONTROL_FLOOR)
    (False, False, False)
    >>> before("next-3.9", CONTROL_FLOOR), before("master", CONTROL_FLOOR)
    (False, False)
    """
    match = RELEASE.fullmatch(version)
    if match is None:
        return False
    return (int(match[1]), int(match[2]), match[3]) < floor


def skip_pattern(module: str, version: str) -> str:
    """Name the examples `go test -skip` omits for a module on a tmux release.

    >>> skip_pattern("workspace", "3.6")
    '^ExampleBuildInto$'
    >>> skip_pattern("workspace", "3.7")
    ''
    >>> skip_pattern(".", "3.3a").endswith("|^ExamplePane_CaptureToFile$")
    True
    >>> skip_pattern("examples", "3.3a")
    ''
    """
    skip = ""
    if before(version, CONTROL_FLOOR):
        skip = SKIPPED_BEFORE_37.get(module, "")
    if version == "3.3a" and module == ".":
        skip += "|^ExamplePane_CaptureToFile$"
    return skip


def tmux_version(binary: str) -> str:
    """Return the release a tmux executable reports, without the leading name."""
    reported = bounded.run(
        [binary, "-V"], capture_output=True, text=True, check=True, timeout=bounded.PROBE
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
    except (OSError, subprocess.SubprocessError) as error:
        print(f"test-module: cannot read the tmux version: {error}", file=sys.stderr)
        return 1
    arguments, skip = command(module, version, flags)
    if skip:
        print(f"test-module: tmux {version}; feature examples omitted: {skip}", flush=True)
    os.chdir(ROOT / module)
    try:
        os.execvp(arguments[0], arguments)
    except OSError as error:
        print(f"test-module: cannot run {arguments[0]}: {error.strerror}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(run(sys.argv[1:], os.environ))
