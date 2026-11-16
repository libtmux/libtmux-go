"""Tests for lane_tests.py, with a go that prints what a run would and a tmux that reports a release."""

import json
import os
import re
import select
import sys
from pathlib import Path

import pytest

import lane_tests

TESTS = Path(__file__).parent
ROOT = lane_tests.ROOT


def install(path: Path, source: str) -> None:
    """Install a stand-in program, run by the interpreter that runs the tests."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(f"#!{sys.executable}\n{(TESTS / source).read_text()}")
    path.chmod(0o755)


class Lane:
    """A directory of stand-in builds and the environment a workflow step would have."""

    def __init__(self, directory: Path) -> None:
        self.directory = directory
        self.log = directory / "go-calls.jsonl"
        install(directory / "gobin" / "go", "fake_go.py")
        self.environ = {
            "PATH": str(directory / "gobin"),
            "TMUX_TMPDIR": str(directory / "sockets"),
            "FAKE_GO_LOG": str(self.log),
        }

    def prefix(self, name: str, version: str) -> str:
        """Install a tmux that reports ``version`` and return its prefix."""
        binary = self.directory / name / "bin" / "tmux"
        install(binary, "fake_tmux.py")
        binary.with_name("tmux.version").write_text(version)
        return str(self.directory / name)

    def calls(self) -> list[dict[str, object]]:
        """The go calls so far, oldest first."""
        if not self.log.exists():
            return []
        return [json.loads(line) for line in self.log.read_text().splitlines()]

    def run(self, *arguments: str, **environ: str) -> int:
        """Run the script's main with the lane's environment, plus ``environ``."""
        return lane_tests.main(arguments, {**self.environ, **environ})


@pytest.fixture
def lane(tmp_path: Path) -> Lane:
    return Lane(tmp_path)


def argv_of(call: dict[str, object]) -> list[str]:
    """The arguments of a go call."""
    argv = call["argv"]
    assert isinstance(argv, list)
    return [str(argument) for argument in argv]


def env_of(call: dict[str, object]) -> dict[str, str]:
    """The LIBTMUX_* variables a go call saw."""
    environment = call["env"]
    assert isinstance(environment, dict)
    return {str(name): str(value) for name, value in environment.items()}


def run_pattern(call: dict[str, object]) -> str:
    """The value of the ``-run`` flag of a go call."""
    argv = argv_of(call)
    return argv[argv.index("-run") + 1]


def test_the_suite_runs_every_module_in_go_work_order(
    lane: Lane, capsys: pytest.CaptureFixture[str]
) -> None:
    prefix = lane.prefix("tmux", "3.8")
    assert lane.run("suite", "--prefix", prefix, "--expect", "tmux 3.8") == 0
    calls = lane.calls()
    assert [call["cwd"] for call in calls] == [
        str((ROOT / module).resolve()) for module in lane_tests.MODULES
    ]
    assert all(call["path"] == f"{prefix}/bin" for call in calls)
    assert calls[0]["argv"] == ["test", "-count=1", "./..."]
    assert Path(lane.environ["TMUX_TMPDIR"]).is_dir()
    out = capsys.readouterr().out
    assert out.startswith("tmux 3.8\n--- .: full suite ---\n")
    assert out.count("full suite ---") == len(lane_tests.MODULES)


@pytest.mark.parametrize(
    ("reports", "expect", "accepted"),
    [("next-3.9", "tmux next-*", True), ("3.8", "tmux 3.8", True), ("3.7c", "tmux 3.8", False)],
)
def test_the_expected_release_is_a_glob_and_a_mismatch_runs_nothing(
    lane: Lane, capsys: pytest.CaptureFixture[str], reports: str, expect: str, accepted: bool
) -> None:
    prefix = lane.prefix("tmux", reports)
    status = lane.run("suite", "--prefix", prefix, "--expect", expect)
    assert (status == 0) is accepted
    assert bool(lane.calls()) is accepted
    if not accepted:
        assert f"::error::expected {expect}, got tmux {reports}" in capsys.readouterr().out


@pytest.mark.parametrize("version", sorted(lane_tests.EXACT_TESTS))
def test_the_exact_release_run_selects_its_tests_and_requires_each_to_pass(
    lane: Lane, version: str
) -> None:
    prefix = lane.prefix("tmux", version)
    assert lane.run("exact", "--prefix", prefix, "--version", version) == 0
    (call,) = lane.calls()
    names = lane_tests.EXACT_TESTS[version]
    assert run_pattern(call) == "^(" + "|".join(names) + ")$"
    assert {"test", "-count=1", "-v"} <= set(argv_of(call))
    assert call["cwd"] == str(ROOT.resolve())


def test_a_required_test_that_does_not_run_fails_the_lane(
    lane: Lane, capsys: pytest.CaptureFixture[str]
) -> None:
    prefix = lane.prefix("tmux", "3.7")
    omitted = lane_tests.EXACT_TESTS["3.7"][2]
    assert lane.run("exact", "--prefix", prefix, "--version", "3.7", FAKE_GO_OMIT=omitted) == 1
    captured = capsys.readouterr()
    assert f"::error::{omitted} did not run and pass" in captured.out
    assert "1 required tests did not run and pass" in captured.err


@pytest.mark.parametrize(
    ("version", "reports", "message"),
    [("3.7", "3.6", "expected tmux 3.7, got tmux 3.6"), ("3.8", "3.8", "no exact-release tests")],
)
def test_the_exact_run_refuses_the_wrong_release_and_an_unlisted_one(
    lane: Lane, capsys: pytest.CaptureFixture[str], version: str, reports: str, message: str
) -> None:
    prefix = lane.prefix("tmux", reports)
    assert lane.run("exact", "--prefix", prefix, "--version", version) == 1
    assert message in capsys.readouterr().err
    assert lane.calls() == []


def test_the_older_daemon_tests_run_per_module_beside_a_3_3a_build(lane: Lane) -> None:
    prefix = lane.prefix("tmux", "3.8")
    older = lane.prefix("older", "3.3a")
    status = lane.run("older-daemon", "--prefix", prefix, "--older-prefix", older)
    assert status == 0
    calls = lane.calls()
    assert [call["cwd"] for call in calls] == [
        str((ROOT / module).resolve()) for module, _ in lane_tests.OLDER_DAEMON_TESTS
    ]
    assert {env_of(call)["LIBTMUX_OPTIONAL_OLD_TMUX"] for call in calls} == {f"{older}/bin/tmux"}
    for call, (_, names) in zip(calls, lane_tests.OLDER_DAEMON_TESTS, strict=True):
        assert run_pattern(call) == "^(" + "|".join(names) + ")$"


def test_the_older_daemon_run_needs_a_3_3a_build_and_every_test(
    lane: Lane, capsys: pytest.CaptureFixture[str]
) -> None:
    prefix = lane.prefix("tmux", "3.8")
    wrong = lane.prefix("older", "3.4")
    assert lane.run("older-daemon", "--prefix", prefix, "--older-prefix", wrong) == 1
    assert "expected tmux 3.3a, got tmux 3.4" in capsys.readouterr().err
    older = lane.prefix("older", "3.3a")
    omitted = lane_tests.OLDER_DAEMON_TESTS[2][1][0]
    status = lane.run(
        "older-daemon", "--prefix", prefix, "--older-prefix", older, FAKE_GO_OMIT=omitted
    )
    assert status == 1
    assert f"::error::{omitted} did not run and pass" in capsys.readouterr().out


@pytest.mark.parametrize(
    ("version", "extra"), [("3.2a", False), ("3.0a", True), ("3.1c", True), ("3.3", False)]
)
def test_a_rejected_release_runs_the_core_minimum_tests_on_the_floor_build(
    lane: Lane, version: str, extra: bool
) -> None:
    floor = lane.prefix("floor", "3.3a")
    refused = lane.prefix("refused", version)
    newer = lane.prefix("newer", "3.7c")
    names = [*lane_tests.REJECTED_TESTS, *(lane_tests.PRE32_TESTS if extra else [])]
    status = lane.run(
        *["rejected", "--version", version, "--floor-prefix", floor],
        *["--rejected-prefix", refused, "--newer-prefix", newer],
        FAKE_GO_PREFIX_TESTS=",".join([*names, lane_tests.FRESH_EXECUTABLE_TEST]),
    )
    assert status == 0
    first, *rest = lane.calls()
    assert run_pattern(first) == "^TestCoreMinimum"
    assert argv_of(first)[:2] == ["test", "-count=1"]
    assert first["path"] == f"{floor}/bin"
    environment = env_of(first)
    assert environment["LIBTMUX_REJECTED_TMUX"] == f"{refused}/bin/tmux"
    assert environment["LIBTMUX_FLOOR_TMUX"] == f"{floor}/bin/tmux"
    assert environment["LIBTMUX_REQUIRE_REJECTED"] == "1"
    assert ("LIBTMUX_REQUIRE_PRE32" in environment) is extra
    if version == "3.3":
        (fresh,) = rest
        assert run_pattern(fresh) == f"^{lane_tests.FRESH_EXECUTABLE_TEST}$"
        assert fresh["path"] == f"{newer}/bin"
    else:
        assert rest == []


def test_a_rejected_release_fails_without_its_extra_tests_or_the_newer_client(
    lane: Lane, capsys: pytest.CaptureFixture[str]
) -> None:
    floor = lane.prefix("floor", "3.3a")
    refused = lane.prefix("refused", "3.0a")
    arguments = ["rejected", "--version", "3.0a", "--floor-prefix", floor]
    arguments += ["--rejected-prefix", refused]
    only_base = ",".join(lane_tests.REJECTED_TESTS)
    assert lane.run(*arguments, FAKE_GO_PREFIX_TESTS=only_base) == 1
    assert f"::error::{lane_tests.PRE32_TESTS[0]} did not run and pass" in capsys.readouterr().out
    refused = lane.prefix("refused", "3.3")
    arguments = ["rejected", "--version", "3.3", "--floor-prefix", floor]
    arguments += ["--rejected-prefix", refused]
    assert lane.run(*arguments, FAKE_GO_PREFIX_TESTS=only_base) == 1
    assert "needs --newer-prefix" in capsys.readouterr().err


def test_a_failing_run_and_a_hung_run_fail_the_lane(
    lane: Lane, capsys: pytest.CaptureFixture[str], monkeypatch: pytest.MonkeyPatch
) -> None:
    prefix = lane.prefix("tmux", "3.7")
    arguments = ["exact", "--prefix", prefix, "--version", "3.7"]
    assert lane.run(*arguments, FAKE_GO_STATUS="3") == 1
    assert "run_tests.py . exited with status 3" in capsys.readouterr().err
    monkeypatch.setattr(lane_tests, "RUN_TIMEOUT", 0.3)
    assert lane.run(*arguments, FAKE_GO_HANG="1") == 1
    assert "ran past 0.3 s" in capsys.readouterr().err


def test_a_timed_out_run_stops_the_children_of_go(
    lane: Lane, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The test binary below `go test` holds the FIFO too; it must not outlive the limit."""
    fifo = lane.directory / "sleeper.fifo"
    os.mkfifo(fifo)
    reader = os.open(fifo, os.O_RDONLY | os.O_NONBLOCK)
    try:
        prefix = lane.prefix("tmux", "3.7")
        monkeypatch.setattr(lane_tests, "RUN_TIMEOUT", 1.0)
        arguments = ["exact", "--prefix", prefix, "--version", "3.7"]
        assert lane.run(*arguments, FAKE_GO_HANG="1", FAKE_GO_SLEEPER=str(fifo)) == 1
        assert os.read(reader, 1) == b"x", "the stand-in go never started its child"
        ready, _, _ = select.select([reader], [], [], 5)
        assert ready, "the child of go is still running"
        assert os.read(reader, 1) == b""
    finally:
        os.close(reader)


def test_a_missing_socket_directory_variable_is_reported(lane: Lane) -> None:
    prefix = lane.prefix("tmux", "3.7")
    del lane.environ["TMUX_TMPDIR"]
    assert lane.run("exact", "--prefix", prefix, "--version", "3.7") == 1


def test_every_required_test_is_a_go_test_function() -> None:
    """A renamed test would otherwise fail a lane that no longer knows its name."""
    declared = set()
    for path in ROOT.rglob("*_test.go"):
        declared.update(
            re.findall(r"^func (Test\w+)\(", path.read_text(errors="replace"), re.MULTILINE)
        )
    required = {
        *(name for names in lane_tests.EXACT_TESTS.values() for name in names),
        *(name for _, names in lane_tests.OLDER_DAEMON_TESTS for name in names),
        *lane_tests.REJECTED_TESTS,
        *lane_tests.PRE32_TESTS,
        lane_tests.FRESH_EXECUTABLE_TEST,
    }
    assert sorted(required - declared) == []
    assert {module for module, _ in lane_tests.OLDER_DAEMON_TESTS} <= set(lane_tests.MODULES)
