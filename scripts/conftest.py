"""Keep the test commands from leaving compiled files in the checkout.

The repository ignores no build output, because the generate check lists every
untracked file, so a `__pycache__` that a test run leaves behind is one to
commit by mistake.
"""

import os
import sys
from contextlib import suppress
from pathlib import Path

sys.dont_write_bytecode = True
# Scripts that tests start in a subprocess read the variable instead.
os.environ["PYTHONDONTWRITEBYTECODE"] = "1"


def pytest_unconfigure() -> None:
    """Remove the one compiled file written before the setting above took effect."""
    cache = Path(__file__).parent / "__pycache__"
    for compiled in cache.glob("conftest.*.pyc"):
        compiled.unlink()
    with suppress(OSError):
        cache.rmdir()
