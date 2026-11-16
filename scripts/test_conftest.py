"""The test commands leave no compiled files behind."""

import os
import sys


def test_the_test_run_writes_no_bytecode() -> None:
    """The interpreter and every subprocess a test starts skip ``__pycache__``."""
    assert sys.dont_write_bytecode
    assert os.environ["PYTHONDONTWRITEBYTECODE"] == "1"
