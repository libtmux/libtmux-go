"""A stand-in go for the lane's test runs.

It records each call as a JSON line in FAKE_GO_LOG: the arguments, the working
directory, the first directory on PATH and the LIBTMUX_* variables. For a `-run`
pattern of the form `^(A|B)$` or `^A$` it prints a `--- PASS:` line for each name,
except the names in FAKE_GO_OMIT. A bare prefix pattern prints the names in
FAKE_GO_PREFIX_TESTS. FAKE_GO_STATUS is the exit status; FAKE_GO_HANG makes it sleep.
"""

import json
import os
import sys
import time
from pathlib import Path


def selected(pattern: str) -> list[str]:
    """Return the test names a ``^(A|B)$`` or ``^A$`` pattern selects, if it names them."""
    if not pattern.endswith("$"):
        return [name for name in os.environ.get("FAKE_GO_PREFIX_TESTS", "").split(",") if name]
    return pattern.removeprefix("^").removesuffix("$").strip("()").split("|")


def main() -> int:
    arguments = sys.argv[1:]
    call = {
        "argv": arguments,
        "cwd": str(Path.cwd()),
        "path": os.environ["PATH"].split(os.pathsep)[0],
        "env": {name: value for name, value in os.environ.items() if name.startswith("LIBTMUX_")},
    }
    with Path(os.environ["FAKE_GO_LOG"]).open("a") as log:
        log.write(json.dumps(call) + "\n")
    if os.environ.get("FAKE_GO_HANG"):
        time.sleep(30)
    if "-run" in arguments:
        omitted = os.environ.get("FAKE_GO_OMIT", "").split(",")
        for name in selected(arguments[arguments.index("-run") + 1]):
            if name not in omitted:
                print(f"--- PASS: {name} (0.00s)")
    return int(os.environ.get("FAKE_GO_STATUS", "0"))


if __name__ == "__main__":
    sys.exit(main())
