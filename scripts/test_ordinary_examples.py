#!/usr/bin/env python3
"""Compile and run the displayed Go quick start through an external supervisor."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[1]


def save(path: Path, value: object) -> None:
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


def displayed_source() -> str:
    text = (ROOT / "README.md").read_text(encoding="utf-8")
    block = text.split("<!-- docs:quickstart -->", 1)[1].split("<!-- docs:end -->", 1)[0]
    source = block.split("```go\n", 1)[1].split("\n```", 1)[0].strip() + "\n"
    program = (ROOT / "examples/quickstart/main.go").read_text(encoding="utf-8")
    ordinary = "\n".join(line for line in program.splitlines() if not line.strip().startswith("// docs:"))
    if source.strip() != ordinary.strip():
        raise RuntimeError("README quickstart differs from the complete ordinary program")
    return source


def state(native) -> dict[str, list[str]]:
    return {
        "identity": native("display-message", "-p", "#{pid}|#{start_time}|#{socket_path}"),
        "sessions": native("list-sessions", "-F", "#{session_id}|#{session_name}"),
        "windows": native("list-windows", "-a", "-F", "#{session_id}|#{window_id}|#{window_index}|#{window_name}"),
        "panes": native("list-panes", "-a", "-F", "#{session_id}|#{window_id}|#{pane_id}|#{pane_pid}"),
        "serverOptions": [row for row in native("show-options", "-s") if not row.startswith("@libtmux_owner_generation ")],
        "sessionOptions": native("show-options", "-g"),
        "windowOptions": native("show-options", "-gw"),
        "environment": native("show-environment", "-g"),
    }


def worker(binary: str, condition: str, output: Path) -> None:
    # Explicit selectors apply only to native harness commands. The copied Go
    # program receives endpoint selection through its usual environment API.
    socket = os.environ.get("LIBTMUX_SOCKET_PATH")
    if socket is None:
        socket = str(Path(os.environ["TMUX_TMPDIR"]) / f"tmux-{os.getuid()}" / os.environ["LIBTMUX_SOCKET_NAME"])
    real = shutil.which("tmux")
    if real is None:
        raise RuntimeError("tmux is unavailable")
    def selected(*args: str) -> list[str]:
        result = subprocess.run([real, "-S", socket, *args], check=True, capture_output=True, text=True)
        return result.stdout.splitlines()
    native = selected
    seeded = None
    if condition == "absent":
        if os.path.lexists(socket):
            raise RuntimeError("absent example endpoint was prestarted")
    else:
        native("new-session", "-d", "-s", "user-session", "-n", "notes", "cat")
        native("new-window", "-d", "-t", "user-session", "-n", "monitor", "cat")
        native("split-window", "-d", "-t", "user-session:monitor", "cat")
        native("set-option", "-s", "exit-empty", "on")
        native("set-option", "-g", "base-index", "7")
        native("set-option", "-g", "@user-setting", "keep this value")
        native("set-environment", "-g", "USER_SETTING", "preserved")
        seeded = state(native)
    records = []
    previous = None
    parent = dict(os.environ)
    home = Path(os.environ["TMUX_TMPDIR"]) / "home"
    home.mkdir()
    (home / ".tmux.conf").write_text("set -g @ordinary-config loaded\nset -g base-index 4\n", encoding="utf-8")
    child_environment = parent | {"HOME": str(home), "XDG_CONFIG_HOME": str(home / "xdg"),
                                  "TMUX": "ignored,malformed,context", "TMUX_PANE": "%999"}
    if "LIBTMUX_SOCKET_PATH" in parent:
        child_environment["LIBTMUX_SOCKET_NAME"] = "not-selected"
    for _ in range(2):
        result = subprocess.run([binary], env=child_environment, capture_output=True, text=True, timeout=20)
        if result.returncode != 0 or result.stdout != "workspace ready: logs\n":
            raise RuntimeError(f"ordinary example failed ({result.returncode}): {result.stdout}{result.stderr}")
        current = state(native)
        if condition == "absent" and "@ordinary-config loaded" not in current["sessionOptions"]:
            raise RuntimeError("Ensure bypassed normal configuration loading")
        if any("libtmux-start-" in row for row in current["sessions"]):
            raise RuntimeError("Ensure left a bootstrap session")
        if seeded is not None:
            for key, rows in seeded.items():
                if key in ("sessions", "windows", "panes"):
                    if not set(rows).issubset(current[key]):
                        raise RuntimeError(f"example changed existing {key}")
                elif rows != current[key]:
                    raise RuntimeError(f"example changed existing {key}")
        if previous is not None and current != previous:
            raise RuntimeError("repeated ordinary example changed the workspace")
        previous = current
        records.append(dict(stdout=result.stdout, stderr=result.stderr, state=current))
    if parent != dict(os.environ):
        raise RuntimeError("example test mutated its parent environment")
    save(output, dict(condition=condition, socket=socket, seeded=seeded, runs=records))


def main() -> int:
    if len(sys.argv) == 5 and sys.argv[1] == "--worker":
        worker(sys.argv[2], sys.argv[3], Path(sys.argv[4]))
        return 0
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runner", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--tmux", default="tmux")
    args = parser.parse_args()
    runner = args.runner.resolve(strict=True)
    binary = shutil.which(args.tmux)
    if binary is None:
        parser.error(f"Cannot find tmux: {args.tmux}")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    environment = dict(os.environ)
    env = environment | {"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off"}
    shown = displayed_source()
    source = output / "displayed.go"
    source.write_text(shown, encoding="utf-8")
    receipt = dict(supervisor=str(runner), supervisorSHA256=hashlib.sha256(runner.read_bytes()).hexdigest(),
                   displayedSHA256=hashlib.sha256(shown.encode()).hexdigest(),
                   go=subprocess.check_output(["go", "version"], env=env, text=True).strip(),
                   tmux=subprocess.check_output([binary, "-V"], text=True).strip(), cases=[])
    save(output / "checks.json", receipt)
    for label, target in (("source", "./examples/quickstart"), ("displayed", str(source))):
        compiled = output / label
        subprocess.run(["go", "build", "-race", "-o", str(compiled), target], cwd=ROOT, env=env, check=True)
        for condition in ("absent", "running"):
            for selector in ("path", "name"):
                name = f"{label}-{condition}-{selector}"
                case = output / name
                observations = output / f"{name}.json"
                command = [sys.executable, str(runner), "--output-dir", str(case), "--cwd", str(ROOT),
                           "--tmux", binary, "--server-state", condition, "--socket-mode", selector,
                           "--timeout", "50", "--", sys.executable, str(Path(__file__).resolve()),
                           "--worker", str(compiled), condition, str(observations)]
                status = subprocess.run(command, env=env, check=False).returncode
                result = json.loads((case / "result.json").read_text())
                cleanup = (result.get("allChildExitsObserved") and result.get("rootRemoved")
                           and all(child["exitObserved"] for child in result["processes"])
                           and all(child["exitObservedAt"] <= result["rootRemovedAt"] for child in result["processes"]))
                receipt["cases"].append(dict(name=name, exitCode=status, passed=result.get("passed"), cleanup=cleanup))
                save(output / "checks.json", receipt)
                if status or not result.get("passed") or not cleanup:
                    raise RuntimeError(f"{name} failed; inspect {case}")
    if environment != dict(os.environ):
        raise RuntimeError("ordinary runner mutated the parent environment")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
