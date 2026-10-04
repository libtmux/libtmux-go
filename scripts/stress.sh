#!/usr/bin/env bash
#
# Repeat the real-tmux suites and report how often each test fails.
#
# One green run says little about a timing-sensitive test, so this runs the
# suites LIBTMUX_STRESS_REPEAT times and counts failures per test. It writes
# stress-failures.csv (one row per test that failed at least once),
# stress-runs.csv (one row per repetition, with the pseudo-terminal and tmux
# server counts after it), stress-durations.csv (the slowest tests, as the
# longest each took in any repetition), and a Markdown table on stdout. A suite that fails
# to build or crashes is counted under its package name.
#
# Modules are those whose tests drive a real tmux server. Output goes to
# LIBTMUX_STRESS_OUT, default the current directory.

set -uo pipefail

script_directory=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repository_root=$(cd "$script_directory/.." && pwd -P)

repeat=${LIBTMUX_STRESS_REPEAT:-10}
out=${LIBTMUX_STRESS_OUT:-$PWD}
label=${LIBTMUX_STRESS_LABEL:-$(uname -s)}
modules=${LIBTMUX_STRESS_MODULES:-". workspace mcp"}
case $repeat in
    '' | *[!0-9]* | 0)
        echo "stress: LIBTMUX_STRESS_REPEAT must be a positive integer, got $repeat" >&2
        exit 1
        ;;
esac
mkdir -p "$out"
: "${TMUX_TMPDIR:=/tmp/libtmux-go-stress}"
export TMUX_TMPDIR
mkdir -p "$TMUX_TMPDIR"

events="$out/stress-events.jsonl"
runs="$out/stress-runs.csv"
failures="$out/stress-failures.csv"
durations="$out/stress-durations.csv"
: > "$events"
echo 'label,repetition,seconds,ptys_in_use,peak_ptys,peak_tmux_servers,tmux_servers,ptmx_max' > "$runs"

# shellcheck disable=SC2009
ptys_in_use() {
    ps -axo tty= | grep -v '^?*$' | grep -v '^-*$' | sort -u | wc -l | tr -d ' '
}

# Peak pseudo-terminals and tmux servers alive at once, sampled while the
# suites run: the limit that matters on macOS is the peak, not the end.
peak_file="$out/stress-peak.txt"
sample_peak() {
    local ptys=0 servers=0 now_ptys now_servers
    while :; do
        now_ptys=$(ptys_in_use)
        now_servers=$(pgrep -x tmux | wc -l | tr -d ' ')
        ((now_ptys > ptys)) && ptys=$now_ptys
        ((now_servers > servers)) && servers=$now_servers
        echo "$ptys $servers" > "$peak_file"
        sleep 2
    done
}

for ((repetition = 1; repetition <= repeat; repetition++)); do
    started=$SECONDS
    sample_peak &
    sampler=$!
    for module in $modules; do
        (cd "$repository_root/$module" && go test -json -count=1 ./... 2>&1) |
            jq -R -c --arg module "$module" --argjson rep "$repetition" \
                'fromjson? // {Action: "output", Output: .} | . + {module: $module, rep: $rep}' \
                >> "$events"
    done
    kill "$sampler" 2>/dev/null
    wait "$sampler" 2>/dev/null
    read -r peak_ptys peak_servers < "$peak_file"
    ptmx_max=$(sysctl -n kern.tty.ptmx_max 2>/dev/null || cat /proc/sys/kernel/pty/max 2>/dev/null || echo)
    echo "$label,$repetition,$((SECONDS - started)),$(ptys_in_use),$peak_ptys,$peak_servers,$(pgrep -x tmux | wc -l | tr -d ' '),$ptmx_max" >> "$runs"
done

# A test fails in a repetition when go reports a fail action for it; a package
# with no failing test but a fail action (build failure, crash) is counted as
# the package itself.
jq -r -s --arg label "$label" --argjson repeat "$repeat" '
    [ .[] | select(.Action == "fail") |
      {key: ((.Package // .module) + " " + (.Test // "(package)")), rep: .rep} ] |
    group_by(.key) |
    map({key: .[0].key, failed: (map(.rep) | unique | length)}) |
    sort_by(-.failed, .key) |
    (["label", "test", "failed", "repetitions"] | @csv),
    (.[] | [$label, .key, .failed, $repeat] | @csv)
' "$events" > "$failures"

jq -r -s --arg label "$label" '
    [ .[] | select((.Action == "pass" or .Action == "fail") and .Test != null) |
      {key: (.Package + " " + .Test), elapsed: .Elapsed} ] |
    group_by(.key) |
    map({key: .[0].key, longest: (map(.elapsed) | max)}) |
    sort_by(-.longest) | .[:25] |
    (["label", "test", "longest_seconds"] | @csv),
    (.[] | [$label, .key, .longest] | @csv)
' "$events" > "$durations"

echo "### Real-tmux suites on $label, $repeat repetitions"
echo
if [ "$(wc -l < "$failures")" -le 1 ]; then
    echo "No test failed."
else
    echo '| Test | Failed | Of |'
    echo '| --- | ---: | ---: |'
    tail -n +2 "$failures" | tr -d '"' | awk -F, -v n="$repeat" '{ printf "| %s | %s | %s |\n", $2, $3, n }'
fi
echo
echo '| Repetition | Seconds | Peak ptys | Peak tmux servers | Ptys left | Servers left |'
echo '| ---: | ---: | ---: | ---: | ---: | ---: |'
tail -n +2 "$runs" | awk -F, '{ printf "| %s | %s | %s | %s | %s | %s |\n", $2, $3, $5, $6, $4, $7 }'
echo
echo '| Slowest test | Longest seconds |'
echo '| --- | ---: |'
tail -n +2 "$durations" | tr -d '"' | head -n 10 | awk -F, '{ printf "| %s | %s |\n", $2, $3 }'
