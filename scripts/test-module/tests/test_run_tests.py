"""Tests for run_tests.py: which examples each tmux release omits."""

import json
import re
import subprocess
import sys
from pathlib import Path

import pytest

import run_tests

SCRIPT = Path(run_tests.__file__)
ROOT = SCRIPT.parents[2]


def install(directory: Path, name: str, source: str) -> None:
    path = directory / name
    path.write_text(f"#!{sys.executable}\n{source}")
    path.chmod(0o755)


@pytest.fixture
def fakes(tmp_path: Path) -> Path:
    """A PATH holding a go that records its call, and a tmux that reports 3.5."""
    bin_directory = tmp_path / "bin"
    bin_directory.mkdir()
    install(
        bin_directory,
        "go",
        "import json, os, sys\nprint(json.dumps({'argv': sys.argv[1:], 'cwd': os.getcwd()}))\n",
    )
    install(bin_directory, "tmux", "print('tmux 3.5')\n")
    return bin_directory


def invoke(fakes: Path, *argv: str, **environ: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(SCRIPT), *argv],
        env={"PATH": str(fakes), **environ},
        capture_output=True,
        text=True,
        check=False,
    )


def test_a_release_without_the_feature_omits_its_examples(fakes: Path) -> None:
    result = invoke(fakes, "workspace", "-race", "-count=1")
    assert result.returncode == 0, result.stderr
    notice, call = result.stdout.splitlines()
    assert notice == "test-module: tmux 3.5; feature examples omitted: ^ExampleBuildInto$"
    assert json.loads(call) == {
        "argv": ["test", "-skip", "^ExampleBuildInto$", "-race", "-count=1", "./..."],
        "cwd": str((ROOT / "workspace").resolve()),
    }


def test_a_release_with_every_feature_omits_nothing(fakes: Path) -> None:
    install(fakes, "tmux", "print('tmux 3.8')\n")
    result = invoke(fakes, ".", "-count=1")
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) == {
        "argv": ["test", "-count=1", "./..."],
        "cwd": str(ROOT.resolve()),
    }


def test_the_tmux_comes_from_libtmux_tmux_when_set(fakes: Path, tmp_path: Path) -> None:
    other = tmp_path / "other"
    other.mkdir()
    install(other, "tmux", "print('tmux 3.3a')\n")
    result = invoke(fakes, "mcp", LIBTMUX_TMUX=str(other / "tmux"))
    assert "tmux 3.3a; feature examples omitted" in result.stdout
    assert "^Example_watchingAVisibleCommandAcrossTurns$" in result.stdout


@pytest.mark.parametrize(
    ("module", "version", "omitted", "kept"),
    [
        (".", "3.3a", ["ExampleControlClient_Send", "ExamplePane_CaptureToFile"], []),
        (".", "3.4", ["ExampleServer_OpenControl"], ["ExamplePane_CaptureToFile"]),
        (".", "3.6", ["ExampleRunning_StreamTo"], ["ExamplePane_CaptureToFile"]),
        (".", "3.7", [], ["ExampleServer_OpenControl", "ExamplePane_CaptureToFile"]),
        (".", "3.6", [], ["ExampleServer_Start", "ExampleServer_OpenControlled"]),
        ("workspace", "3.3a", ["ExampleBuildInto"], ["ExampleBuild"]),
        ("workspace", "3.7", [], ["ExampleBuildInto"]),
        ("examples", "3.3a", [], ["ExampleBuildInto"]),
    ],
)
def test_the_omitted_examples_follow_the_release(
    module: str, version: str, omitted: list[str], kept: list[str]
) -> None:
    """Check each name against the pattern as `go test -skip` reads it."""
    pattern = run_tests.skip_pattern(module, version)
    for name in omitted:
        assert re.search(pattern, name), name
    for name in kept:
        assert pattern == "" or not re.search(pattern, name), name


def test_an_unknown_module_is_refused(fakes: Path) -> None:
    result = invoke(fakes, "typo")
    assert result.returncode == 1
    assert "test-module: unknown module: typo" in result.stderr
    assert result.stdout == ""


def test_a_module_is_required(fakes: Path) -> None:
    result = invoke(fakes)
    assert result.returncode == 2
    assert "usage: run_tests.py MODULE" in result.stderr


def test_a_tmux_that_cannot_report_stops_the_run(tmp_path: Path) -> None:
    result = invoke(tmp_path, ".")
    assert result.returncode == 1
    assert "cannot read the tmux version" in result.stderr
