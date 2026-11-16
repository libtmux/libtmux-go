"""Tests for matrix.py, with a go that records its call standing in for the real one."""

import subprocess
import sys
import tempfile
from pathlib import Path

import pytest

import matrix

SCRIPT = Path(matrix.__file__)
SUPPORTED = matrix.supported_versions(matrix.MANIFEST)

TESTS = Path(__file__).parent


def install(path: Path, source: str) -> None:
    """Install a stand-in program, run by the interpreter that runs the tests."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(f"#!{sys.executable}\n{(TESTS / source).read_text()}")
    path.chmod(0o755)


def install_tmux(matrix_directory: Path, version: str, reported: str | None = None) -> None:
    """Install a tmux for ``version`` that reports ``reported``, which defaults to the same."""
    binary = matrix_directory / version / "bin" / "tmux"
    install(binary, "fake_tmux.py")
    binary.with_name("tmux.version").write_text(
        (reported or f"tmux {version}").removeprefix("tmux ")
    )


@pytest.fixture
def bin_directory(tmp_path: Path) -> Path:
    directory = tmp_path / "bin"
    install(directory / "go", "fake_go.py")
    return directory


class Sweep:
    """Run matrix.py against a directory of fake tmux builds."""

    def __init__(self, bin_directory: Path, matrix_directory: Path, sockets: Path) -> None:
        self.bin_directory = bin_directory
        self.matrix_directory = matrix_directory
        self.sockets = sockets

    def __call__(self, **variables: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(SCRIPT)],
            env={
                "PATH": str(self.bin_directory),
                "LIBTMUX_TMUX_MATRIX": str(self.matrix_directory),
                "TMUX_TMPDIR": str(self.sockets),
                "TMPDIR": tempfile.gettempdir(),
                **variables,
            },
            capture_output=True,
            text=True,
            check=False,
        )


@pytest.fixture
def sweep(tmp_path: Path, bin_directory: Path) -> Sweep:
    matrix_directory = tmp_path / "matrix"
    matrix_directory.mkdir()
    sockets = tmp_path / "callers"
    sockets.mkdir()
    return Sweep(bin_directory, matrix_directory, sockets)


def output_of(result: subprocess.CompletedProcess[str]) -> str:
    return result.stdout + result.stderr


def test_a_missing_matrix_skips_with_an_explanation(sweep: Sweep, tmp_path: Path) -> None:
    result = sweep(LIBTMUX_TMUX_MATRIX=str(tmp_path / "missing"))
    assert result.returncode == 0
    assert "no tmux matrix at" in result.stderr
    assert "Skipping rather than failing" in result.stderr
    assert result.stdout == ""


def test_a_requested_version_without_a_binary_is_refused(sweep: Sweep) -> None:
    result = sweep(LIBTMUX_MATRIX_VERSIONS="3.7c", LIBTMUX_MATRIX_MODULES=".")
    assert result.returncode == 1
    assert "3.7c/bin/tmux is not executable" in output_of(result)


def test_a_binary_reporting_another_version_is_refused(sweep: Sweep) -> None:
    install_tmux(sweep.matrix_directory, "3.7c", "tmux 3.7b")
    result = sweep(LIBTMUX_MATRIX_VERSIONS="3.7c", LIBTMUX_MATRIX_MODULES=".")
    assert result.returncode == 1
    assert 'expected "tmux 3.7c", got "tmux 3.7b"' in output_of(result)


def test_an_unknown_module_is_refused(sweep: Sweep) -> None:
    install_tmux(sweep.matrix_directory, "3.7c")
    result = sweep(LIBTMUX_MATRIX_VERSIONS="3.7c", LIBTMUX_MATRIX_MODULES="typo")
    assert result.returncode == 1
    assert 'unknown module "typo"' in output_of(result)


def test_no_selected_modules_is_refused(sweep: Sweep) -> None:
    install_tmux(sweep.matrix_directory, "3.7c")
    result = sweep(LIBTMUX_MATRIX_VERSIONS="3.7c", LIBTMUX_MATRIX_MODULES="   ")
    assert result.returncode == 1
    assert "no modules selected" in output_of(result)


def test_a_required_matrix_must_exist(sweep: Sweep, tmp_path: Path) -> None:
    result = sweep(
        LIBTMUX_TMUX_MATRIX=str(tmp_path / "missing"),
        LIBTMUX_MATRIX_REQUIRED="1",
        LIBTMUX_MATRIX_VERSIONS="3.7c",
        LIBTMUX_MATRIX_MODULES=".",
    )
    assert result.returncode == 1
    assert "required matrix directory does not exist" in output_of(result)


def test_the_required_flag_takes_only_zero_or_one(sweep: Sweep) -> None:
    result = sweep(LIBTMUX_MATRIX_REQUIRED="yes")
    assert result.returncode == 1
    assert "LIBTMUX_MATRIX_REQUIRED must be 0 or 1, got yes" in result.stderr


@pytest.mark.parametrize("missing", SUPPORTED)
def test_a_required_matrix_refuses_each_missing_supported_version(
    sweep: Sweep, missing: str
) -> None:
    for version in SUPPORTED:
        if version != missing:
            install_tmux(sweep.matrix_directory, version)
    result = sweep(LIBTMUX_MATRIX_REQUIRED="1", LIBTMUX_MATRIX_MODULES=".")
    assert result.returncode == 1
    assert f"{missing}/bin/tmux is not executable" in output_of(result)


def test_a_required_matrix_counts_every_selected_cell(sweep: Sweep, tmp_path: Path) -> None:
    install_tmux(sweep.matrix_directory, "3.7b")
    install_tmux(sweep.matrix_directory, "3.7c")
    log = tmp_path / "go-calls"
    result = sweep(
        LIBTMUX_MATRIX_REQUIRED="1",
        LIBTMUX_MATRIX_VERSIONS="3.7b 3.7c",
        LIBTMUX_MATRIX_MODULES=". workspace",
        MATRIX_TEST_LOG=str(log),
    )
    assert result.returncode == 0, output_of(result)
    assert "matrix: 4 cells across 2 tmux versions, all green" in result.stdout
    assert len(log.read_text().split()) == 4


def test_without_a_selection_it_runs_the_supported_builds_it_finds(
    sweep: Sweep, tmp_path: Path
) -> None:
    install_tmux(sweep.matrix_directory, "3.3a")
    install_tmux(sweep.matrix_directory, "3.8")
    install_tmux(sweep.matrix_directory, "3.2a")
    (sweep.matrix_directory / "3.7c").mkdir()
    result = sweep(LIBTMUX_MATRIX_MODULES="benchmarks")
    assert result.returncode == 0, output_of(result)
    assert "matrix: 3.3a (tmux 3.3a)\n  benchmarks     ok\n" in result.stdout
    assert "3.2a" not in result.stdout
    assert "matrix: 2 cells across 2 tmux versions, all green" in result.stdout


def test_a_failing_cell_is_named_and_fails_the_sweep(sweep: Sweep) -> None:
    install_tmux(sweep.matrix_directory, "3.7c")
    result = sweep(
        LIBTMUX_MATRIX_VERSIONS="3.7c",
        LIBTMUX_MATRIX_MODULES=". workspace",
        MATRIX_TEST_FAIL="workspace",
    )
    assert result.returncode == 1
    assert "  .              ok\n  workspace      FAILED\n" in result.stdout
    assert "--- FAIL: TestRecorded" in result.stdout
    assert "recorded_test.go:1: failed" in result.stdout
    assert "matrix: 1 of 2 cells failed: 3.7c:workspace" in result.stdout


def test_a_sweep_gets_a_socket_directory_of_its_own_without_selectors(
    sweep: Sweep, tmp_path: Path
) -> None:
    """A sweep must not reach the tmux a person uses, or reuse a directory."""
    install_tmux(sweep.matrix_directory, "3.7c")
    directories: list[str] = []
    for round_ in range(2):
        env_log = tmp_path / f"go-env-{round_}"
        result = sweep(
            LIBTMUX_MATRIX_VERSIONS="3.7c",
            LIBTMUX_MATRIX_MODULES=". workspace",
            MATRIX_TEST_ENV_LOG=str(env_log),
            TMUX="/tmp/decoy,1,0",
            TMUX_PANE="%9",
            LIBTMUX_SOCKET="decoy",
            LIBTMUX_SOCKET_PATH="/tmp/decoy/path",
            LIBTMUX_SOCKET_DIR="/tmp/decoy",
        )
        assert result.returncode == 0, output_of(result)
        seen = set()
        for line in env_log.read_text().splitlines():
            kind, _, value = line.partition(" ")
            assert kind == "tmpdir", f"a cell was handed {value}"
            seen.add(value)
        assert len(seen) == 1, "the cells share one TMUX_TMPDIR"
        (directory,) = seen
        assert directory != str(sweep.sockets)
        assert "lmx." in Path(directory).name
        assert not Path(directory).exists(), "the sweep left its socket directory behind"
        directories.append(directory)
    assert directories[0] != directories[1]


def test_a_socket_directory_holding_a_socket_is_kept(sweep: Sweep, tmp_path: Path) -> None:
    """Deleting it could orphan the server behind the socket, so it stays and fails."""
    install_tmux(sweep.matrix_directory, "3.7c")
    env_log = tmp_path / "go-env"
    result = sweep(
        LIBTMUX_MATRIX_VERSIONS="3.7c",
        LIBTMUX_MATRIX_MODULES=".",
        MATRIX_TEST_ENV_LOG=str(env_log),
        MATRIX_TEST_STALE_SOCKET="1",
    )
    directory = Path(env_log.read_text().splitlines()[0].removeprefix("tmpdir "))
    try:
        assert result.returncode == 1
        assert "a socket remains in TMUX_TMPDIR, so the directory was kept" in result.stderr
        assert (directory / "stale.sock").exists()
    finally:
        (directory / "stale.sock").unlink(missing_ok=True)
        directory.rmdir()


def test_a_cell_that_runs_past_its_timeout_fails_the_sweep(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    """A hung module is a failed cell with a reason, not a sweep that never ends."""
    install_tmux(tmp_path, "3.7c")
    limits: list[object] = []

    def run(command: list[str], **kwargs: object) -> subprocess.CompletedProcess[str]:
        if command[1:] == ["-V"]:
            return subprocess.CompletedProcess(command, 0, "tmux 3.7c", "")
        limits.append(kwargs.get("timeout"))
        raise subprocess.TimeoutExpired(command, matrix.CELL_TIMEOUT)

    monkeypatch.setattr(subprocess, "run", run)
    environ = {"LIBTMUX_MATRIX_VERSIONS": "3.7c", "LIBTMUX_MATRIX_MODULES": ". workspace"}
    assert matrix.sweep(environ, tmp_path, tmp_path) == 1
    assert limits == [matrix.CELL_TIMEOUT, matrix.CELL_TIMEOUT]
    out = capsys.readouterr().out
    assert f"  .              FAILED\n  timed out after {matrix.CELL_TIMEOUT} s\n" in out
    assert "matrix: 2 of 2 cells failed: 3.7c:. 3.7c:workspace" in out
