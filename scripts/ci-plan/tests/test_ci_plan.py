"""Tests for ci_plan.py: the lanes each event plans, and the refusals."""

import json
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Protocol

import pytest

import ci_plan

SCRIPT = Path(ci_plan.__file__)
REAL_MANIFEST = Path(__file__).resolve().parents[3] / ".github" / "ci-matrix.txt"

# A middle release, a development lane and refused releases, so each rule of the
# plan has something to include or leave out.
MANIFEST = """
# a comment
supported 3.3a
supported 3.4
supported 3.7  # trailing comment
supported 3.8

refused 3.2a
refused 3.3

development master

control 3.7

go 1.26
go 1.27
"""

PUSH = {"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/master"}
PULL_REQUEST = {"GITHUB_EVENT_NAME": "pull_request", "GITHUB_REF": "refs/pull/7/merge"}
TAG = {"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/tags/v0.1.1"}
DISPATCH = {"GITHUB_EVENT_NAME": "workflow_dispatch"}
COMMIT = "0123456789abcdef0123456789abcdef01234567"


@dataclass
class Planned:
    scope: str
    lanes: list[dict[str, object]]
    rejected: list[str]
    go: list[str]
    os: str
    skip: str
    jobs: str

    @property
    def versions(self) -> list[str]:
        return [str(lane["tmux-version"]) for lane in self.lanes]


def run_plan(
    tmp_path: Path,
    manifest: str,
    environ: dict[str, str],
    capsys: pytest.CaptureFixture[str],
) -> tuple[int, str, Planned | None]:
    manifest_path = tmp_path / "ci-matrix.txt"
    manifest_path.write_text(manifest)
    output = tmp_path / "output"
    status = ci_plan.run(
        [], {**environ, "CI_MATRIX": str(manifest_path), "GITHUB_OUTPUT": str(output)}
    )
    stderr = capsys.readouterr().err
    if status:
        return status, stderr, None
    fields = dict(line.split("=", 1) for line in output.read_text().splitlines())
    return (
        status,
        stderr,
        Planned(
            scope=fields["scope"],
            lanes=json.loads(fields["tmux"]),
            rejected=json.loads(fields["rejected"]),
            go=json.loads(fields["go-versions"]),
            os=fields["os"],
            skip=fields["skip"],
            jobs=fields["jobs"],
        ),
    )


class PlanFixture(Protocol):
    def __call__(self, environ: dict[str, str], manifest: str = ..., /) -> Planned: ...


class RefusalFixture(Protocol):
    def __call__(self, environ: dict[str, str], manifest: str = ..., /) -> str: ...


@pytest.fixture
def plan(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> PlanFixture:
    """Plan an event and return what it wrote."""

    def plan(environ: dict[str, str], manifest: str = MANIFEST, /) -> Planned:
        status, stderr, planned = run_plan(tmp_path, manifest, environ, capsys)
        assert status == 0, stderr
        assert planned is not None
        return planned

    return plan


@pytest.fixture
def refusal(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> RefusalFixture:
    """Plan an event that must fail and return what it said."""

    def refusal(environ: dict[str, str], manifest: str = MANIFEST, /) -> str:
        status, stderr, planned = run_plan(tmp_path, manifest, environ, capsys)
        assert status == 1, f"planned {planned}"
        return stderr

    return refusal


@pytest.mark.parametrize("event", [PUSH, PULL_REQUEST], ids=["push", "pull request"])
def test_push_and_pull_request_run_only_the_edges(plan: PlanFixture, event: dict[str, str]) -> None:
    planned = plan(event)
    assert planned.versions == ["3.3a", "3.8"]
    assert planned.rejected == []
    assert (planned.scope, planned.jobs, planned.skip) == ("edges", "2", "tmux-rejected")
    assert planned.go == ["1.26", "1.27"]
    assert planned.os == "ubuntu-latest"


@pytest.mark.parametrize(
    "event",
    [TAG, {**DISPATCH, "INPUT_SCOPE": "full"}],
    ids=["tag push", "dispatch full"],
)
def test_a_tag_and_full_scope_run_the_whole_table(plan: PlanFixture, event: dict[str, str]) -> None:
    planned = plan(event)
    assert planned.versions == ["3.3a", "3.4", "3.7", "3.8", "master"]
    assert planned.rejected == ["3.2a", "3.3"]
    assert (planned.scope, planned.jobs, planned.skip) == ("full", "7", "")
    assert planned.lanes[-1]["advisory"] is True
    assert planned.lanes[-1]["expect"] == "tmux next-*"
    assert planned.lanes[0]["advisory"] is False


def test_lanes_at_or_after_the_control_release_have_the_control_client(plan: PlanFixture) -> None:
    planned = plan({**DISPATCH, "INPUT_SCOPE": "full"})
    assert {lane["tmux-version"]: lane["control"] for lane in planned.lanes} == {
        "3.3a": False,
        "3.4": False,
        "3.7": True,
        "3.8": True,
        "master": True,
    }


def test_a_push_ignores_dispatch_inputs(plan: PlanFixture) -> None:
    planned = plan(
        {
            **PUSH,
            "INPUT_SCOPE": "full",
            "INPUT_TMUX_REF": "3.4",
            "INPUT_TMATE": "before-tests",
            "INPUT_GO_VERSION": "1.99",
            "INPUT_OS": "ubuntu-22.04",
        }
    )
    assert planned.versions == ["3.3a", "3.8"]
    assert {(lane["tmate"], lane["go-version"]) for lane in planned.lanes} == {("off", "stable")}
    assert (planned.os, planned.go) == ("ubuntu-latest", ["1.26", "1.27"])


def test_custom_scope_runs_the_named_refs(plan: PlanFixture) -> None:
    planned = plan(
        {
            **DISPATCH,
            "INPUT_SCOPE": "custom",
            "INPUT_TMUX_REF": f" 3.7 , master,feature/x,{COMMIT},3.7,3.3,3.9",
        }
    )
    assert planned.versions == ["3.7", "master", "feature/x", COMMIT, "3.9"]
    assert planned.rejected == ["3.3"]
    lanes = {str(lane["tmux-version"]): lane for lane in planned.lanes}
    assert {version: lane["expect"] for version, lane in lanes.items()} == {
        "3.7": "tmux 3.7",
        "master": "tmux next-*",
        "feature/x": "tmux *",
        COMMIT: "tmux *",
        "3.9": "tmux 3.9",
    }
    assert lanes[COMMIT]["name"] == COMMIT[:12]
    assert all(lanes[name]["control"] for name in ("3.9", COMMIT, "feature/x"))
    assert (planned.scope, planned.jobs) == ("custom", "6")


def test_custom_scope_can_name_only_a_refused_release(plan: PlanFixture) -> None:
    planned = plan({**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "3.2a"})
    assert planned.lanes == []
    assert planned.rejected == ["3.2a"]
    assert planned.skip == "tmux-matrix"


def test_stack_inputs_reach_the_lanes_and_the_module_jobs(plan: PlanFixture) -> None:
    planned = plan(
        {
            **DISPATCH,
            "INPUT_SCOPE": "custom",
            "INPUT_TMUX_REF": "3.4,3.8",
            "INPUT_GO_VERSION": "1.26, stable",
            "INPUT_OS": "ubuntu-24.04",
        }
    )
    assert planned.go == ["1.26", "stable"]
    assert planned.os == "ubuntu-24.04"
    assert [lane["name"] for lane in planned.lanes] == [
        "3.4, go 1.26",
        "3.4",
        "3.8, go 1.26",
        "3.8",
    ]


def test_tmate_goes_to_exactly_one_lane(plan: PlanFixture) -> None:
    planned = plan(
        {**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "3.8", "INPUT_TMATE": "on-failure"}
    )
    assert [lane["tmate"] for lane in planned.lanes] == ["on-failure"]


@pytest.mark.parametrize(
    "inputs",
    [
        {"INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "3.7,3.8", "INPUT_TMATE": "before-tests"},
        {
            "INPUT_SCOPE": "custom",
            "INPUT_TMUX_REF": "3.8",
            "INPUT_GO_VERSION": "1.26,1.27",
            "INPUT_TMATE": "on-failure",
        },
        {"INPUT_SCOPE": "edges", "INPUT_TMATE": "on-failure"},
        {"INPUT_SCOPE": "full", "INPUT_TMATE": "on-failure"},
        {"INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "3.8,3.3", "INPUT_TMATE": "on-failure"},
    ],
    ids=["two refs", "two toolchains", "the edges", "the whole table", "a refused lane"],
)
def test_tmate_is_refused_on_more_than_one_job(
    refusal: RefusalFixture, inputs: dict[str, str]
) -> None:
    assert "tmate needs exactly one tmux lane" in refusal({**DISPATCH, **inputs})


@pytest.mark.parametrize(
    ("environ", "message"),
    [
        ({"GITHUB_EVENT_NAME": "schedule"}, "no plan for the schedule event"),
        ({}, "GITHUB_EVENT_NAME"),
        ({**DISPATCH, "INPUT_SCOPE": "everything"}, "scope must be"),
        ({**DISPATCH, "INPUT_SCOPE": "custom"}, "tmux-ref is empty"),
        ({**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "  "}, "tmux-ref is empty"),
        ({**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "3.8,,3.7"}, "empty item"),
        ({**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "3.8,"}, "empty item"),
        ({**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "3.8;id"}, "invalid item"),
        ({**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "$(id)"}, "invalid item"),
        ({**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "3.8 3.7"}, "invalid item"),
        ({**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "a" * 65}, "invalid item"),
        ({**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "-abc"}, "invalid item"),
        ({**DISPATCH, "INPUT_OS": "macos-latest"}, "os must be"),
        ({**DISPATCH, "INPUT_TMATE": "always"}, "tmate must be"),
        ({**DISPATCH, "INPUT_GO_VERSION": "1.26;id"}, "invalid item"),
        ({**DISPATCH, "INPUT_GO_VERSION": "1.26,,1.27"}, "empty item"),
        ({**DISPATCH, "INPUT_GO_VERSION": "-1.26"}, "invalid item"),
    ],
)
def test_it_refuses_what_it_cannot_plan(
    refusal: RefusalFixture, environ: dict[str, str], message: str
) -> None:
    assert message in refusal(environ)


@pytest.mark.parametrize(
    "environ",
    [
        PUSH,
        {"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/tags/v1"},
        {"GITHUB_EVENT_NAME": "pull_request"},
        DISPATCH,
        {**DISPATCH, "INPUT_SCOPE": "edges"},
        {**DISPATCH, "INPUT_SCOPE": "full"},
        {**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "3.4"},
        {**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": "3.3"},
    ],
)
def test_every_plan_has_a_tmux_job(plan: PlanFixture, environ: dict[str, str]) -> None:
    planned = plan(environ)
    assert planned.jobs != "0"
    assert planned.lanes or planned.rejected


def test_the_longest_ref_is_accepted(plan: PlanFixture) -> None:
    longest = "a" * 64
    planned = plan({**DISPATCH, "INPUT_SCOPE": "custom", "INPUT_TMUX_REF": longest})
    assert planned.versions == [longest]


VALID = "supported 3.3a\nsupported 3.8\ncontrol 3.7\ngo 1.26\n"


@pytest.mark.parametrize(
    ("manifest", "message"),
    [
        ("control 3.7\ngo 1.26\n", "no supported tmux release"),
        ("supported 3.3a\ncontrol 3.7\n", "no Go toolchain"),
        ("supported 3.3a\ngo 1.26\n", "no control release"),
        (VALID + "control 3.8\n", "two control releases"),
        (VALID + "preview 3.9\n", "unknown kind"),
        (VALID + "refused 3.3a\n", "repeats tmux 3.3a"),
        (VALID + "go 1.26\n", "repeats go 1.26"),
        (VALID + "refused\n", "malformed"),
        (VALID + "refused 3.2a 3.1c\n", "malformed"),
        (VALID + "refused 3.2a|id\n", "invalid tmux ref"),
        (VALID + "go $HOME\n", "invalid toolchain"),
        ("supported 3.3a\ncontrol master\ngo 1.26\n", "not a tmux release"),
    ],
    ids=[
        "no supported release",
        "no toolchain",
        "no control release",
        "two control releases",
        "an unknown kind",
        "a repeated release",
        "a repeated toolchain",
        "a line without value",
        "a line with extra",
        "a release with a pipe",
        "a toolchain with a $",
        "a control release that is a branch",
    ],
)
def test_it_refuses_a_manifest_it_cannot_trust(
    refusal: RefusalFixture, manifest: str, message: str
) -> None:
    assert message in refusal(PUSH, manifest)


def test_a_single_supported_release_has_one_edge(plan: PlanFixture) -> None:
    planned = plan(PULL_REQUEST, "supported 3.8\ncontrol 3.7\ngo 1.26\n")
    assert planned.versions == ["3.8"]


def test_the_real_manifest_runs_its_first_and_last_release(plan: PlanFixture) -> None:
    supported = [
        fields[1]
        for line in REAL_MANIFEST.read_text().splitlines()
        if (fields := line.split("#", 1)[0].split()) and fields[0] == "supported"
    ]
    planned = plan(PULL_REQUEST, REAL_MANIFEST.read_text())
    assert planned.versions == [supported[0], supported[-1]]


def test_the_plan_is_appended_to_the_output_and_summarised(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    manifest = tmp_path / "ci-matrix.txt"
    manifest.write_text(VALID)
    output = tmp_path / "output"
    output.write_text("earlier=1\n")
    summary = tmp_path / "summary"
    assert (
        ci_plan.run(
            [],
            {
                **PULL_REQUEST,
                "CI_MATRIX": str(manifest),
                "GITHUB_OUTPUT": str(output),
                "GITHUB_STEP_SUMMARY": str(summary),
            },
        )
        == 0
    )
    lines = output.read_text().splitlines()
    assert lines[0] == "earlier=1"
    assert lines[1:] == [
        "scope=edges",
        (
            'tmux=[{"tmux-version":"3.3a","name":"3.3a","go-version":"stable",'
            '"expect":"tmux 3.3a","advisory":false,"control":false,"tmate":"off"},'
            '{"tmux-version":"3.8","name":"3.8","go-version":"stable",'
            '"expect":"tmux 3.8","advisory":false,"control":true,"tmate":"off"}]'
        ),
        "rejected=[]",
        'go-versions=["1.26"]',
        "os=ubuntu-latest",
        "skip=tmux-rejected",
        "jobs=2",
    ]
    assert summary.read_text() == (
        "### tmux plan\n\n"
        "- scope: `edges`\n"
        "- tmux lanes: `3.3a 3.8`\n"
        "- refused releases: ``\n"
        "- Go: `1.26`\n"
        "- planned tmux jobs: 2\n"
    )
    assert capsys.readouterr().err == ""


def verify(needs: str, allow: str, capsys: pytest.CaptureFixture[str]) -> tuple[int, str]:
    status = ci_plan.run(["verify"], {"NEEDS_JSON": needs, "ALLOW_SKIPPED": allow})
    captured = capsys.readouterr()
    return status, captured.out + captured.err


@pytest.mark.parametrize(
    ("needs", "allow", "failure"),
    [
        ('{"plan":{"result":"success"},"test":{"result":"success"}}', "", None),
        (
            '{"plan":{"result":"success"},"tmux-rejected":{"result":"skipped"}}',
            "tmux-rejected",
            None,
        ),
        ('{"plan":{"result":"success"},"test":{"result":"failure"}}', "", "test: failure"),
        ('{"plan":{"result":"success"},"test":{"result":"cancelled"}}', "", "test: cancelled"),
        (
            '{"plan":{"result":"success"},"tmux-matrix":{"result":"skipped"}}',
            "tmux-rejected",
            "tmux-matrix: skipped",
        ),
        (
            '{"plan":{"result":"failure"},"tmux-matrix":{"result":"skipped"}}',
            "",
            "plan: failure",
        ),
        ('{"plan":{"result":"success"},"test":{}}', "", "test: null"),
        ("{}", "", "holds no jobs"),
        ("not json", "", "holds no jobs"),
    ],
    ids=[
        "all succeeded",
        "a planned-out job skipped",
        "a job failed",
        "a job was cancelled",
        "a job the plan kept in scope was skipped",
        "the plan failed and skipped the rest",
        "a job with no result",
        "no jobs at all",
        "not JSON",
    ],
)
def test_verify_passes_only_when_every_planned_job_passed(
    capsys: pytest.CaptureFixture[str], needs: str, allow: str, failure: str | None
) -> None:
    status, output = verify(needs, allow, capsys)
    if failure is None:
        assert status == 0, output
    else:
        assert status == 1
        assert failure in output


def test_verify_needs_its_input(capsys: pytest.CaptureFixture[str]) -> None:
    assert ci_plan.run(["verify"], {}) == 1
    assert "verify needs NEEDS_JSON" in capsys.readouterr().err


def test_an_unknown_argument_is_refused(capsys: pytest.CaptureFixture[str]) -> None:
    assert ci_plan.run(["frobnicate"], {}) == 1
    assert "unknown argument: frobnicate" in capsys.readouterr().err


def test_the_script_runs_as_a_program(tmp_path: Path) -> None:
    output = tmp_path / "output"
    result = subprocess.run(
        [sys.executable, str(SCRIPT)],
        env={
            "GITHUB_EVENT_NAME": "pull_request",
            "GITHUB_OUTPUT": str(output),
            "PATH": "",
        },
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, result.stderr
    assert "scope=edges\n" in output.read_text()
