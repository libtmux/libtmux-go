"""Tests for api_diff.py, over a throwaway git repository and a go that prints its call."""

import io
import subprocess
import sys
from pathlib import Path

import pytest

import api_diff

TESTS = Path(__file__).parent


def git(root: Path, *arguments: str) -> None:
    """Run git in ``root`` with a fixed identity."""
    subprocess.run(
        ["git", "-c", "user.name=T", "-c", "user.email=t@example.com", *arguments],
        cwd=root,
        check=True,
        capture_output=True,
    )


@pytest.fixture
def repository(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """A repository with tags for the root and mcp modules, and a go on PATH."""
    root = tmp_path / "repo"
    for module in api_diff.MODULES:
        (root / module).mkdir(parents=True, exist_ok=True)
    git(root, "init", "--quiet")
    git(root, "commit", "--quiet", "--allow-empty", "-m", "first")
    for tag in ["v0.1.2", "v0.1.10", "v0.1.10-alpha.1", "mcp/v0.1.3", "mcp/v0.2.0-alpha.1", "x"]:
        git(root, "tag", tag)
    bin_directory = tmp_path / "bin"
    bin_directory.mkdir()
    go = bin_directory / "go"
    go.write_text(f"#!{sys.executable}\n{(TESTS / 'fake_go.py').read_text()}")
    go.chmod(0o755)
    monkeypatch.setenv("PATH", f"{bin_directory}:/usr/bin")
    return root


def test_each_module_is_compared_with_its_newest_tag(repository: Path) -> None:
    """A nested module's tag loses its directory, a module with no tag says so."""
    summary = io.StringIO()
    assert api_diff.report(repository, "gorelease@v1", summary) == 0
    assert summary.getvalue() == (
        "### . since v0.1.10\n\n```\ngorelease for repo: run gorelease@v1 -base v0.1.10\n```\n\n"
        "### workspace\n\nNo tag to compare against.\n\n"
        "### mcp since mcp/v0.2.0-alpha.1\n\n"
        "```\ngorelease for mcp: run gorelease@v1 -base v0.2.0-alpha.1\n```\n\n"
    )


def test_a_failing_run_is_reported_but_does_not_stop_the_others(
    repository: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("FAKE_GO_FAIL", "repo")
    summary = io.StringIO()
    assert api_diff.report(repository, "g", summary) == 1
    assert "### mcp since" in summary.getvalue()


def test_a_run_past_its_limit_is_a_section_with_the_reason(
    repository: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(api_diff, "RUN_TIMEOUT", 0.3)
    monkeypatch.setenv("FAKE_GO_HANG", "mcp")
    summary = io.StringIO()
    assert api_diff.report(repository, "g", summary) == 124
    assert "```\ngorelease ran past 0.3 s\n```" in summary.getvalue()


def test_main_writes_the_step_summary_and_never_fails_the_job(
    repository: Path,
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    monkeypatch.setattr(api_diff, "ROOT", repository)
    monkeypatch.setenv("FAKE_GO_FAIL", "mcp")
    summary = tmp_path / "summary.md"
    environ = {"GORELEASE": "g", "GITHUB_STEP_SUMMARY": str(summary)}
    assert api_diff.main(environ) == 0
    assert summary.read_text().startswith("### . since v0.1.10")
    assert capsys.readouterr().out == "gorelease reported status 1; this job does not fail on it.\n"


def test_main_needs_the_gorelease_package(capsys: pytest.CaptureFixture[str]) -> None:
    assert api_diff.main({}) == 1
    assert "set GORELEASE" in capsys.readouterr().err
