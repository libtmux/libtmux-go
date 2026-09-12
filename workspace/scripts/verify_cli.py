#!/usr/bin/env python3
"""Exercise an installed CLI on private sockets and record measured behavior."""

import argparse
import hashlib
import json
import os
import pathlib
import platform
import pty
import select
import signal
import shutil
import statistics
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--iterations", type=int, default=15)
    parser.add_argument("--reference", type=pathlib.Path)
    args = parser.parse_args()
    if args.iterations < 1:
        parser.error("iterations must be positive")
    binary = str(args.binary.resolve())
    report = {"schema_version": 1, "port": "go", "platform": platform.system(),
              "checks": [], "benchmarks": {}, "corpus": [],
              "binary_sha256": hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest(),
              "verifier_sha256": hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()}
    with tempfile.TemporaryDirectory(prefix="wg-", dir="/tmp") as temporary:
        root = pathlib.Path(temporary)
        socket = root / "s"
        env = dict(os.environ)
        for name in ("TMUX", "TMUX_PANE", "PYTHONPATH", "PYTHONSTARTUP", "PYTHONOPTIMIZE"):
            env.pop(name, None)
        python = env.get("TMUX_WORKSPACE_PYTHON", "python3")
        user_base = subprocess.check_output([python, "-c", "import site; print(site.USER_BASE)"], env=env, text=True).strip()
        env.update(HOME=str(root), TMUXP_CONFIGDIR=str(root), PYTHONUSERBASE=user_base,
                   TMUXP_DETECT_TERMINAL_SIZE="0", TMUXP_DEFAULT_COLUMNS="100",
                   TMUXP_DEFAULT_ROWS="30", NO_COLOR="1", TERM="xterm-256color")
        config = root / "tmux.conf"
        config.write_text("set -g default-shell /bin/sh\nset -g default-command 'exec /bin/sh -i'\nset -g status off\nset -g remain-on-exit on\n")
        workspace = root / "sample.yaml"
        workspace.write_text("session_name: sample\nwindows:\n- window_name: editor\n  panes: [blank, blank]\n- window_name: logs\n  panes: [blank]\n")
        load = ["load", "-S", str(socket), "-f", str(config), "-d", "--json", str(workspace)]

        def invoke(arguments, expected=0, extra_env=None, timeout=20):
            result = subprocess.run([binary, *arguments], env=env | (extra_env or {}), cwd=root,
                                    capture_output=True, timeout=timeout)
            if result.returncode != expected:
                raise AssertionError(f"{arguments[0]} returned {result.returncode}, expected {expected}: {result.stderr.decode(errors='replace')}")
            return result

        def tmux(*arguments, check=True):
            return subprocess.run(["tmux", "-S", str(socket), *arguments], env=env,
                                  capture_output=True, text=True, check=check, timeout=10)

        def check(name, action):
            action()
            report["checks"].append({"name": name, "status": "PASS"})

        def measured(name, arguments, setup=None, cleanup=None):
            samples = []
            for i in range(args.iterations):
                if setup:
                    setup(i)
                started = time.perf_counter_ns()
                result = invoke(arguments(i) if callable(arguments) else arguments)
                samples.append((time.perf_counter_ns() - started) / 1_000_000)
                if cleanup:
                    cleanup(i, result)
            ordered = sorted(samples)
            report["benchmarks"][name] = {"unit": "ms", "iterations": len(samples),
                "minimum": min(samples), "median": statistics.median(samples),
                "p95": ordered[min(len(samples)-1, int(len(samples)*0.95))], "samples": samples}

        def matched_reference():
            reference_binary = shutil.which("tmuxp",path=env["PATH"])
            assert reference_binary, "installed tmuxp entrypoint is required"
            version = subprocess.check_output([python,"-c","from importlib.metadata import version; print(version('tmuxp'))"],env=env,text=True).strip()
            assert version=="1.74.0"
            report["matched_reference_version"] = version
            report["matched_benchmarks"] = {}
            matched_env = env | {"TMUXP_PROGRESS":"0"}
            def run_program(program, arguments):
                result = subprocess.run([program,*arguments],env=matched_env,cwd=root,
                                        capture_output=True,timeout=30)
                assert result.returncode==0, result.stderr.decode(errors="replace")
                return result
            def summarize(samples):
                ordered=sorted(samples)
                return {"unit":"ms","iterations":len(samples),"minimum":min(samples),
                        "median":statistics.median(samples),"p95":ordered[min(len(samples)-1,int(len(samples)*0.95))],
                        "samples":samples}
            for boundary,arguments in (("startup-version",["--version"]),
                                       ("discovery-json",["ls","--json"]),
                                       ("search-python-regex",["search","--json","name:sample"])):
                samples={"go":[],"tmuxp":[]}
                for i in range(args.iterations):
                    for name,program in (("go",binary),("tmuxp",reference_binary)) if i%2==0 else (("tmuxp",reference_binary),("go",binary)):
                        started=time.perf_counter_ns()
                        result=run_program(program,arguments)
                        samples[name].append((time.perf_counter_ns()-started)/1_000_000)
                        if boundary!="startup-version": json.loads(result.stdout)
                report["matched_benchmarks"][boundary]={name:summarize(values) for name,values in samples.items()}
            samples={"go":[],"tmuxp":[]}
            for i in range(args.iterations):
                for name,program in (("go",binary),("tmuxp",reference_binary)) if i%2==0 else (("tmuxp",reference_binary),("go",binary)):
                    session=f"matched-{name}-{i}"
                    saved=root/f"matched-{name}-{i}.json"
                    started=time.perf_counter_ns()
                    run_program(program,["load","-S",str(socket),"-f",str(config),"-d","-s",session,str(workspace)])
                    run_program(program,["freeze","-S",str(socket),"-y","-q","-f","json","-o",str(saved),session])
                    samples[name].append((time.perf_counter_ns()-started)/1_000_000)
                    frozen=json.loads(saved.read_text())
                    assert [len(w["panes"]) for w in frozen["windows"]]==[2,1]
                    saved.unlink()
                    tmux("kill-session","-t",session)
            report["matched_benchmarks"]["load-and-freeze-file"]={name:summarize(values) for name,values in samples.items()}
            report["matched_boundaries"] = "Alternating installed CLI processes; identical argv for each boundary; same private server, source document and environment; load/freeze includes atomic file write and excludes topology verification and cleanup."

        def validate_graph():
            graph = json.loads(invoke(["--command-tree"]).stdout)
            assert len(graph["children"]) == 9
            importer = next(child for child in graph["children"] if child["name"] == "import")
            assert len(importer["children"]) == 2
            for child in graph["children"]:
                for leaf in child["children"] or [child]:
                    flags = {f["name"] for f in leaf["flags"] + leaf["inherited_flags"]}
                    assert {"json", "ndjson"} <= flags
            report["command_tree"] = graph
            reference_code = """import argparse,json
from tmuxp.cli import create_parser
from importlib.metadata import version
if version('tmuxp')!='1.74.0': raise RuntimeError('tmuxp 1.74.0 required')
rows=[]
def walk(parser,path):
    actions=[a for a in parser._actions if not isinstance(a,argparse._SubParsersAction) and a.dest!='help']
    rows.append({'command':path,'actions':[{'flags':a.option_strings,'dest':a.dest,'nargs':a.nargs} for a in actions]})
    for action in parser._actions:
        if isinstance(action,argparse._SubParsersAction):
            for name,child in action.choices.items(): walk(child,path+' '+name)
walk(create_parser(),'tmux-workspace')
print(json.dumps(rows))
"""
            rows=json.loads(subprocess.check_output([python,"-c",reference_code],env=env,text=True))
            native={}
            def collect(node):
                flags={"--"+f["name"] for f in node["flags"]+node["inherited_flags"]}
                flags.update("-"+f["shorthand"] for f in node["flags"] if f.get("shorthand"))
                native[node["path"]]=flags
                for child in node["children"]:collect(child)
            collect(graph)
            missing=[]
            for row in rows:
                for action in row["actions"]:
                    for flag in action["flags"]:
                        if flag not in native[row["command"]]:missing.append(row["command"]+" "+flag)
            assert not missing,missing
            definitions=sum(len(row["actions"]) for row in rows)
            spellings=sum(len(action["flags"]) for row in rows for action in row["actions"])
            assert (definitions,spellings)==(61,66),(definitions,spellings)
            report["reference_inventory"]={"argument_definitions":definitions,"flag_spellings":spellings,"missing":missing}

        def native_roundtrip():
            invoke(load)
            frozen = json.loads(invoke(["freeze", "-S", str(socket), "--json", "sample"]).stdout)
            assert len(frozen["windows"]) == 2
            assert sum(len(w["panes"]) for w in frozen["windows"]) == 3
            frozen["session_name"] = "roundtrip"
            (root / "roundtrip.json").write_text(json.dumps(frozen))
            invoke(["load", "-S", str(socket), "-d", "--json", str(root / "roundtrip.json")])
            restored = json.loads(invoke(["freeze", "-S", str(socket), "--json", "roundtrip"]).stdout)
            assert [len(w["panes"]) for w in restored["windows"]] == [2, 1]

        def leaves():
            (root / "tmuxinator.yml").write_text("name: imported\nwindows:\n- editor: vim\n")
            (root / "teamocil.yml").write_text("session:\n  name: imported\n  windows:\n  - name: editor\n    panes:\n    - cmd: vim\n")
            for command in (["ls", "--full", "--json"], ["search", "--json", "(?<=sam)ple"],
                            ["convert", str(workspace), "--json"], ["debug-info", "--json"],
                            ["import", "teamocil", str(root / "teamocil.yml"), "--json"],
                            ["import", "tmuxinator", str(root / "tmuxinator.yml"), "--json"],
                            ["edit", str(workspace), "--json"],
                            ["shell", "-S", str(socket), "-c", "print(session.session_name)", "--json", "sample"]):
                json.loads(invoke(command, extra_env={"EDITOR": "/bin/true"}).stdout)

        def stream():
            streamed = root / "stream.yaml"
            streamed.write_text("session_name: streamed\nbefore_script: /bin/sh -c 'printf first; sleep 0.5; printf second; printf err >&2'\nwindows:\n- panes: [blank]\n")
            started = time.monotonic()
            process = subprocess.Popen([binary, "load", "-S", str(socket), "-d", "--ndjson", str(streamed)],
                                       env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            first_output = None
            records = []
            for line in iter(process.stdout.readline, b""):
                record = json.loads(line)
                records.append(record)
                if record["event"] == "script-output" and first_output is None:
                    first_output = time.monotonic()-started
                    assert process.poll() is None, "events were retained until child exit"
            assert process.wait(timeout=10) == 0, process.stderr.read()
            assert first_output is not None and first_output < 0.5
            assert [r["sequence"] for r in records] == list(range(1,len(records)+1))
            assert records[-1]["event"] == "completed"
            assert sum(r["event"] in {"completed", "failed"} for r in records) == 1
            report["stream"] = {"first_output_ms": first_output*1000, "records": len(records)}

        def terminal_progress():
            master, slave = pty.openpty()
            try:
                child = subprocess.Popen([binary, "load", "-S", str(socket), "-d", "-s", "terminal", "--progress-format", "verbose", str(workspace)],
                                         env=env, stdout=subprocess.PIPE, stderr=slave)
                os.close(slave)
                slave = None
                chunks = []
                deadline = time.monotonic()+10
                while time.monotonic() < deadline:
                    ready, _, _ = select.select([master], [], [], 0.2)
                    if ready:
                        try:
                            value = os.read(master, 65536)
                        except OSError:
                            break
                        if not value:
                            break
                        chunks.append(value)
                    elif child.poll() is not None:
                        break
                assert child.wait(timeout=2) == 0
                text = b"".join(chunks)
                assert b"Loading workspace:" in text and b"\x1b[2K" in text
                assert b"Loaded" in child.stdout.read()
                report["terminal_progress"] = {"stderr_bytes": len(text), "stdout_is_status_only": True}
            finally:
                os.close(master)
                if slave is not None:
                    os.close(slave)

        def tty_client(arguments, expected_session, extra_env=None):
            pid, master = pty.fork()
            if pid == 0:
                os.chdir(root)
                os.execvpe(arguments[0], arguments, env | (extra_env or {}))
            chunks = []
            try:
                deadline = time.monotonic()+10
                attached = False
                while time.monotonic() < deadline:
                    ready, _, _ = select.select([master], [], [], 0.05)
                    if ready:
                        try:
                            chunks.append(os.read(master,65536))
                        except OSError:
                            break
                    clients = tmux("list-clients", "-F", "#{session_name}", check=False)
                    if expected_session in clients.stdout.splitlines():
                        attached = True
                        break
                assert attached, f"no client attached to {expected_session}: {b''.join(chunks)[-400:]!r}"
                return pid, master
            except BaseException:
                os.kill(pid, signal.SIGKILL)
                os.waitpid(pid,0)
                os.close(master)
                raise

        def detach_client(pid, master):
            for client in tmux("list-clients","-F","#{client_name}",check=False).stdout.splitlines():
                tmux("detach-client", "-t", client)
            deadline = time.monotonic()+5
            while time.monotonic() < deadline:
                ready, _, _ = select.select([master], [], [], 0.05)
                if ready:
                    try:
                        os.read(master,65536)
                    except OSError:
                        pass
                child, status = os.waitpid(pid,os.WNOHANG)
                if child:
                    assert os.waitstatus_to_exitcode(status)==0
                    os.close(master)
                    return
                time.sleep(0.01)
            os.kill(pid,signal.SIGKILL)
            os.waitpid(pid,0)
            os.close(master)
            raise AssertionError("client did not exit after detach")

        def attach_and_switch():
            pid, master = tty_client([binary,"load","-S",str(socket),"-s","attached",str(workspace)],"attached")
            detach_client(pid,master)
            pid, master = tty_client(["tmux","-S",str(socket),"attach-session","-t","sample"],"sample")
            try:
                pane = tmux("list-panes","-t","sample","-F","#{pane_id}").stdout.splitlines()[0]
                server_pid = tmux("display-message","-p","#{pid}").stdout.strip()
                invoke(["load","-S",str(socket),"-s","switched","-y",str(workspace)],
                       extra_env={"TMUX":f"{socket},{server_pid},0","TMUX_PANE":pane})
                assert "switched" in tmux("list-clients","-F","#{session_name}").stdout.splitlines()
            finally:
                detach_client(pid,master)

        def negatives():
            for command in (["import", "tmuxinator", "--json"], ["load", "--json", str(workspace)],
                            ["load", "-2", "-8", "-d", "--json", str(workspace)],
                            ["search", "--json"], ["shell", "--code", "--pdb", "--json"],
                            ["freeze", "--workspace-format", "xml", "--json"]):
                result = invoke(command, expected=2)
                assert not result.stdout
                assert json.loads(result.stderr)["code"] == "usage"

        try:
            report["tmux_version"] = subprocess.check_output(["tmux", "-V"], text=True).strip()
            check("command graph and all-leaf machine flags", validate_graph)
            check("native load-freeze-reload correctness", native_roundtrip)
            check("valid invocation of every leaf", leaves)
            check("negative invocation before execution", negatives)
            check("NDJSON flush before child exit", stream)
            check("terminal progress uses stderr", terminal_progress)
            check("foreground attach and current client switching", attach_and_switch)
            for shell in ("bash", "zsh", "fish", "powershell"):
                assert invoke(["--generate-completion", shell]).stdout
            for format in ("markdown", "man", "yaml"):
                assert b"tmuxinator" in invoke(["--generate-docs", format]).stdout
            measured("startup-version", ["--version"])
            measured("discovery-json", ["ls", "--json"])
            measured("search-python-regex", ["search", "--json", "name:sample"])
            measured("capture-json", ["freeze", "-S", str(socket), "--json", "sample"])
            def finish_load(i, result):
                value = json.loads(result.stdout)
                assert value["status"] == "ok"
                frozen = json.loads(invoke(["freeze", "-S", str(socket), "--json", f"bench-{i}"]).stdout)
                assert [len(w["panes"]) for w in frozen["windows"]] == [2,1]
                tmux("kill-session", "-t", f"bench-{i}")
            measured("load-native", lambda i: [*load, "-s", f"bench-{i}"], cleanup=finish_load)
            if args.reference:
                check("matched pinned tmuxp CLI boundaries", matched_reference)
                reference = args.reference.resolve()
                report["reference_commit"] = subprocess.check_output(["git", "-C", str(reference), "rev-parse", "HEAD"], text=True).strip()
                (root / "test").mkdir()
                script = root / "test3.sh"
                script.write_text("#!/bin/sh\nexit 0\n")
                script.chmod(0o700)
                fixture_env = {"MY_ENV_VAR": str(root), "PWD": str(root), "USER": "audit",
                               "MAIN_PANE_HEIGHT": "10", "SERVER_PORT": "8000"}
                fixtures = sorted((reference / "examples").glob("*.yaml"))
                assert len(fixtures) == 23
                for i, source in enumerate(fixtures):
                    original = source.read_bytes()
                    fixture = root / "fixture.yaml"
                    fixture.write_bytes(original)
                    parsed = json.loads(invoke(["convert", str(fixture), "--json"]).stdout)
                    expected = [len(w.get("panes", [None])) for w in parsed["windows"]]
                    started = time.monotonic()
                    result = subprocess.run([binary, "load", "-S", str(socket), "-f", str(config),
                        "-d", "-s", f"corpus-{i}", "--json", str(fixture)], env=env | fixture_env,
                        cwd=root, capture_output=True, timeout=90)
                    row = {"fixture":source.name, "sha256":hashlib.sha256(original).hexdigest(),
                           "parse":"PASS", "load_ms": (time.monotonic()-started)*1000}
                    if result.returncode:
                        message = (result.stderr+result.stdout).decode(errors="replace").replace(str(root),"<fixture>").replace(str(reference),"<reference>")
                        row.update(load="FAIL", diagnostic=message)
                        if source.name == "plugin-system.yaml" and "tmuxp_plugin_extended_build" in message:
                            row["load"] = "DEPENDENCY_MISSING"
                    else:
                        frozen = json.loads(invoke(["freeze", "-S", str(socket), "--json", f"corpus-{i}"]).stdout)
                        actual = [len(w["panes"]) for w in frozen["windows"]]
                        row.update(load="PASS" if actual == expected else "FAIL", panes=actual, expected_panes=expected)
                    row["application_readiness"] = "not asserted; fixture programs may be unavailable"
                    report["corpus"].append(row)
                    tmux("kill-session", "-t", f"corpus-{i}", check=False)
                    print(json.dumps({"fixture":source.name,"load":row["load"]}),flush=True)
        finally:
            tmux("kill-server", check=False)
    report["status"] = "PASS" if all(row["load"]=="PASS" for row in report["corpus"]) else "PARTIAL"
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2)+"\n")
    print(json.dumps({"status":report["status"], "checks":len(report["checks"]),
                      "benchmarks":{k:v["median"] for k,v in report["benchmarks"].items()}}))


if __name__ == "__main__":
    main()
