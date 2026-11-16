"""A stand-in tmux that reports the release named in the tmux.version file beside it."""

from pathlib import Path


def main() -> None:
    print("tmux", Path(__file__).with_name("tmux.version").read_text().strip())


if __name__ == "__main__":
    main()
