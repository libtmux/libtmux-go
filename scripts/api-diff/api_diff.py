#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Report each published module's exported API difference since its last tag.

    GORELEASE=golang.org/x/exp/cmd/gorelease@VERSION api_diff.py

Runs gorelease in the root, workspace and mcp modules against the newest tag each
carries, and writes one Markdown section per module to $GITHUB_STEP_SUMMARY, or
to standard output without it. It exits 0 whatever gorelease reports: a 0.1.N
module breaks its API on purpose, and a gate that forbids it would be turned off.
"""

import os
import re
import subprocess
import sys
from collections.abc import Iterable, Mapping
from pathlib import Path
from typing import TextIO

ROOT = Path(__file__).resolve().parents[2]
# Importing a shared module must not leave a __pycache__ in the checkout.
sys.dont_write_bytecode = True
sys.path.insert(0, str(ROOT / "scripts" / "lib"))

import bounded

# The published modules; examples, benchmarks and internal/tools are not released.
MODULES = (".", "workspace", "mcp")

# Seconds one gorelease run may take, including the compile of gorelease itself.
RUN_TIMEOUT = 120

SEMVER = re.compile(r"v(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?")


def tag_prefix(module: str) -> str:
    """Return the prefix a module's tags carry: a nested module is tagged with its directory.

    >>> tag_prefix("."), tag_prefix("mcp")
    ('', 'mcp/')
    """
    return "" if module == "." else f"{module}/"


def release_order(version: str) -> tuple[int, int, int, int, tuple[tuple[int, int | str], ...]]:
    """Order a semantic version, with a pre-release below its release.

    Numeric pre-release identifiers compare as numbers, so alpha.10 follows alpha.9.

    >>> release_order("v0.1.0-alpha.9") < release_order("v0.1.0-alpha.10") < release_order("v0.1.0")
    True
    >>> release_order("v0.1.9") < release_order("v0.1.10")
    True
    """
    match = SEMVER.fullmatch(version)
    if match is None:
        raise ValueError(f"not a semantic version: {version}")
    major, minor, patch, pre = match.groups()
    identifiers = tuple(
        (0, int(part)) if part.isdigit() else (1, part) for part in (pre or "").split(".") if part
    )
    return int(major), int(minor), int(patch), 0 if pre else 1, identifiers


def latest_tag(tags: Iterable[str], module: str) -> str | None:
    """Return the newest tag of a module, or None when it has none.

    >>> tags = ["v0.1.2", "v0.1.10", "mcp/v0.1.3", "mcp/v0.2.0-alpha.1", "other"]
    >>> latest_tag(tags, "."), latest_tag(tags, "mcp"), latest_tag(tags, "workspace")
    ('v0.1.10', 'mcp/v0.2.0-alpha.1', None)
    """
    prefix = tag_prefix(module)
    versions = {
        tag: tag.removeprefix(prefix)
        for tag in tags
        if tag.startswith(prefix) and SEMVER.fullmatch(tag.removeprefix(prefix))
    }
    if not versions:
        return None
    return max(versions, key=lambda tag: release_order(versions[tag]))


def list_tags(root: Path) -> list[str]:
    """List the repository's tags."""
    result = bounded.run(
        ["git", "tag", "--list"],
        cwd=root,
        capture_output=True,
        text=True,
        check=True,
        timeout=RUN_TIMEOUT,
    )
    return result.stdout.split()


def section(module: str, tag: str | None, gorelease: str, root: Path) -> tuple[str, int]:
    """Run gorelease in a module against its tag; return the Markdown and the exit status."""
    if tag is None:
        return f"### {module}\n\nNo tag to compare against.\n\n", 0
    base = tag.removeprefix(tag_prefix(module))
    try:
        result = bounded.run(
            ["go", "run", gorelease, "-base", base],
            cwd=root / module,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            errors="replace",
            timeout=RUN_TIMEOUT,
        )
        output, status = result.stdout, result.returncode
    except subprocess.TimeoutExpired:
        output, status = f"gorelease ran past {RUN_TIMEOUT} s\n", 124
    return f"### {module} since {tag}\n\n```\n{output}```\n\n", status


def report(root: Path, gorelease: str, summary: TextIO) -> int:
    """Write a section for each published module; return the last nonzero gorelease status."""
    tags = list_tags(root)
    status = 0
    for module in MODULES:
        text, code = section(module, latest_tag(tags, module), gorelease, root)
        summary.write(text)
        summary.flush()
        status = code or status
    return status


def main(environ: Mapping[str, str]) -> int:
    gorelease = environ.get("GORELEASE")
    if not gorelease:
        print("api-diff: set GORELEASE to the gorelease package and version", file=sys.stderr)
        return 1
    if path := environ.get("GITHUB_STEP_SUMMARY"):
        with Path(path).open("a") as summary:
            status = report(ROOT, gorelease, summary)
    else:
        status = report(ROOT, gorelease, sys.stdout)
    print(f"gorelease reported status {status}; this job does not fail on it.")
    return 0


if __name__ == "__main__":
    sys.exit(main(os.environ))
