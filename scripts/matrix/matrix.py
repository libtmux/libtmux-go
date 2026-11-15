#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Run each module's compatible tests against the tmux releases it supports.

The ordinary gate runs against whichever tmux is on PATH, which is one
version, and version-specific breakage is not hypothetical: tmux 3.4 stopped
accepting split-window's -p flag that 3.3a and 3.5 both take, and
pane_start_path does not exist before 3.3. A single-version gate cannot see
either.

It is separate from the ordinary gate because it is slow -- every module
against every tmux build -- and because it needs a matrix of tmux builds a
checkout does not come with. Point LIBTMUX_TMUX_MATRIX at a directory holding
<version>/bin/tmux, or let it look where the matrix is usually built. Narrow
what runs with LIBTMUX_MATRIX_MODULES and LIBTMUX_MATRIX_VERSIONS. Set
LIBTMUX_MATRIX_REQUIRED=1 when absence must fail rather than skip. The
versions it covers are the supported ones in .github/ci-matrix.txt.
"""

import io
import os
import re
import shutil
import stat
import subprocess
import sys
import tempfile
from collections.abc import Mapping
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RUN_TESTS = ROOT / "scripts" / "test-module" / "run_tests.py"
MANIFEST = ROOT / ".github" / "ci-matrix.txt"

MODULES = (".", "examples", "workspace", "mcp", "benchmarks", "internal/tools")

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
    for parent, _, names in os.walk(directory):
        for name in names:
            if stat.S_ISSOCK(os.lstat(Path(parent) / name).st_mode):
                return True
    return False


def check_version(matrix: Path, version: str) -> None:
    """Refuse a build that is missing or reports another release than its name."""
    binary = matrix / version / "bin" / "tmux"
    if not (binary.is_file() and os.access(binary, os.X_OK)):
        raise MatrixError(f"{binary} is not executable")
    result = subprocess.run(
        [str(binary), "-V"], capture_output=True, text=True, check=False, errors="replace"
    )
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
    selected_cells = len(versions) * len(modules)
    run_cells = 0
    for version in versions:
        print(f"matrix: {version} (tmux {version})")
        binary_directory = matrix / version / "bin"
        for module in modules:
            run_cells += 1
            result = subprocess.run(
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
                check=False,
            )
            if result.returncode == 0:
                print(f"  {module:<14} ok")
            else:
                print(f"  {module:<14} FAILED")
                matching = [line for line in result.stdout.splitlines() if FAILURE_LINE.match(line)]
                for line in matching[:8]:
                    print(line)
                failed.append(f"{version}:{module}")

    print()
    if run_cells != selected_cells:
        raise MatrixError(f"ran {run_cells} of {selected_cells} selected cells")
    if not failed:
        print(f"matrix: {run_cells} cells across {len(versions)} tmux versions, all green")
        return 0
    print(f"matrix: {len(failed)} of {selected_cells} cells failed: {' '.join(failed)}")
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
            "Skipping rather than failing: a checkout without a matrix can still run every\n"
            "other gate, and reporting a pass this did not earn would be worse than saying\n"
            "it did not run.",
            file=sys.stderr,
        )
        return 0

    # A tmux namespace of this sweep's own, made fresh each run. Sibling
    # checkouts sweep their own matrices on the same machine, and a shared
    # socket directory is one sweep ending servers another is still using. The
    # caller's TMUX_TMPDIR is not reused for the same reason: it is the
    # directory of the tmux they use.
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
