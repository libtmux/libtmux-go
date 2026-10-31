#!/usr/bin/env bash
#
# Plan the tmux lanes of the tests workflow.
#
#   ci-plan.sh          read .github/ci-matrix.txt, the event and the dispatch
#                       inputs, and write the matrices to $GITHUB_OUTPUT
#   ci-plan.sh verify   read NEEDS_JSON and ALLOW_SKIPPED, and fail unless
#                       every job the plan put in scope succeeded
#
# A push or pull request runs the first and last supported release. The whole
# table, the development lanes and the refused releases run for a tag push and
# for a dispatch with scope=full. A dispatch with scope=custom runs the tmux
# versions, tags, branches or commits it names.
#
# Inputs reach this script only through the environment: INPUT_SCOPE,
# INPUT_TMUX_REF, INPUT_GO_VERSION, INPUT_OS and INPUT_TMATE. A push or pull
# request ignores them. CI_MATRIX names another manifest, for the tests.

set -euo pipefail

script_directory=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repository_root=$(cd "$script_directory/.." && pwd -P)

die() {
    printf '::error::ci-plan: %s\n' "$*" >&2
    exit 1
}

verify() {
    local needs=${NEEDS_JSON:-}
    [[ -n $needs ]] || die "verify needs NEEDS_JSON"
    local failures
    failures=$(jq -r --arg allow "${ALLOW_SKIPPED:-}" '
        ($allow | split(" ") | map(select(. != ""))) as $skippable
        | to_entries
        | if length == 0 then error("no jobs to verify") else . end
        | map(select(
            .value.result != "success"
            and (.value.result != "skipped" or ((.key as $job | $skippable | index($job)) == null))
        ))
        | map("\(.key): \(.value.result)")
        | .[]
    ' <<< "$needs") || die "NEEDS_JSON holds no jobs"
    if [[ -n $failures ]]; then
        printf '::error::required: these jobs did not pass:\n%s\n' "$failures" >&2
        exit 1
    fi
    printf 'required: every planned job passed\n'
}

if [[ ${1:-} == verify ]]; then
    verify
    exit 0
elif [[ $# -gt 0 ]]; then
    die "unknown argument: $1"
fi

manifest=${CI_MATRIX:-$repository_root/.github/ci-matrix.txt}
event=${GITHUB_EVENT_NAME:?GITHUB_EVENT_NAME is not set}

# Characters a tmux version, tag, branch or commit may carry, and the same for
# a Go toolchain. A name that starts with a dash would read as a git option.
valid_ref() { [[ $1 =~ ^[A-Za-z0-9._/-]{1,64}$ && $1 != -* ]]; }
valid_toolchain() { [[ $1 =~ ^[A-Za-z0-9._-]{1,32}$ && $1 != -* ]]; }

trim() {
    local text=$1
    text=${text#"${text%%[![:space:]]*}"}
    text=${text%"${text##*[![:space:]]}"}
    printf '%s' "$text"
}

# Split a comma-separated input into the array named by $1. An empty item is
# an error: it is how a stray comma would otherwise vanish from the plan.
split_list() {
    local -n target=$1
    local text=$2 label=$3 item
    local -a items
    target=()
    [[ -n $(trim "$text") ]] || die "$label is empty"
    mapfile -t items < <(printf '%s\n' "${text//,/$'\n'}")
    for item in "${items[@]}"; do
        item=$(trim "$item")
        [[ -n $item ]] || die "$label has an empty item: $text"
        if [[ " ${target[*]-} " != *" $item "* ]]; then
            target+=("$item")
        fi
    done
}

contains() {
    local needle=$1
    shift
    [[ " $* " == *" $needle "* ]]
}

supported=() refused=() development=() go_versions=() control=''
if [[ ! -f $manifest ]]; then
    die "no manifest at $manifest"
fi
while IFS= read -r line || [[ -n $line ]]; do
    line=${line%%'#'*}
    read -r kind value extra <<< "$line"
    [[ -n $kind ]] || continue
    [[ -n $value && -z $extra ]] || die "malformed manifest line: $line"
    case $kind in
        go)
            valid_toolchain "$value" || die "manifest names an invalid toolchain: $value"
            contains "$value" "${go_versions[@]-}" && die "manifest repeats go $value"
            go_versions+=("$value")
            continue
            ;;
        control)
            valid_ref "$value" || die "manifest names an invalid tmux ref: $value"
            [[ -z $control ]] || die "manifest names two control releases"
            control=$value
            continue
            ;;
        supported | refused | development) ;;
        *) die "manifest has an unknown kind: $kind" ;;
    esac
    valid_ref "$value" || die "manifest names an invalid tmux ref: $value"
    contains "$value" "${supported[@]-}" "${refused[@]-}" "${development[@]-}" &&
        die "manifest repeats tmux $value"
    case $kind in
        supported) supported+=("$value") ;;
        refused) refused+=("$value") ;;
        development) development+=("$value") ;;
    esac
done < "$manifest"
(( ${#supported[@]} > 0 )) || die "manifest lists no supported tmux release"
(( ${#go_versions[@]} > 0 )) || die "manifest lists no Go toolchain"
[[ -n $control ]] || die "manifest names no control release"

# Inputs exist only on a dispatch.
dispatch=0
scope=edges
case $event in
    push)
        if [[ ${GITHUB_REF:-} == refs/tags/* ]]; then scope=full; fi
        ;;
    pull_request) ;;
    workflow_dispatch)
        dispatch=1
        scope=${INPUT_SCOPE:-edges}
        ;;
    *) die "no plan for the $event event" ;;
esac
case $scope in
    edges | full | custom) ;;
    *) die "scope must be edges, full or custom, got: $scope" ;;
esac

os=ubuntu-latest
tmate=off
lane_go=(stable)
tests_go=("${go_versions[@]}")
if (( dispatch )); then
    if [[ -n ${INPUT_OS:-} ]]; then
        case $INPUT_OS in
            ubuntu-latest | ubuntu-24.04 | ubuntu-22.04) os=$INPUT_OS ;;
            *) die "os must be ubuntu-latest, ubuntu-24.04 or ubuntu-22.04, got: $INPUT_OS" ;;
        esac
    fi
    case ${INPUT_TMATE:-off} in
        off | before-tests | on-failure) tmate=${INPUT_TMATE:-off} ;;
        *) die "tmate must be off, before-tests or on-failure, got: $INPUT_TMATE" ;;
    esac
    if [[ -n $(trim "${INPUT_GO_VERSION:-}") ]]; then
        split_list tests_go "$INPUT_GO_VERSION" go-version
        for toolchain in "${tests_go[@]}"; do
            valid_toolchain "$toolchain" || die "go-version has an invalid item: $toolchain"
        done
        lane_go=("${tests_go[@]}")
    fi
fi

refs=() rejected=() requested=()
case $scope in
    edges)
        refs=("${supported[0]}")
        if [[ ${supported[-1]} != "${supported[0]}" ]]; then refs+=("${supported[-1]}"); fi
        ;;
    full)
        refs=("${supported[@]}" "${development[@]}")
        rejected=("${refused[@]}")
        ;;
    custom)
        split_list requested "${INPUT_TMUX_REF:-}" tmux-ref
        for ref in "${requested[@]}"; do
            valid_ref "$ref" || die "tmux-ref has an invalid item (want ^[A-Za-z0-9._/-]{1,64}\$, no leading dash): $ref"
            if contains "$ref" "${refused[@]-}"; then rejected+=("$ref"); else refs+=("$ref"); fi
        done
        ;;
esac

release_like() { [[ $1 =~ ^[0-9]+\.[0-9]+[a-z]?$ ]]; }

lanes=()
for ref in "${refs[@]-}"; do
    [[ -n $ref ]] || continue
    if contains "$ref" "${development[@]-}"; then
        expect='tmux next-*' advisory=true has_control=true
    elif release_like "$ref"; then
        expect="tmux $ref" advisory=false has_control=false
        # sort -C succeeds when the control release does not follow this one.
        if printf '%s\n%s\n' "$control" "$ref" | sort -V -C; then has_control=true; fi
    else
        expect='tmux *' advisory=false has_control=true
    fi
    label=$ref
    if [[ $ref =~ ^[0-9a-f]{40}$ ]]; then label=${ref:0:12}; fi
    for toolchain in "${lane_go[@]}"; do
        lane_label=$label
        if [[ $toolchain != stable ]]; then lane_label="$label, go $toolchain"; fi
        lanes+=("$(jq -n -c \
            --arg version "$ref" --arg name "$lane_label" --arg go "$toolchain" \
            --arg expect "$expect" --arg tmate "$tmate" \
            --argjson advisory "$advisory" --argjson control "$has_control" \
            '{"tmux-version": $version, name: $name, "go-version": $go, expect: $expect,
              advisory: $advisory, control: $control, tmate: $tmate}')")
    done
done

lane_count=${#lanes[@]}
rejected_count=${#rejected[@]}
if [[ $tmate != off ]] && (( lane_count != 1 || rejected_count != 0 )); then
    die "tmate needs exactly one tmux lane and the plan has $((lane_count + rejected_count)); name one tmux-ref and at most one go-version"
fi

json_array() {
    if (( $# == 0 )); then printf '[]'; else printf '%s\n' "$@" | jq -s -c .; fi
}
strings_array() {
    if (( $# == 0 )); then printf '[]'; else printf '%s\n' "$@" | jq -R . | jq -s -c .; fi
}

tmux_json=$(json_array "${lanes[@]}")
rejected_json=$(strings_array "${rejected[@]}")
go_json=$(strings_array "${tests_go[@]}")
skip=''
if (( lane_count == 0 )); then skip='tmux-matrix'; fi
if (( rejected_count == 0 )); then skip="${skip:+$skip }tmux-rejected"; fi

output=${GITHUB_OUTPUT:-/dev/stdout}
{
    printf 'scope=%s\n' "$scope"
    printf 'tmux=%s\n' "$tmux_json"
    printf 'rejected=%s\n' "$rejected_json"
    printf 'go-versions=%s\n' "$go_json"
    printf 'os=%s\n' "$os"
    printf 'skip=%s\n' "$skip"
    printf 'jobs=%s\n' "$((lane_count + rejected_count))"
} >> "$output"

if [[ -n ${GITHUB_STEP_SUMMARY:-} ]]; then
    {
        printf '### tmux plan\n\n'
        printf -- "- scope: \`%s\`\n" "$scope"
        printf -- "- tmux lanes: \`%s\`\n" "${refs[*]-}"
        printf -- "- refused releases: \`%s\`\n" "${rejected[*]-}"
        printf -- "- Go: \`%s\`\n" "${tests_go[*]}"
        printf -- '- planned tmux jobs: %s\n' "$((lane_count + rejected_count))"
    } >> "$GITHUB_STEP_SUMMARY"
fi
