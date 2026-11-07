"""Regressions for reusing native executables on CI runners."""

from __future__ import annotations

import hashlib
import json
import pathlib
import shutil
import subprocess

import pytest
import tmux_build


@pytest.fixture
def native() -> tuple[pathlib.Path, dict[str, str]]:
    """Use the runner's real tmux for validation and private daemon checks."""
    executable = shutil.which("tmux")
    assert executable is not None
    binary = pathlib.Path(executable).resolve()
    version = (
        subprocess.run([str(binary), "-V"], check=True, capture_output=True, text=True)
        .stdout.strip()
        .removeprefix("tmux ")
    )
    return binary, {"version": version if version in tmux_build._RELEASES else "master"}


def _install(prefix: pathlib.Path, native: pathlib.Path) -> pathlib.Path:
    binary = prefix / "bin" / "tmux"
    binary.parent.mkdir(parents=True)
    shutil.copy2(native, binary)
    return binary


def _record(prefix: pathlib.Path, identity: dict[str, str]) -> None:
    digest = hashlib.sha256((prefix / "bin" / "tmux").read_bytes()).hexdigest()
    (prefix / "build.json").write_text(
        json.dumps({"identity": identity, "sha256": digest})
    )


@pytest.mark.parametrize("fault", ["loader", "version", "identity", "hash", "metadata"])
def test_broken_cached_executable_is_rebuilt(
    tmp_path: pathlib.Path,
    monkeypatch: pytest.MonkeyPatch,
    native: tuple[pathlib.Path, dict[str, str]],
    fault: str,
) -> None:
    """Replace slow compilation only; run validation against a real executable."""
    installed, identity = native
    prefix = tmp_path / "tmux"
    binary = _install(prefix, installed)
    if fault == "loader":
        binary.write_text("#!/bin/sh\nexit 127\n")
    elif fault == "version":
        binary.write_text("#!/bin/sh\necho 'unrelated executable'\n")
    _record(prefix, identity if fault != "identity" else {"version": "other"})
    if fault == "hash":
        with binary.open("ab") as output:
            output.write(b"changed")
    elif fault == "metadata":
        (prefix / "build.json").write_text("{")
    stale = prefix / "old-object.o"
    stale.touch()
    sibling = tmp_path / "borrowed"
    sibling.write_text("keep")

    def compile_fresh(destination: pathlib.Path, _: dict[str, str]) -> None:
        assert not destination.exists()
        _install(destination, installed)

    monkeypatch.setattr(tmux_build, "_build", compile_fresh)
    assert tmux_build._prepare(prefix, identity)
    assert not stale.exists()
    assert sibling.read_text() == "keep"
    assert binary.read_bytes() == installed.read_bytes()
    assert tmux_build._valid(prefix, identity)


def test_valid_cache_skips_build_and_leaves_other_servers_alive(
    tmp_path: pathlib.Path,
    monkeypatch: pytest.MonkeyPatch,
    native: tuple[pathlib.Path, dict[str, str]],
) -> None:
    """Avoid compilation on a valid hit; exercise real startup and cleanup."""
    installed, identity = native
    prefix = tmp_path / "tmux"
    binary = _install(prefix, installed)
    _record(prefix, identity)
    inode = binary.stat().st_ino

    def unexpected_build(_: pathlib.Path, __: dict[str, str]) -> None:
        pytest.fail("A validated cache must not rebuild")

    monkeypatch.setattr(tmux_build, "_build", unexpected_build)
    monkeypatch.delenv("TMUX", raising=False)
    monkeypatch.delenv("TMUX_PANE", raising=False)
    monkeypatch.setenv("SHELL", "/bin/sh")
    monkeypatch.setenv("TERM", "xterm-256color")
    command = [str(installed), "-f", "/dev/null", "-S", str(tmp_path / "borrowed.sock")]
    subprocess.run(
        [*command, "new-session", "-d", "-s", "keep", "exec cat"], check=True
    )
    try:
        assert not tmux_build._prepare(prefix, identity)
        assert binary.stat().st_ino == inode
        subprocess.run([*command, "has-session", "-t", "keep"], check=True)
    finally:
        subprocess.run([*command, "kill-server"], check=True)


def test_symlink_cache_does_not_delete_its_target(
    tmp_path: pathlib.Path,
    monkeypatch: pytest.MonkeyPatch,
    native: tuple[pathlib.Path, dict[str, str]],
) -> None:
    """Replace compilation only; discard the cache link without following it."""
    installed, identity = native
    borrowed = tmp_path / "borrowed"
    original = _install(borrowed, installed)
    _record(borrowed, identity)
    prefix = tmp_path / "tmux"
    prefix.symlink_to(borrowed, target_is_directory=True)

    def compile_fresh(destination: pathlib.Path, _: dict[str, str]) -> None:
        _install(destination, installed)

    monkeypatch.setattr(tmux_build, "_build", compile_fresh)
    assert tmux_build._prepare(prefix, identity)
    assert not prefix.is_symlink()
    assert original.read_bytes() == installed.read_bytes()


def test_failed_build_does_not_write_a_valid_cache_record(
    tmp_path: pathlib.Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Inject a compiler failure; a partial installation must remain invalid."""
    prefix = tmp_path / "tmux"

    def fail_build(destination: pathlib.Path, _: dict[str, str]) -> None:
        destination.mkdir()
        msg = "compiler failed"
        raise RuntimeError(msg)

    monkeypatch.setattr(tmux_build, "_build", fail_build)
    with pytest.raises(RuntimeError, match="compiler failed"):
        tmux_build._prepare(prefix, {"version": "3.6"})
    assert not (prefix / "build.json").exists()


@pytest.mark.parametrize(
    "field", ["image", "arch", "source", "recipe", "compiler", "packages"]
)
def test_changed_build_environment_cannot_restore_the_same_key(field: str) -> None:
    """A source, recipe, toolchain, or runner change invalidates the binary."""
    identity = {
        "version": "3.6",
        "image": "ubuntu24-20260920.1",
        "arch": "x86_64",
        "source": "source-a",
        "recipe": "recipe-a",
        "compiler": "compiler-a",
        "packages": "packages-a",
    }
    changed = dict(identity)
    changed[field] += "-changed"
    assert tmux_build._cache_key(identity) != tmux_build._cache_key(changed)


@pytest.mark.parametrize("field", ["ImageOS", "ImageVersion"])
def test_missing_runner_image_is_rejected_before_source_resolution(
    monkeypatch: pytest.MonkeyPatch, field: str
) -> None:
    """An unbound runner variable must not produce another version-only key."""
    monkeypatch.setenv("ImageOS", "ubuntu26")
    monkeypatch.setenv("ImageVersion", "20261105.1")
    monkeypatch.setenv(field, "")
    with pytest.raises(ValueError, match="runner image"):
        tmux_build._identity("master")


_AUTOGEN = r"""cat > configure <<'CONFIGURE'
#!/bin/sh
for argument in "$@"; do
	case $argument in --prefix=*) prefix=${argument#--prefix=} ;; esac
done
{
	printf 'PREFIX = %s\n' "$prefix"
	printf 'all:\n\t@true\n'
	printf 'install:\n\tmkdir -p $(PREFIX)/bin\n'
	printf '\techo "#!/bin/sh" > $(PREFIX)/bin/tmux\n'
	printf '\techo "echo tmux $$(cat VERSION)" >> $(PREFIX)/bin/tmux\n'
	printf '\tchmod +x $(PREFIX)/bin/tmux\n'
} > Makefile
CONFIGURE
chmod +x configure
"""


@pytest.fixture
def fixture(tmp_path: pathlib.Path, monkeypatch: pytest.MonkeyPatch) -> dict[str, str]:
    """Serve a repository shaped like tmux's: a tag, a branch and a master."""
    repository = tmp_path / "upstream"
    repository.mkdir()

    def git(*args: str) -> str:
        return subprocess.run(
            [
                "git",
                "-c",
                "user.name=fixture",
                "-c",
                "user.email=fixture@example.invalid",
                "-c",
                "commit.gpgsign=false",
                "-c",
                "tag.gpgsign=false",
                "-c",
                "init.defaultBranch=master",
                *args,
            ],
            check=True,
            capture_output=True,
            text=True,
            cwd=repository,
        ).stdout.strip()

    def commit(version: str) -> str:
        (repository / "VERSION").write_text(f"{version}\n")
        git("add", ".")
        git("commit", "--quiet", "--message", version)
        return git("rev-parse", "HEAD")

    git("init", "--quiet")
    (repository / "autogen.sh").write_text(_AUTOGEN)
    commits = {"tag": commit("tagged")}
    git("tag", "--annotate", "--message", "release", "9.9")
    git("branch", "feature/x")
    commits["master"] = commit("master")
    git("switch", "--quiet", "feature/x")
    commits["branch"] = commit("branch")
    git("switch", "--quiet", "master")
    git("config", "uploadpack.allowAnySHA1InWant", "true")
    monkeypatch.setattr(tmux_build, "_REPOSITORY", f"file://{repository}")
    return commits


def test_every_kind_of_ref_resolves_to_its_commit(fixture: dict[str, str]) -> None:
    """An annotated tag resolves to the commit it tags, not the tag object."""
    for ref, commit in {
        "9.9": fixture["tag"],
        "master": fixture["master"],
        "feature/x": fixture["branch"],
        fixture["branch"]: fixture["branch"],
    }.items():
        assert tmux_build._resolve(ref) == commit


def test_unknown_ref_is_refused(fixture: dict[str, str]) -> None:
    """A name that no tag or branch carries must not become a cache key."""
    with pytest.raises(ValueError, match="no tag or branch named 9.8"):
        tmux_build._resolve("9.8")


@pytest.mark.parametrize("ref", ["9.9", "master", "feature/x", "branch-commit"])
def test_development_build_installs_the_commit_a_ref_names(
    tmp_path: pathlib.Path, fixture: dict[str, str], ref: str
) -> None:
    """Fetch the commit the identity resolved, and build exactly that."""
    if ref == "branch-commit":
        ref = fixture["branch"]
    commit = tmux_build._resolve(ref)
    prefix = tmp_path / "prefix"
    tmux_build._build(prefix, {"version": ref, "source": commit})
    reported = subprocess.run(
        [str(prefix / "bin" / "tmux")], check=True, capture_output=True, text=True
    ).stdout.strip()
    names = {
        fixture["tag"]: "tagged",
        fixture["master"]: "master",
        fixture["branch"]: "branch",
    }
    assert reported == f"tmux {names[commit]}"


def test_directory_names_cannot_collide_or_leave_the_runner_directory() -> None:
    """A branch with a slash gets one flat, distinct directory."""
    assert tmux_build._name("3.8") == "tmux-3.8"
    names = {tmux_build._name(ref) for ref in ("a/b", "a_b", "master")}
    assert len(names) == 3
    assert all("/" not in name for name in names)
