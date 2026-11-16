"""Tests for fuzz_targets.py, with a go that lists the targets it is told of."""

import json
import sys
from pathlib import Path

import pytest

import fuzz_targets

FAKE = Path(__file__).with_name("fake_go.py")


class Go:
    """A directory holding a stand-in go, and the environment that finds it."""

    def __init__(self, directory: Path, targets: str) -> None:
        binary = directory / "bin" / "go"
        binary.parent.mkdir()
        binary.write_text(f"#!{sys.executable}\n{FAKE.read_text()}")
        binary.chmod(0o755)
        self.log = directory / "calls.jsonl"
        self.environ = {
            "PATH": str(binary.parent),
            "FAKE_GO_LOG": str(self.log),
            "FAKE_GO_TARGETS": targets,
        }

    def calls(self) -> list[list[str]]:
        """The go calls so far, oldest first."""
        return (
            [json.loads(line) for line in self.log.read_text().splitlines()]
            if self.log.exists()
            else []
        )


def test_a_listed_target_is_fuzzed_for_the_requested_time(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    go = Go(tmp_path, "FuzzA,FuzzB")
    assert fuzz_targets.main(["./tmux", "FuzzA", "--seconds", "7"], go.environ) == 0
    assert go.calls() == [
        ["test", "./tmux", "-list", "^FuzzA$"],
        ["test", "./tmux", "-run", "^$", "-fuzz", "^FuzzA$", "-fuzztime", "7s"],
    ]
    assert "fuzz: elapsed" in capsys.readouterr().out


def test_a_renamed_target_fails_before_any_fuzzing(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    """``go test -fuzz`` would pass with a warning; the list check is what fails the job."""
    go = Go(tmp_path, "FuzzRenamed")
    assert fuzz_targets.main(["./tmux", "FuzzA"], go.environ) == 1
    assert go.calls() == [["test", "./tmux", "-list", "^FuzzA$"]]
    captured = capsys.readouterr()
    assert "::error::./tmux has no fuzz target FuzzA" in captured.out
    assert "fuzz-targets: ./tmux has no fuzz target FuzzA" in captured.err


def test_a_failing_fuzz_run_fails_the_step(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    go = Go(tmp_path, "FuzzA")
    environ = {**go.environ, "FAKE_GO_STATUS": "1"}
    assert fuzz_targets.main(["./tmux", "FuzzA"], environ) == 1
    assert "exited with status 1" in capsys.readouterr().err


@pytest.mark.parametrize(
    "arguments",
    [
        ["--bad", "FuzzA"],
        ["./tmux", "Test"],
        ["./tmux", "FuzzA", "--seconds", "0"],
        ["-x", "FuzzA"],
    ],
)
def test_arguments_that_could_change_the_go_command_are_refused(
    tmp_path: Path, arguments: list[str]
) -> None:
    go = Go(tmp_path, "FuzzA")
    with pytest.raises(SystemExit) as refused:
        fuzz_targets.main(arguments, go.environ)
    assert refused.value.code == 2
    assert go.calls() == []


def test_the_fuzz_limit_is_the_fuzz_time_plus_the_compile_slack(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A hung engine fails the step instead of holding the job to its time limit."""
    go = Go(tmp_path, "FuzzA")
    limits: list[float] = []

    def stream(command: list[str], *, timeout: float, **options: object) -> tuple[int, list[str]]:
        limits.append(timeout)
        return 0, []

    monkeypatch.setattr(fuzz_targets.bounded, "stream", stream)
    assert fuzz_targets.main(["./tmux", "FuzzA", "--seconds", "30"], go.environ) == 0
    assert limits == [30 + fuzz_targets.FUZZ_SLACK]
