#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Plan the tmux lanes of the tests workflow.

    ci_plan.py          read .github/ci-matrix.txt, the event and the dispatch
                        inputs, and write the matrices to $GITHUB_OUTPUT
    ci_plan.py verify   read NEEDS_JSON and ALLOW_SKIPPED, and fail unless
                        every job the plan put in scope succeeded

A push or pull request runs the first and last supported release. The whole
table, the development lanes and the refused releases run for a tag push and
for a dispatch with scope=full. A dispatch with scope=custom runs the tmux
versions, tags, branches or commits it names.

Inputs reach this script only through the environment: INPUT_SCOPE,
INPUT_TMUX_REF, INPUT_GO_VERSION, INPUT_OS and INPUT_TMATE. A push or pull
request ignores them. CI_MATRIX names another manifest, for the tests.
"""

import json
import os
import re
import sys
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, NoReturn

MANIFEST = Path(__file__).resolve().parents[2] / ".github" / "ci-matrix.txt"

REF = re.compile(r"[A-Za-z0-9._/-]{1,64}")
TOOLCHAIN = re.compile(r"[A-Za-z0-9._-]{1,32}")
RELEASE = re.compile(r"(\d+)\.(\d+)([a-z]?)")
COMMIT = re.compile(r"[0-9a-f]{40}")

RUNNERS = ("ubuntu-latest", "ubuntu-24.04", "ubuntu-22.04")
TMATE_MODES = ("off", "before-tests", "on-failure")


class PlanError(Exception):
    """A plan or a verification the inputs make impossible."""


def die(message: str) -> NoReturn:
    raise PlanError(message)


def valid_ref(text: str) -> bool:
    """Report whether text may name a tmux version, tag, branch or commit.

    A name that starts with a dash would read as a git option.

    >>> valid_ref("3.3a"), valid_ref("feature/x"), valid_ref("-abc"), valid_ref("3.8;id")
    (True, True, False, False)
    """
    return REF.fullmatch(text) is not None and not text.startswith("-")


def valid_toolchain(text: str) -> bool:
    """Report whether text may name a Go toolchain.

    >>> valid_toolchain("1.26"), valid_toolchain("stable"), valid_toolchain("-1.26")
    (True, True, False)
    """
    return TOOLCHAIN.fullmatch(text) is not None and not text.startswith("-")


def split_list(text: str, label: str) -> list[str]:
    """Split a comma-separated input into its distinct items, in order.

    An empty item is an error: it is how a stray comma would otherwise vanish
    from the plan.

    >>> split_list(" 3.7 , master,3.7", "tmux-ref")
    ['3.7', 'master']
    >>> try:
    ...     split_list("3.8,", "tmux-ref")
    ... except PlanError as error:
    ...     print(error)
    tmux-ref has an empty item: 3.8,
    """
    if not text.strip():
        die(f"{label} is empty")
    items: list[str] = []
    for raw in text.split(","):
        item = raw.strip()
        if not item:
            die(f"{label} has an empty item: {text}")
        if item not in items:
            items.append(item)
    return items


@dataclass
class Manifest:
    """The releases and toolchains CI runs, read from .github/ci-matrix.txt."""

    supported: list[str] = field(default_factory=list)
    refused: list[str] = field(default_factory=list)
    development: list[str] = field(default_factory=list)
    go_versions: list[str] = field(default_factory=list)
    control: str = ""


def parse_manifest(text: str) -> Manifest:
    """Read a manifest: one `kind value` entry per line, `#` starting a comment.

    >>> parse_manifest("supported 3.3a\\nsupported 3.8\\ncontrol 3.7\\ngo 1.26\\n").supported
    ['3.3a', '3.8']
    """
    manifest = Manifest()
    for line in text.splitlines():
        fields = line.split("#", 1)[0].split()
        if not fields:
            continue
        if len(fields) != 2:
            die(f"malformed manifest line: {line.split('#', 1)[0]}")
        kind, value = fields
        match kind:
            case "go":
                if not valid_toolchain(value):
                    die(f"manifest names an invalid toolchain: {value}")
                if value in manifest.go_versions:
                    die(f"manifest repeats go {value}")
                manifest.go_versions.append(value)
            case "control":
                if not valid_ref(value):
                    die(f"manifest names an invalid tmux ref: {value}")
                if not RELEASE.fullmatch(value):
                    die(f"manifest names a control release that is not a tmux release: {value}")
                if manifest.control:
                    die("manifest names two control releases")
                manifest.control = value
            case "supported" | "refused" | "development":
                if not valid_ref(value):
                    die(f"manifest names an invalid tmux ref: {value}")
                if value in (*manifest.supported, *manifest.refused, *manifest.development):
                    die(f"manifest repeats tmux {value}")
                getattr(manifest, kind).append(value)
            case _:
                die(f"manifest has an unknown kind: {kind}")
    if not manifest.supported:
        die("manifest lists no supported tmux release")
    if not manifest.go_versions:
        die("manifest lists no Go toolchain")
    if not manifest.control:
        die("manifest names no control release")
    return manifest


def release_key(name: str) -> tuple[int, int, str]:
    """Order releases as tmux numbers them: a lettered release follows its base.

    >>> release_key("3.7") < release_key("3.7a") < release_key("3.8") < release_key("3.10")
    True
    """
    match = RELEASE.fullmatch(name)
    assert match is not None, name
    return int(match[1]), int(match[2]), match[3]


@dataclass
class Plan:
    """The matrices and job list one run needs."""

    scope: str
    lanes: list[dict[str, Any]]
    rejected: list[str]
    go_versions: list[str]
    os: str
    refs: list[str]

    @property
    def jobs(self) -> int:
        return len(self.lanes) + len(self.rejected)

    @property
    def skip(self) -> str:
        skipped = []
        if not self.lanes:
            skipped.append("tmux-matrix")
        if not self.rejected:
            skipped.append("tmux-rejected")
        return " ".join(skipped)


def make_plan(
    manifest: Manifest,
    event: str,
    ref: str,
    environ: Mapping[str, str],
) -> Plan:
    """Plan the lanes for one event.

    >>> manifest = parse_manifest("supported 3.3a\\nsupported 3.8\\ncontrol 3.7\\ngo 1.26\\n")
    >>> make_plan(manifest, "pull_request", "", {}).refs
    ['3.3a', '3.8']
    """
    dispatch = event == "workflow_dispatch"
    scope = "edges"
    match event:
        case "push":
            if ref.startswith("refs/tags/"):
                scope = "full"
        case "pull_request":
            pass
        case "workflow_dispatch":
            scope = environ.get("INPUT_SCOPE") or "edges"
        case _:
            die(f"no plan for the {event} event")
    if scope not in ("edges", "full", "custom"):
        die(f"scope must be edges, full or custom, got: {scope}")

    runner = "ubuntu-latest"
    tmate = "off"
    lane_go = ["stable"]
    tests_go = list(manifest.go_versions)
    if dispatch:
        if requested := environ.get("INPUT_OS"):
            if requested not in RUNNERS:
                die(f"os must be ubuntu-latest, ubuntu-24.04 or ubuntu-22.04, got: {requested}")
            runner = requested
        tmate = environ.get("INPUT_TMATE") or "off"
        if tmate not in TMATE_MODES:
            die(f"tmate must be off, before-tests or on-failure, got: {tmate}")
        if (versions := environ.get("INPUT_GO_VERSION", "")).strip():
            tests_go = split_list(versions, "go-version")
            for toolchain in tests_go:
                if not valid_toolchain(toolchain):
                    die(f"go-version has an invalid item: {toolchain}")
            lane_go = list(tests_go)

    refs: list[str] = []
    rejected: list[str] = []
    match scope:
        case "edges":
            refs = [manifest.supported[0]]
            if manifest.supported[-1] != manifest.supported[0]:
                refs.append(manifest.supported[-1])
        case "full":
            refs = [*manifest.supported, *manifest.development]
            rejected = list(manifest.refused)
        case "custom":
            for item in split_list(environ.get("INPUT_TMUX_REF", ""), "tmux-ref"):
                if not valid_ref(item):
                    die(
                        "tmux-ref has an invalid item "
                        f"(want ^[A-Za-z0-9._/-]{{1,64}}$, no leading dash): {item}"
                    )
                (rejected if item in manifest.refused else refs).append(item)

    lanes: list[dict[str, Any]] = []
    for name in refs:
        if name in manifest.development:
            expect, advisory, has_control = "tmux next-*", True, True
        elif RELEASE.fullmatch(name):
            expect, advisory = f"tmux {name}", False
            has_control = release_key(manifest.control) <= release_key(name)
        else:
            expect, advisory, has_control = "tmux *", False, True
        label = name[:12] if COMMIT.fullmatch(name) else name
        for toolchain in lane_go:
            lane_label = label if toolchain == "stable" else f"{label}, go {toolchain}"
            lanes.append(
                {
                    "tmux-version": name,
                    "name": lane_label,
                    "go-version": toolchain,
                    "expect": expect,
                    "advisory": advisory,
                    "control": has_control,
                    "tmate": tmate,
                }
            )

    if tmate != "off" and (len(lanes) != 1 or rejected):
        die(
            f"tmate needs exactly one tmux lane and the plan has {len(lanes) + len(rejected)}; "
            "name one tmux-ref and at most one go-version"
        )
    return Plan(scope, lanes, rejected, tests_go, runner, refs)


def compact(value: object) -> str:
    return json.dumps(value, separators=(",", ":"), ensure_ascii=False)


def render_outputs(plan: Plan) -> str:
    """Write the plan the way the workflow reads it from $GITHUB_OUTPUT."""
    return "".join(
        f"{key}={value}\n"
        for key, value in (
            ("scope", plan.scope),
            ("tmux", compact(plan.lanes)),
            ("rejected", compact(plan.rejected)),
            ("go-versions", compact(plan.go_versions)),
            ("os", plan.os),
            ("skip", plan.skip),
            ("jobs", plan.jobs),
        )
    )


def render_summary(plan: Plan) -> str:
    return (
        "### tmux plan\n\n"
        f"- scope: `{plan.scope}`\n"
        f"- tmux lanes: `{' '.join(plan.refs)}`\n"
        f"- refused releases: `{' '.join(plan.rejected)}`\n"
        f"- Go: `{' '.join(plan.go_versions)}`\n"
        f"- planned tmux jobs: {plan.jobs}\n"
    )


def verify(needs_json: str, allow_skipped: str) -> list[str]:
    """List the jobs that did not pass, as `job: result`.

    A job the plan left out of scope may be skipped; every other job has to
    have succeeded.

    >>> verify('{"plan":{"result":"success"},"test":{"result":"failure"}}', "")
    ['test: failure']
    >>> verify('{"plan":{"result":"success"},"tmux-rejected":{"result":"skipped"}}', "tmux-rejected")
    []
    """
    try:
        needs = json.loads(needs_json)
        if not isinstance(needs, dict) or not needs:
            raise ValueError("no jobs to verify")
        results = {}
        for job, need in needs.items():
            if need is not None and not isinstance(need, dict):
                raise ValueError(f"{job} is not an object")
            results[job] = None if need is None else need.get("result")
    except ValueError:
        die("NEEDS_JSON holds no jobs")
    skippable = allow_skipped.split(" ")
    return [
        f"{job}: {'null' if result is None else result}"
        for job, result in results.items()
        if result != "success" and (result != "skipped" or job not in skippable)
    ]


def run(argv: Sequence[str], environ: Mapping[str, str]) -> int:
    try:
        if argv[:1] == ["verify"]:
            needs = environ.get("NEEDS_JSON", "")
            if not needs:
                die("verify needs NEEDS_JSON")
            if failures := verify(needs, environ.get("ALLOW_SKIPPED", "")):
                print(
                    "::error::required: these jobs did not pass:\n" + "\n".join(failures),
                    file=sys.stderr,
                )
                return 1
            print("required: every planned job passed")
            return 0
        if argv:
            die(f"unknown argument: {argv[0]}")

        if not (event := environ.get("GITHUB_EVENT_NAME")):
            die("GITHUB_EVENT_NAME is not set")
        manifest_path = Path(environ["CI_MATRIX"]) if "CI_MATRIX" in environ else MANIFEST
        if not manifest_path.is_file():
            die(f"no manifest at {manifest_path}")
        manifest = parse_manifest(manifest_path.read_text())
        plan = make_plan(manifest, event, environ.get("GITHUB_REF", ""), environ)
    except PlanError as error:
        print(f"::error::ci-plan: {error}", file=sys.stderr)
        return 1

    outputs = render_outputs(plan)
    if output := environ.get("GITHUB_OUTPUT"):
        with Path(output).open("a") as handle:
            handle.write(outputs)
    else:
        sys.stdout.write(outputs)
    if summary := environ.get("GITHUB_STEP_SUMMARY"):
        with Path(summary).open("a") as handle:
            handle.write(render_summary(plan))
    return 0


def main() -> int:
    return run(sys.argv[1:], os.environ)


if __name__ == "__main__":
    sys.exit(main())
