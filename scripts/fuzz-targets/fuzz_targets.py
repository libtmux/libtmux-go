#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Fuzz one target of a Go package, after checking that the target exists.

    fuzz_targets.py PACKAGE TARGET [--seconds N]

`go test -fuzz` exits zero with only a warning when its pattern matches no
target, so a renamed target would turn the fuzz job into a step that passes
without fuzzing. This lists the package's targets first and fails unless TARGET
is the first one listed, then fuzzes it for N seconds (30 by default). It runs
from the repository root.
"""

import argparse
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

# Seconds `go test -list` may take: it compiles the package's tests.
LIST_TIMEOUT = 120
# Seconds a fuzz run may take beyond its fuzzing time: it compiles first.
FUZZ_SLACK = 120

TARGET = re.compile(r"Fuzz[A-Za-z0-9_]*")


class FuzzError(Exception):
    """A target that is missing, or a run that fails."""


def listed(package: str, target: str, environ: Mapping[str, str]) -> bool:
    """Report whether `go test -list` names the target first."""
    try:
        result = bounded.run(
            ["go", "test", package, "-list", f"^{target}$"],
            cwd=ROOT,
            env=environ,
            stdout=subprocess.PIPE,
            text=True,
            timeout=LIST_TIMEOUT,
        )
    except subprocess.TimeoutExpired:
        raise FuzzError(f"go test -list ran past {LIST_TIMEOUT} s") from None
    return result.returncode == 0 and result.stdout.splitlines()[:1] == [target]


def fuzz(package: str, target: str, seconds: int, environ: Mapping[str, str]) -> None:
    """Fuzz a target for ``seconds`` seconds, echoing the engine's output."""
    command = [
        "go",
        "test",
        package,
        "-run",
        "^$",
        "-fuzz",
        f"^{target}$",
        "-fuzztime",
        f"{seconds}s",
    ]
    limit = seconds + FUZZ_SLACK
    try:
        status, _ = bounded.stream(
            command,
            timeout=limit,
            on_line=lambda line: print(line, end="", flush=True),
            cwd=ROOT,
            env=environ,
        )
    except subprocess.TimeoutExpired:
        raise FuzzError(f"fuzzing {target} ran past {limit} s") from None
    if status:
        raise FuzzError(f"fuzzing {target} exited with status {status}")


def main(argv: Sequence[str], environ: Mapping[str, str]) -> int:
    parser = argparse.ArgumentParser(description=(__doc__ or "").split("\n\n")[0])
    parser.add_argument("package", help="the Go package, such as ./tmux")
    parser.add_argument("target", help="the fuzz target, such as FuzzDecodeArgs")
    parser.add_argument("--seconds", type=int, default=30, help="how long to fuzz")
    args = parser.parse_args(argv)
    if args.package.startswith("-"):
        parser.error("the package must not start with a dash")
    if not TARGET.fullmatch(args.target):
        parser.error("the target must be a Go fuzz function name, such as FuzzDecodeArgs")
    if args.seconds < 1:
        parser.error("--seconds must be at least 1")
    try:
        if not listed(args.package, args.target, environ):
            print(f"::error::{args.package} has no fuzz target {args.target}", flush=True)
            raise FuzzError(f"{args.package} has no fuzz target {args.target}")
        fuzz(args.package, args.target, args.seconds, environ)
    except FuzzError as error:
        print(f"fuzz-targets: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:], os.environ))
