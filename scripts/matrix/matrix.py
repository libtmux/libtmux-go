#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Run each module's compatible tests against the tmux releases it supports.

The ordinary gate runs against whichever tmux is on PATH, which is one
release, and a release can lack a flag or format variable another has:

- tmux 3.4 reads the value of split-window's `-l` where `-p` was meant, so
  `-p` alone fails there while 3.3a and 3.5 take it. The fix is tmux commit
  bd29a48b5 ("Check for the right flag to fix split-window -p"), released in
  3.5. Compare `cmd-split-window.c` at the 3.4 and 3.5 tags.
- `pane_start_path` is new in 3.3 (tmux CHANGES, "CHANGES FROM 3.2a TO 3.3").

A sweep runs every module against every build, so it is slow, and it needs a
directory of tmux builds that a checkout does not come with. Point
LIBTMUX_TMUX_MATRIX at a directory holding <version>/bin/tmux, or let it look
where the matrix is usually built. Narrow what runs with LIBTMUX_MATRIX_MODULES
and LIBTMUX_MATRIX_VERSIONS. Set LIBTMUX_MATRIX_REQUIRED=1 when absence must
fail rather than skip. The versions it covers are the supported ones in
.github/ci-matrix.txt. A cell that runs past CELL_TIMEOUT seconds fails.
"""

import io
import os
import re
import shutil
import subprocess
import sys
import tempfile
from collections.abc import Mapping
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RUN_TESTS = ROOT / "scripts" / "test-module" / "run_tests.py"
MANIFEST = ROOT / ".github" / "ci-matrix.txt"
# Importing a sibling script must not leave a __pycache__ in the checkout.
sys.dont_write_bytecode = True
sys.path.insert(0, str(RUN_TESTS.parent))
sys.path.insert(0, str(ROOT / "scripts" / "lib"))

import bounded
from run_tests import MODULES

# Seconds one module may run against one tmux build; a hung cell would otherwise
# hold the whole sweep.
CELL_TIMEOUT = 600

# A line of a failing cell's output that names what failed.
FAILURE_LINE = re.compile(r"^(---|\s+[a-z_]+_test)")


class MatrixError(Exception):
    """A matrix that cannot run, as opposed to a cell that fails."""


def socket_base(tmpdir: str) -> str:
    """Choose where a sweep's socket directory goes.

    The directory sits in TMPDIR only while the default socket in it stays
    short enough for a Unix socket path.

    >>> socket_base("/var/tmp"), socket_base("relative"), socket_base("/" + "x" * 80)
    ('/var/tmp', '/tmp', '/tmp')
    """
    if not tmpdir.startswith("/") or len(tmpdir) + 32 > 100:
        return "/tmp"
    return tmpdir


def supported_versions(manifest: Path) -> list[str]:
    """List the supported releases in the CI manifest, oldest first.

    >>> import tempfile
    >>> with tempfile.NamedTemporaryFile("w", suffix=".txt") as handle:
    ...     _ = handle.write("supported 3.3a  # floor\\nrefused 3.2a\\nsupported 3.8\\n")
    ...     handle.flush()
    ...     supported_versions(Path(handle.name))
    ['3.3a', '3.8']
    """
    versions: list[str] = []
    for line in manifest.read_text().splitlines():
        fields = line.split("#", 1)[0].split()
        if len(fields) >= 2 and fields[0] == "supported":
            versions.append(fields[1])
    return versions


def available_versions(matrix: Path, supported: list[str]) -> list[str]:
    """List the supported releases the matrix holds a tmux for, oldest first.

    The manifest orders releases as tmux numbers them, which a text sort of the
    directory names does not.

    >>> import tempfile
    >>> with tempfile.TemporaryDirectory() as root:
    ...     for name in ("3.10", "3.9", "3.2a"):
    ...         tmux = Path(root) / name / "bin" / "tmux"
    ...         tmux.parent.mkdir(parents=True)
    ...         _ = tmux.write_text("#!/bin/sh\\n")
    ...         tmux.chmod(0o755)
    ...     available_versions(Path(root), ["3.9", "3.10"])
    ['3.9', '3.10']
    """
    return [
        version for version in supported if os.access(matrix / version / "bin" / "tmux", os.X_OK)
    ]


def holds_socket(directory: Path) -> bool:
    """Report whether a socket file sits anywhere under directory."""
    return any(path.is_socket() for path in directory.rglob("*"))


def check_version(matrix: Path, version: str) -> None:
    """Refuse a build that is missing or reports another release than its name."""
    binary = matrix / version / "bin" / "tmux"
    if not (binary.is_file() and os.access(binary, os.X_OK)):
        raise MatrixError(f"{binary} is not executable")
    try:
        result = bounded.run(
            [str(binary), "-V"],
            capture_output=True,
            text=True,
            errors="replace",
            timeout=bounded.PROBE,
        )
    except subprocess.TimeoutExpired:
        raise MatrixError(f"{binary} did not report its version within {bounded.PROBE} s") from None
    reported = (result.stdout + result.stderr).rstrip("\n")
    if result.returncode:
        raise MatrixError(f"{binary} could not report its version: {reported}")
    if reported != f"tmux {version}":
        raise MatrixError(f'{binary}: expected "tmux {version}", got "{reported}"')


def sweep(environ: Mapping[str, str], matrix: Path, socket_directory: Path) -> int:
    """Run every selected module against every selected release."""
    if "LIBTMUX_MATRIX_MODULES" in environ:
        modules = environ["LIBTMUX_MATRIX_MODULES"].split()
    else:
        modules = list(MODULES)
    if not modules:
        raise MatrixError("no modules selected")
    for module in modules:
        if module not in MODULES:
            raise MatrixError(f'unknown module "{module}"')
        if not (ROOT / module / "go.mod").is_file():
            raise MatrixError(f'module "{module}" has no go.mod')

    supported = supported_versions(MANIFEST)
    if not supported:
        raise MatrixError(f"{MANIFEST} lists no supported release")
    if environ.get("LIBTMUX_MATRIX_VERSIONS"):
        versions = environ["LIBTMUX_MATRIX_VERSIONS"].split()
    elif environ.get("LIBTMUX_MATRIX_REQUIRED") == "1":
        versions = supported
    else:
        versions = available_versions(matrix, supported)
    if not versions:
        raise MatrixError(f"{matrix} holds no version with bin/tmux in it")
    for version in versions:
        check_version(matrix, version)

    cell_environment = {
        name: value
        for name, value in environ.items()
        if name not in ("TMUX", "TMUX_PANE") and not name.startswith("LIBTMUX_SOCKET")
    }
    cell_environment["TMUX_TMPDIR"] = str(socket_directory)

    failed: list[str] = []
    cells = len(versions) * len(modules)
    for version in versions:
        print(f"matrix: {version} (tmux {version})")
        binary_directory = matrix / version / "bin"
        for module in modules:
            # Name the cell before it starts: it may run for CELL_TIMEOUT seconds.
            print(f"  {module:<14} ", end="", flush=True)
            try:
                result = bounded.run(
                    [sys.executable, str(RUN_TESTS), module, "-count=1"],
                    env={
                        **cell_environment,
                        "PATH": f"{binary_directory}{os.pathsep}{environ.get('PATH', '')}",
                        "LIBTMUX_TMUX": str(binary_directory / "tmux"),
                    },
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                    text=True,
                    errors="replace",
                    timeout=CELL_TIMEOUT,
                )
            except subprocess.TimeoutExpired:
                print(f"FAILED\n  timed out after {CELL_TIMEOUT} s")
                failed.append(f"{version}:{module}")
                continue
            if result.returncode == 0:
                print("ok")
            else:
                print("FAILED")
                matching = [line for line in result.stdout.splitlines() if FAILURE_LINE.match(line)]
                for line in matching[:8]:
                    print(line)
                failed.append(f"{version}:{module}")

    print()
    if not failed:
        print(f"matrix: {cells} cells across {len(versions)} tmux versions, all green")
        return 0
    print(f"matrix: {len(failed)} of {cells} cells failed: {' '.join(failed)}")
    return 1


def run(environ: Mapping[str, str]) -> int:
    required = environ.get("LIBTMUX_MATRIX_REQUIRED") or "0"
    if required not in ("0", "1"):
        print(f"matrix: LIBTMUX_MATRIX_REQUIRED must be 0 or 1, got {required}", file=sys.stderr)
        return 1
    matrix = Path(
        environ.get("LIBTMUX_TMUX_MATRIX")
        or Path(environ.get("HOME", "")) / ".local/share/libtmux-tmux-matrix"
    )
    if not matrix.is_dir():
        if required == "1":
            print(f"matrix: required matrix directory does not exist: {matrix}", file=sys.stderr)
            return 1
        print(
            f"matrix: no tmux matrix at {matrix}\n\n"
            "Set LIBTMUX_TMUX_MATRIX to a directory holding one subdirectory per version,\n"
            "each with bin/tmux inside it:\n\n"
            "    $LIBTMUX_TMUX_MATRIX/3.3a/bin/tmux\n"
            "    $LIBTMUX_TMUX_MATRIX/3.7c/bin/tmux\n\n"
            "Skipping rather than failing, so a checkout without a matrix still runs every\n"
            "other gate. Set LIBTMUX_MATRIX_REQUIRED=1 to fail instead.",
            file=sys.stderr,
        )
        return 0

    # A socket directory of this sweep's own, fresh each run: sibling checkouts
    # sweep concurrently, and a shared one lets one sweep end servers another
    # still uses. The caller's TMUX_TMPDIR is not reused for the same reason.
    try:
        socket_directory = Path(
            tempfile.mkdtemp(prefix="lmx.", dir=socket_base(environ.get("TMPDIR") or "/tmp"))
        )
    except OSError:
        print("matrix: could not make a directory for tmux sockets", file=sys.stderr)
        return 1

    status = 1
    try:
        status = sweep(environ, matrix, socket_directory)
    except MatrixError as error:
        print(f"matrix: {error}", file=sys.stderr)
        status = 1
    finally:
        # A socket left in the directory may belong to a server that still
        # answers, which deleting the directory would orphan.
        if holds_socket(socket_directory):
            print(
                "matrix: a socket remains in TMUX_TMPDIR, so the directory was kept",
                file=sys.stderr,
            )
            status = status or 1
        else:
            shutil.rmtree(socket_directory)
    return status


if __name__ == "__main__":
    # Progress and refusals interleave as they happen when output is piped.
    if isinstance(sys.stdout, io.TextIOWrapper):
        sys.stdout.reconfigure(line_buffering=True)
    sys.exit(run(os.environ))
