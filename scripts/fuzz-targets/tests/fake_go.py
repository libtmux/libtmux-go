"""A stand-in go for the fuzz script.

It records each call as a JSON line in FAKE_GO_LOG. With `-list` it prints the names in
FAKE_GO_TARGETS (comma separated) that the pattern `^NAME$` selects, then an `ok` line,
as `go test -list` does. Otherwise it prints a progress line. FAKE_GO_STATUS is the exit
status of a fuzz run.
"""

import json
import os
import sys
from pathlib import Path


def main() -> int:
    arguments = sys.argv[1:]
    with Path(os.environ["FAKE_GO_LOG"]).open("a") as log:
        log.write(json.dumps(arguments) + "\n")
    if "-list" in arguments:
        pattern = arguments[arguments.index("-list") + 1]
        known = [name for name in os.environ.get("FAKE_GO_TARGETS", "").split(",") if name]
        print(*[name for name in known if pattern == f"^{name}$"], sep="\n")
        print("ok  \texample/tmux\t0.010s")
        return 0
    print("fuzz: elapsed: 0s, execs: 0")
    return int(os.environ.get("FAKE_GO_STATUS", "0"))


if __name__ == "__main__":
    sys.exit(main())
