"""Tests for drive_mcp.py, against a fake server that speaks the protocol."""

import io
import json
import os
import select
import subprocess
import sys
from pathlib import Path

import pytest

import drive_mcp

SCRIPT = Path(drive_mcp.__file__)
FAKE = Path(__file__).with_name("fake_server.py")


def install_server(directory: Path, **environment: str) -> str:
    """Write an executable that runs the fake server, and return its path."""
    path = directory / "fake-mcp"
    exports = "".join(f"os.environ[{name!r}] = {value!r}\n" for name, value in environment.items())
    path.write_text(f"#!{sys.executable}\nimport os\n{exports}{FAKE.read_text()}")
    path.chmod(0o755)
    return str(path)


def call(name: str, **step: object) -> dict[str, object]:
    return {"method": "tools/call", "params": {"name": name, "arguments": {}}, **step}


async def drive(
    binary: str,
    plan: list[dict[str, object]],
    capsys: pytest.CaptureFixture[str],
    *assignments: str,
    environ: dict[str, str] | None = None,
) -> str:
    await drive_mcp.drive(binary, assignments, plan, environ or {"PATH": "/usr/bin"})
    return capsys.readouterr().out


def test_the_environment_is_built_from_nothing() -> None:
    environment = drive_mcp.build_environment(
        {"PATH": "/bin", "HOME": "/h", "TMUX": "t", "TMUX_PANE": "%1", "LANG": "C", "SECRET": "s"},
        ["TMUX=other", "NAME=a=b"],
    )
    assert environment == {
        "PATH": "/bin",
        "HOME": "/h",
        "TMUX": "other",
        "TMUX_PANE": "%1",
        "LANG": "C",
        "NAME": "a=b",
    }


@pytest.mark.asyncio
async def test_a_plan_is_run_and_each_reply_printed(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    out = await drive(install_server(tmp_path), [call("list_panes")], capsys)
    assert out.startswith('=== initialize\n{\n  "tools": {}\n}\n')
    assert '=== tools/call {"name": "list_panes", "arguments": {}}' in out
    assert '"structuredContent"' in out
    assert "!!!" not in out


@pytest.mark.asyncio
async def test_tools_the_plan_never_called_are_listed(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    out = await drive(install_server(tmp_path), [call("list_panes")], capsys)
    assert "=== 5 of 6 tools were never called\n    ask environment hang never_called wide\n" in out


@pytest.mark.asyncio
async def test_a_tool_that_declares_an_output_schema_and_returns_none_is_flagged(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    binary = install_server(tmp_path, FAKE_UNSTRUCTURED="1")
    out = await drive(binary, [call("list_panes")], capsys)
    assert "!!! list_panes declares an outputSchema and returned none" in out


@pytest.mark.parametrize(
    ("unstructured", "strict", "status"),
    [(False, True, 0), (True, False, 0), (True, True, 1)],
    ids=["clean", "flagged", "flagged-strict"],
)
def test_strict_fails_only_when_a_tool_is_flagged(
    tmp_path: Path, unstructured: bool, strict: bool, status: int
) -> None:
    """``--strict`` makes a flagged tool the exit status; without it the status stays 0."""
    binary = install_server(tmp_path, **({"FAKE_UNSTRUCTURED": "1"} if unstructured else {}))
    argv = [*(["--strict"] if strict else []), binary]
    plan = io.StringIO(json.dumps([call("list_panes")]))
    assert drive_mcp.main(argv, {"PATH": "/usr/bin"}, plan) == status


@pytest.mark.asyncio
async def test_a_server_that_outstays_the_close_is_killed_with_its_children(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The child holds the FIFO too; it reaches end-of-file only when both are dead."""
    fifo = tmp_path / "sleeper.fifo"
    os.mkfifo(fifo)
    reader = os.open(fifo, os.O_RDONLY | os.O_NONBLOCK)
    monkeypatch.setattr(drive_mcp, "CLOSE_TIMEOUT", 0.3)
    try:
        binary = install_server(tmp_path, FAKE_SLEEPER=str(fifo), FAKE_LINGER="1")
        await drive_mcp.drive(binary, [], [], {"PATH": "/usr/bin"})
        assert os.read(reader, 1) == b"x", "the server never started its child"
        ready, _, _ = select.select([reader], [], [], 5)
        assert ready, "a child of the server is still running"
        assert os.read(reader, 1) == b""
    finally:
        os.close(reader)


@pytest.mark.asyncio
async def test_a_question_from_the_server_is_declined(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    out = await drive(install_server(tmp_path), [call("ask")], capsys)
    assert r'"text": "{\"action\": \"decline\"}"' in out


@pytest.mark.asyncio
async def test_notifications_are_reported_after_the_plan(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    out = await drive(install_server(tmp_path), [], capsys)
    assert '=== 1 notifications\n    {"jsonrpc": "2.0", "method": "notifications/message"' in out


@pytest.mark.asyncio
async def test_a_call_that_gets_no_reply_times_out_and_the_plan_goes_on(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    plan = [call("hang", timeout=0.2), call("list_panes")]
    out = await drive(install_server(tmp_path), plan, capsys)
    assert '"message": "no reply within 0s"' in out
    assert '"structuredContent"' in out


@pytest.mark.asyncio
async def test_a_reply_longer_than_64_kib_is_read(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    out = await drive(install_server(tmp_path), [call("wide", maxChars=1 << 20)], capsys)
    assert "x" * (1 << 17) in out


@pytest.mark.asyncio
async def test_a_reply_is_cut_at_max_chars(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    out = await drive(install_server(tmp_path), [call("wide", maxChars=30)], capsys)
    body = out.split("=== tools/call", 1)[1].split("\n", 1)[1].split("\n=== ", 1)[0]
    assert len(body.strip()) <= 30


@pytest.mark.asyncio
async def test_the_environment_the_server_sees_is_replaced_not_inherited(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    out = await drive(
        install_server(tmp_path),
        [call("environment")],
        capsys,
        "LIBTMUX_SOCKET=probe",
        environ={"PATH": "/usr/bin", "HOME": "/h", "SECRET": "s", "TMUX": "t"},
    )
    names = json.loads(json.loads(out.split('"text": ')[1].split("\n")[0]))
    assert {"PATH", "HOME", "TMUX", "LIBTMUX_SOCKET"} <= set(names)
    assert "SECRET" not in names


@pytest.mark.asyncio
async def test_what_the_server_writes_to_stderr_is_reported(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    out = await drive(install_server(tmp_path), [], capsys)
    assert out.endswith("=== stderr\nserver started\nserver stopped\n\n")


@pytest.mark.asyncio
async def test_a_server_that_exits_early_reads_as_a_closed_connection(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    binary = tmp_path / "exits"
    binary.write_text(f"#!{sys.executable}\nraise SystemExit(0)\n")
    binary.chmod(0o755)
    out = await drive(str(binary), [call("list_panes")], capsys)
    assert out.count("the server closed the connection") == 2


def test_the_script_runs_as_a_program(tmp_path: Path) -> None:
    result = subprocess.run(
        [sys.executable, str(SCRIPT), install_server(tmp_path), "FOO=bar"],
        input=json.dumps([call("list_panes")]),
        env={"PATH": "/usr/bin"},
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, result.stderr
    assert "=== tools/list" not in result.stdout
    assert '"structuredContent"' in result.stdout


def test_without_a_binary_it_prints_its_usage(capsys: pytest.CaptureFixture[str]) -> None:
    assert drive_mcp.main([], {}, io.StringIO("[]")) == 2
    assert "Drive the MCP server over stdio JSON-RPC" in capsys.readouterr().err


@pytest.mark.parametrize(
    ("plan", "message"),
    [
        ("not json", "the plan is not JSON"),
        ('{"method": "tools/list"}', "the plan must be a JSON list of steps"),
        ('[{"params": {}}]', 'plan step 1 has no "method"'),
        ('[{"method": "tools/list"}, 7]', 'plan step 2 has no "method"'),
        ('[{"method": "tools/call", "params": "x"}]', 'plan step 1 has "params" that is not'),
    ],
)
def test_a_bad_plan_is_one_line_and_starts_no_server(
    plan: str, message: str, capsys: pytest.CaptureFixture[str]
) -> None:
    assert drive_mcp.main(["/does/not/run"], {}, io.StringIO(plan)) == 1
    error = capsys.readouterr().err
    assert error.startswith(f"drive-mcp: {message}")
    assert error.count("\n") == 1


def test_a_missing_binary_is_one_line(capsys: pytest.CaptureFixture[str]) -> None:
    assert drive_mcp.main(["/does/not/exist"], {}, io.StringIO("[]")) == 1
    assert (
        capsys.readouterr().err
        == "drive-mcp: cannot start /does/not/exist: No such file or directory\n"
    )


@pytest.mark.asyncio
async def test_the_server_is_closed_when_the_plan_fails(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A failure in the middle of a plan still shuts the server down."""
    closed: list[int] = []
    real_close = drive_mcp.Connection.close

    async def close(self: drive_mcp.Connection) -> None:
        closed.append(self.process.pid)
        await real_close(self)

    async def fail(server: drive_mcp.Connection, plan: object) -> None:
        raise RuntimeError("plan failed")

    monkeypatch.setattr(drive_mcp.Connection, "close", close)
    monkeypatch.setattr(drive_mcp, "run_plan", fail)
    with pytest.raises(RuntimeError, match="plan failed"):
        await drive_mcp.drive(install_server(tmp_path), [], [], {"PATH": "/usr/bin"})
    assert len(closed) == 1
