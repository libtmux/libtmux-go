"""Order tmux releases the way tmux numbers them."""

import re

RELEASE = re.compile(r"(\d+)\.(\d+)([a-z]?)")

type ReleaseKey = tuple[int, int, str]


def is_release(name: str) -> bool:
    """Report whether a name is a tmux release, as opposed to a branch or a development build.

    >>> is_release("3.7a"), is_release("3.10"), is_release("next-3.9"), is_release("master")
    (True, True, False, False)
    """
    return RELEASE.fullmatch(name) is not None


def release_key(name: str) -> ReleaseKey:
    """Return the sort key of a release: numbers compare as numbers, a letter follows its base.

    >>> release_key("3.7") < release_key("3.7a") < release_key("3.8") < release_key("3.10")
    True
    >>> release_key("master")
    Traceback (most recent call last):
        ...
    ValueError: not a tmux release: master
    """
    match = RELEASE.fullmatch(name)
    if match is None:
        raise ValueError(f"not a tmux release: {name}")
    return int(match[1]), int(match[2]), match[3]


def before(name: str, floor: ReleaseKey) -> bool:
    """Report whether a release is older than ``floor``; a name that is no release is not.

    >>> before("3.5a", (3, 7, "")), before("3.7", (3, 7, "")), before("3.10", (3, 7, ""))
    (True, False, False)
    >>> before("next-3.9", (3, 7, "")), before("master", (3, 7, ""))
    (False, False)
    """
    return is_release(name) and release_key(name) < floor
