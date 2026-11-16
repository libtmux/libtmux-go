"""A stand-in go that prints what a gorelease run would, for the module it runs in.

FAKE_GO_FAIL names a module directory whose run exits 1, and FAKE_GO_HANG one whose
run never ends.
"""

import os
import sys
import time
from pathlib import Path


def main() -> int:
    module = Path.cwd().name
    print(f"gorelease for {module}: {' '.join(sys.argv[1:])}")
    if os.environ.get("FAKE_GO_HANG") == module:
        time.sleep(30)
    return 1 if os.environ.get("FAKE_GO_FAIL") == module else 0


if __name__ == "__main__":
    sys.exit(main())
