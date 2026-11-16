"""A stand-in go that prints the arguments and directory of its call as JSON."""

import json
import sys
from pathlib import Path


def main() -> None:
    print(json.dumps({"argv": sys.argv[1:], "cwd": str(Path.cwd())}))


if __name__ == "__main__":
    main()
