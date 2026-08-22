#!/usr/bin/env python3
"""Measure any MCP server's surface over stdio, so two can be compared.

Counts what the handshake declares and what the tool list carries, then drives
three calls a client gets wrong -- a value outside a closed set, a field the
tool does not have, an argument of the wrong type -- and reports whether each
was refused. Point it at both servers and diff the two reports.

    ./compare.py '["libtmux-mcp"]' go TMUX_TMPDIR=/tmp/compare
    DRIVE_CWD=<python-checkout> ./compare.py \\
        '["uv","run","--frozen","fastmcp","run","fastmcp.json"]' python

Reading one server's source and the other's schemas is how a comparison goes
wrong: it said both took the same arguments, and running them showed one names
every argument in snake_case and the other in camelCase.
"""
import json, os, subprocess, sys, threading, queue, time
cmd = json.loads(sys.argv[1]); label = sys.argv[2]
env = {"PATH": os.environ["PATH"], "HOME": os.environ["HOME"],
       "TMUX_TMPDIR": "/tmp/pycompare", "LIBTMUX_SAFETY": "destructive"}
for extra in sys.argv[3:]:
    k, _, v = extra.partition("="); env[k] = v
p = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                     stderr=subprocess.PIPE, text=True, env=env, bufsize=1,
                     cwd=os.environ.get("DRIVE_CWD") or None)
out = queue.Queue()
threading.Thread(target=lambda: [out.put(l) for l in p.stdout] or out.put(None), daemon=True).start()
st = {"id": 0}
def rpc(m, params, t=60):
    st["id"] += 1; w = st["id"]
    p.stdin.write(json.dumps({"jsonrpc":"2.0","id":w,"method":m,"params":params}) + "\n"); p.stdin.flush()
    end = time.time() + t
    while time.time() < end:
        line = out.get(timeout=max(.1, end - time.time()))
        if line is None: return {"error": "closed"}
        try: o = json.loads(line)
        except json.JSONDecodeError: continue
        if o.get("id") == w: return o
    return {"error": "timeout"}
init = rpc("initialize", {"protocolVersion":"2025-06-18","capabilities":{},
                          "clientInfo":{"name":"compare","version":"1"}})
p.stdin.write(json.dumps({"jsonrpc":"2.0","method":"notifications/initialized"}) + "\n"); p.stdin.flush()
caps = sorted((init.get("result", {}).get("capabilities") or {}).keys())
tools = rpc("tools/list", {}).get("result", {}).get("tools", [])
prompts = rpc("prompts/list", {}).get("result", {}).get("prompts", [])
res = rpc("resources/list", {}).get("result", {}).get("resources", [])
tpl = rpc("resources/templates/list", {}).get("result", {}).get("resourceTemplates", [])
def props(t): return (t.get("inputSchema") or {}).get("properties") or {}
report = {
  "label": label,
  "capabilities": caps,
  "tools": len(tools),
  "arguments": sum(len(props(t)) for t in tools),
  "enumArgs": sum(1 for t in tools for s in props(t).values() if s.get("enum")),
  "withOutputSchema": sum(1 for t in tools if t.get("outputSchema")),
  "withAnnotations": sum(1 for t in tools if t.get("annotations")),
  "described": sum(1 for t in tools if (t.get("description") or "").strip()),
  "argsDescribed": sum(1 for t in tools for s in props(t).values() if (s.get("description") or "").strip()),
  "nullableLists": sum(1 for t in tools for s in ((t.get("outputSchema") or {}).get("properties") or {}).values()
                       if isinstance(s.get("type"), list) and "array" in s["type"] and "null" in s["type"]),
  "prompts": sorted(x["name"] for x in prompts),
  "resources": len(res), "templates": len(tpl),
  "instructionsChars": len(init.get("result", {}).get("instructions") or ""),
}
# Enum enforcement on the wire.
for name, arguments, tag in [("list_panes", {"detail": "verbose"}, "badEnum"),
                             ("list_panes", {"bogusField": 1}, "unknownField"),
                             ("run_command", {"command": True, "paneId": "%0"}, "wrongType")]:
    r = rpc("tools/call", {"name": name, "arguments": arguments}, t=30)
    result = r.get("result", {}) or {}
    said = "".join(c.get("text","") for c in result.get("content", []) if c.get("type")=="text")
    report[tag] = "refused" if (result.get("isError") or r.get("error")) else "accepted"
    report[tag + "Says"] = (said or json.dumps(r.get("error") or "")[:120])[:120]
print(json.dumps(report, indent=1))
p.stdin.close()
try: p.wait(timeout=10)
except subprocess.TimeoutExpired: p.kill()
err = p.stderr.read()
if err.strip(): print("STDERR:", err[:600], file=sys.stderr)
