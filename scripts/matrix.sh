#!/usr/bin/env bash
#
# Run each module's compatible tests against the tmux releases it supports.
#
# The ordinary gate runs against whichever tmux is on PATH, which is one
# version, and version-specific breakage is not hypothetical: tmux 3.4 stopped
# accepting split-window's -p flag that 3.3a and 3.5 both take, and
# pane_start_path does not exist before 3.3. A single-version gate cannot see
# either.
#
# It is separate from the ordinary gate because it is slow -- five modules
# against nine tmux builds -- and because it needs a matrix of tmux builds a
# checkout does not come with. Point LIBTMUX_TMUX_MATRIX at a directory holding
# <version>/bin/tmux, or let it look where the matrix is usually built. Narrow
# what runs with LIBTMUX_MATRIX_MODULES and LIBTMUX_MATRIX_VERSIONS. Set
# LIBTMUX_MATRIX_REQUIRED=1 when absence must fail rather than skip.

set -uo pipefail

script_directory=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repository_root=$(cd "$script_directory/.." && pwd -P)

matrix=${LIBTMUX_TMUX_MATRIX:-$HOME/.local/share/libtmux-tmux-matrix}
required=${LIBTMUX_MATRIX_REQUIRED:-0}
case $required in
    0 | 1) ;;
    *)
        echo "matrix: LIBTMUX_MATRIX_REQUIRED must be 0 or 1, got $required" >&2
        exit 1
        ;;
esac
if [[ ! -d $matrix ]]; then
    if [[ $required == 1 ]]; then
        echo "matrix: required matrix directory does not exist: $matrix" >&2
        exit 1
    fi
    cat >&2 <<MESSAGE
matrix: no tmux matrix at $matrix

Set LIBTMUX_TMUX_MATRIX to a directory holding one subdirectory per version,
each with bin/tmux inside it:

    \$LIBTMUX_TMUX_MATRIX/3.2a/bin/tmux
    \$LIBTMUX_TMUX_MATRIX/3.7c/bin/tmux

Skipping rather than failing: a checkout without a matrix can still run every
other gate, and reporting a pass this did not earn would be worse than saying
it did not run.
MESSAGE
    exit 0
fi

# A tmux namespace of this repository's own. Sibling checkouts sweep their own
# matrices on the same machine, and a shared socket directory is one sweep
# ending servers another is still using.
export TMUX_TMPDIR=${TMUX_TMPDIR:-/tmp/libtmux-go-matrix}
mkdir -p "$TMUX_TMPDIR"
unset TMUX TMUX_PANE

# The workspace stays on. This sweep asks what each tmux release does to the
# code in this tree, and mcp carries no replace directive, so GOWORK=off would
# aim every one of its runs at the core release its require names instead.
# Resolving without a workspace is the standalone module-metadata test's
# question, and it does not vary by tmux version.

all_modules=(. examples workspace mcp benchmarks)
modules=()
if [[ ${LIBTMUX_MATRIX_MODULES+x} ]]; then
    read -r -a modules <<< "$LIBTMUX_MATRIX_MODULES"
else
    modules=("${all_modules[@]}")
fi
if (( ${#modules[@]} == 0 )); then
    echo "matrix: no modules selected" >&2
    exit 1
fi
for module in "${modules[@]}"; do
    case $module in
        . | examples | workspace | mcp | benchmarks) ;;
        *)
            echo "matrix: unknown module \"$module\"" >&2
            exit 1
            ;;
    esac
    if [[ ! -f "$repository_root/$module/go.mod" ]]; then
        echo "matrix: module \"$module\" has no go.mod" >&2
        exit 1
    fi
done

supported_versions=(3.2a 3.3a 3.4 3.5 3.6 3.7 3.7a 3.7b 3.7c)
versions=()
if [[ -n ${LIBTMUX_MATRIX_VERSIONS:-} ]]; then
    read -r -a versions <<< "$LIBTMUX_MATRIX_VERSIONS"
elif [[ $required == 1 ]]; then
    versions=("${supported_versions[@]}")
else
    for candidate in "$matrix"/*/; do
        version=$(basename "$candidate")
        if [[ -x "$candidate/bin/tmux" ]]; then
            versions+=("$version")
        fi
    done
fi
if (( ${#versions[@]} == 0 )); then
    echo "matrix: $matrix holds no version with bin/tmux in it" >&2
    exit 1
fi
for version in "${versions[@]}"; do
    tmux_binary="$matrix/$version/bin/tmux"
    if [[ ! -x $tmux_binary ]]; then
        echo "matrix: $tmux_binary is not executable" >&2
        exit 1
    fi
    if ! reported=$("$tmux_binary" -V 2>&1); then
        echo "matrix: $tmux_binary could not report its version: $reported" >&2
        exit 1
    fi
    expected="tmux $version"
    if [[ $reported != "$expected" ]]; then
        printf 'matrix: %s: expected "%s", got "%s"\n' \
            "$tmux_binary" "$expected" "$reported" >&2
        exit 1
    fi
done

log=$(mktemp)
trap 'rm -f "$log"' EXIT

failed=()
selected_cells=$(( ${#versions[@]} * ${#modules[@]} ))
run_cells=0
for version in "${versions[@]}"; do
    printf 'matrix: %s (tmux %s)\n' "$version" "$version"

    for module in "${modules[@]}"; do
        directory="$repository_root/$module"
        run_cells=$((run_cells + 1))
        if (cd "$directory" && PATH="$matrix/$version/bin:$PATH" go test -count=1 ./... > "$log" 2>&1); then
            printf '  %-14s ok\n' "$module"
        else
            printf '  %-14s FAILED\n' "$module"
            grep -E '^(---|\s+[a-z_]+_test)' "$log" | head -8
            failed+=("$version:$module")
        fi
    done
done

echo
if (( run_cells != selected_cells )); then
    printf 'matrix: ran %d of %d selected cells\n' "$run_cells" "$selected_cells" >&2
    exit 1
fi
if (( ${#failed[@]} == 0 )); then
    printf 'matrix: %d cells across %d tmux versions, all green\n' \
        "$run_cells" "${#versions[@]}"
    exit 0
fi
printf 'matrix: %d of %d cells failed: %s\n' \
    "${#failed[@]}" "$selected_cells" "${failed[*]}"
exit 1
