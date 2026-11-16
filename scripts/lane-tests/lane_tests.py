#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Run the test steps of a tmux lane of the tests workflow.

    lane_tests.py suite --prefix PREFIX --expect GLOB
    lane_tests.py exact --prefix PREFIX --version VERSION
    lane_tests.py older-daemon --prefix PREFIX --older-prefix PREFIX
    lane_tests.py rejected --version VERSION --floor-prefix PREFIX \\
        --rejected-prefix PREFIX [--newer-prefix PREFIX]

A PREFIX is an installation directory with bin/tmux, such as the `prefix` output
of the build-tmux action. TMUX_TMPDIR names the socket directory; it is created.

`suite` runs every module's full suite. The other three run named tests and fail
unless each one prints `--- PASS:`, because a test that skips on any other release
passes silently when a lane builds the wrong tmux. Each module run is stopped
after RUN_TIMEOUT seconds.
"""

import argparse
import fnmatch
import os
import subprocess
import sys
import threading
from collections.abc import Mapping, Sequence
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RUN_TESTS = ROOT / "scripts" / "test-module" / "run_tests.py"
# Importing a sibling script must not leave a __pycache__ in the checkout.
sys.dont_write_bytecode = True
sys.path.insert(0, str(RUN_TESTS.parent))

from run_tests import MODULES

# Seconds one test run may take; a hung run would otherwise hold the job.
RUN_TIMEOUT = 300

CLIPBOARD = "TestRawRefreshClientClipboardQueryIsRefusedWhereItKillsTheServer"

# The tests that skip on every release but the one they exercise.
EXACT_TESTS: dict[str, tuple[str, ...]] = {
    "3.3a": ("TestRawSelectLayoutIsRefusedWhereItKillsTheServer", CLIPBOARD),
    "3.4": (CLIPBOARD,),
    "3.5": (CLIPBOARD,),
    "3.6": (CLIPBOARD,),
    "3.7": (
        "TestExact37RefusesWindowNamesWithColonOrPeriod",
        "TestExact37ConnectionCallRefusesNamesWithColonOrPeriod",
        "TestOptionalExact37BreakPaneKeepsItsNamingContract",
        "TestOptionalExact37ConnectionCallRefusesRawBreakPane",
        "TestOpenControlBindsTheVersionItChecked",
    ),
}

# The tests that start a 3.3a daemon beside a newer client, by module.
OLDER_DAEMON_TESTS: tuple[tuple[str, tuple[str, ...]], ...] = (
    (
        ".",
        (
            "TestOptionalControlRejectsOlderSupportedDaemon",
            "TestOptionalColdControlRejectsOlderExecutable",
            "TestOptionalColdBootstrapKeepsItsFeatureFloor",
            "TestPinnedControlSocketPreventsReplacement",
            "TestOptionalRefusalsLeaveOlderCoreStateIntact",
            "TestStartControlModeRefusesOlderSupportedDaemon",
        ),
    ),
    ("workspace", ("TestWorkspaceChoosesAnEligibleTransport",)),
    (
        "mcp",
        (
            "TestOlderCoreRuntimeKeepsItsProcessLane",
            "TestOlderCoreRuntimeBootstrapsWithoutControl",
            "TestOlderPasteStagingRefusesBeforeTheFirstChunk",
            "TestOlderWaitForTextPollsInsteadOfRefusing",
        ),
    ),
)

# The tests every rejected release must run, and the ones releases that predate
# the version probe add.
REJECTED_TESTS = (
    "TestCoreMinimumRejectsExplicitOlderDaemon",
    "TestCoreMinimumOpaqueExecutableOn33a",
)
PRE32_RELEASES = ("3.0a", "3.1c")
PRE32_TESTS = (
    "TestCoreMinimumRefusesDaemonBeforeProbeExisted",
    "TestCoreMinimumRefusesExecutableBeforeProbeExisted",
    "TestCoreMinimumRefusesPre32DaemonPromptly",
)
# Needs both the floor build and a newer client, so only the unpatched 3.3 lane runs it.
FRESH_EXECUTABLE_TEST = "TestCoreMinimumColdFeatureRequirementUsesFreshExecutable"
FRESH_EXECUTABLE_RELEASE = "3.3"
INTEGRATION = "./tmux/internal/integration"


class LaneError(Exception):
    """A lane that cannot run, or a test that did not run and pass."""


def with_prefix(environ: Mapping[str, str], prefix: str) -> dict[str, str]:
    """Copy the environment with a tmux installation first on PATH.

    >>> with_prefix({"PATH": "/usr/bin"}, "/opt/tmux")["PATH"]
    '/opt/tmux/bin:/usr/bin'
    """
    return {**environ, "PATH": f"{prefix}/bin{os.pathsep}{environ.get('PATH', '')}"}


def reported(binary: str, environ: Mapping[str, str]) -> str:
    """Return what a tmux executable reports for -V, found on the environment's PATH."""
    try:
        result = subprocess.run(
            [binary, "-V"],
            env=environ,
            capture_output=True,
            text=True,
            check=True,
            timeout=RUN_TIMEOUT,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise LaneError(f"cannot read the version of {binary}: {error}") from error
    return result.stdout.strip()


def expect_release(binary: str, environ: Mapping[str, str], expected: str) -> None:
    """Fail unless a tmux executable reports exactly the expected release."""
    actual = reported(binary, environ)
    if actual != expected:
        raise LaneError(f"expected {expected}, got {actual}")


def make_socket_directory(environ: Mapping[str, str]) -> None:
    """Create the directory tmux puts its sockets in."""
    if not environ.get("TMUX_TMPDIR"):
        raise LaneError("TMUX_TMPDIR is not set")
    Path(environ["TMUX_TMPDIR"]).mkdir(parents=True, exist_ok=True)


def run(command: Sequence[str], environ: Mapping[str, str], label: str) -> list[str]:
    """Run a command in the repository root, echo its output as it arrives and return it.

    The command is killed after RUN_TIMEOUT seconds. Standard error goes straight through.
    ``label`` names the command in a failure.
    """
    process = subprocess.Popen(
        command,
        env=environ,
        cwd=ROOT,
        stdout=subprocess.PIPE,
        text=True,
        errors="replace",
    )
    expired = threading.Event()

    def expire() -> None:
        expired.set()
        process.kill()

    timer = threading.Timer(RUN_TIMEOUT, expire)
    timer.start()
    lines: list[str] = []
    try:
        assert process.stdout is not None
        for line in process.stdout:
            print(line, end="", flush=True)
            lines.append(line)
    finally:
        timer.cancel()
        status = process.wait()
    if expired.is_set():
        raise LaneError(f"{label} ran past {RUN_TIMEOUT} s")
    if status:
        raise LaneError(f"{label} exited with status {status}")
    return lines


def pattern(names: Sequence[str]) -> str:
    """Build the `-run` regular expression that selects exactly these tests.

    >>> pattern(["TestA", "TestB"])
    '^(TestA|TestB)$'
    """
    return "^(" + "|".join(names) + ")$"


def passed(lines: Sequence[str], names: Sequence[str]) -> None:
    """Fail unless each test has a top-level `--- PASS:` line in the output."""
    missing = [name for name in names if not any(f"--- PASS: {name} " in line for line in lines)]
    if missing:
        for name in missing:
            print(f"::error::{name} did not run and pass", flush=True)
        raise LaneError(f"{len(missing)} required tests did not run and pass: {' '.join(missing)}")


def run_tests(module: str, flags: Sequence[str], environ: Mapping[str, str]) -> list[str]:
    """Run a module's tests through run_tests.py, which omits what the release cannot run."""
    return run([sys.executable, str(RUN_TESTS), module, *flags], environ, f"run_tests.py {module}")


def suite(environ: Mapping[str, str], prefix: str, expect: str) -> None:
    """Run every module's full suite against the tmux in a prefix."""
    lane = with_prefix(environ, prefix)
    actual = reported("tmux", lane)
    # The expected release is a glob: tmux's master branch reports next-X.Y.
    if not fnmatch.fnmatchcase(actual, expect):
        print(f"::error::expected {expect}, got {actual}", flush=True)
        raise LaneError(f"expected {expect}, got {actual}")
    print(actual)
    make_socket_directory(lane)
    for module in MODULES:
        print(f"--- {module}: full suite ---", flush=True)
        run_tests(module, ["-count=1"], lane)


def exact(environ: Mapping[str, str], prefix: str, version: str) -> None:
    """Run the tests that need exactly this release, and require each to pass."""
    if version not in EXACT_TESTS:
        raise LaneError(f"no exact-release tests are listed for tmux {version}")
    lane = with_prefix(environ, prefix)
    expect_release("tmux", lane, f"tmux {version}")
    make_socket_directory(lane)
    names = EXACT_TESTS[version]
    lines = run_tests(".", ["-count=1", "-v", "-run", pattern(names)], lane)
    passed(lines, names)


def older_daemon(environ: Mapping[str, str], prefix: str, older_prefix: str) -> None:
    """Run the tests that put a 3.3a daemon beside this lane's client."""
    lane = with_prefix(environ, prefix)
    lane["LIBTMUX_OPTIONAL_OLD_TMUX"] = f"{older_prefix}/bin/tmux"
    expect_release(lane["LIBTMUX_OPTIONAL_OLD_TMUX"], lane, "tmux 3.3a")
    make_socket_directory(lane)
    for module, names in OLDER_DAEMON_TESTS:
        lines = run_tests(module, ["-count=1", "-v", "-run", pattern(names)], lane)
        passed(lines, names)


def rejected(
    environ: Mapping[str, str],
    version: str,
    floor_prefix: str,
    rejected_prefix: str,
    newer_prefix: str | None,
) -> None:
    """Check that a release below the floor is refused, with the tests required to run."""
    lane = with_prefix(environ, floor_prefix)
    lane["LIBTMUX_REJECTED_TMUX"] = f"{rejected_prefix}/bin/tmux"
    lane["LIBTMUX_FLOOR_TMUX"] = f"{floor_prefix}/bin/tmux"
    expect_release("tmux", lane, "tmux 3.3a")
    expect_release(lane["LIBTMUX_REJECTED_TMUX"], lane, f"tmux {version}")
    make_socket_directory(lane)
    # A skipped test or a filter that matches nothing must fail the job.
    lane["LIBTMUX_REQUIRE_REJECTED"] = "1"
    names = REJECTED_TESTS
    if version in PRE32_RELEASES:
        lane["LIBTMUX_REQUIRE_PRE32"] = "1"
        names = (*names, *PRE32_TESTS)
    go_test = ["go", "test", "-count=1", "-timeout", "2m", "-v", INTEGRATION, "-run"]
    passed(run([*go_test, "^TestCoreMinimum"], lane, "go test TestCoreMinimum"), names)
    if version == FRESH_EXECUTABLE_RELEASE:
        if not newer_prefix:
            raise LaneError(f"tmux {version} needs --newer-prefix for the stale-executable test")
        # A configured newer executable replaced by the floor build must be
        # checked afresh, which needs both on hand.
        newer = with_prefix(lane, newer_prefix)
        fresh = run(
            [*go_test, f"^{FRESH_EXECUTABLE_TEST}$"], newer, f"go test {FRESH_EXECUTABLE_TEST}"
        )
        passed(fresh, [FRESH_EXECUTABLE_TEST])


def main(argv: Sequence[str], environ: Mapping[str, str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    commands = parser.add_subparsers(dest="command", required=True)
    for name in ("suite", "exact", "older-daemon"):
        command = commands.add_parser(name)
        command.add_argument("--prefix", required=True)
        if name == "suite":
            command.add_argument("--expect", required=True)
        elif name == "exact":
            command.add_argument("--version", required=True)
        else:
            command.add_argument("--older-prefix", required=True)
    command = commands.add_parser("rejected")
    command.add_argument("--version", required=True)
    command.add_argument("--floor-prefix", required=True)
    command.add_argument("--rejected-prefix", required=True)
    command.add_argument("--newer-prefix")
    args = parser.parse_args(argv)
    try:
        match args.command:
            case "suite":
                suite(environ, args.prefix, args.expect)
            case "exact":
                exact(environ, args.prefix, args.version)
            case "older-daemon":
                older_daemon(environ, args.prefix, args.older_prefix)
            case _:
                rejected(
                    environ,
                    args.version,
                    args.floor_prefix,
                    args.rejected_prefix,
                    args.newer_prefix,
                )
    except LaneError as error:
        print(f"lane-tests: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:], os.environ))
