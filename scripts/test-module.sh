#!/usr/bin/env bash
# Run one module's tests, qualifying fixed-output examples by tmux release.
set -euo pipefail

script_directory=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repository_root=$(cd "$script_directory/.." && pwd -P)
module=${1:?usage: test-module.sh MODULE [go test flags]}
shift
case $module in
    . | examples | workspace | mcp | benchmarks | internal/tools) ;;
    *) echo "test-module: unknown module: $module" >&2; exit 1 ;;
esac

tmux_binary=${LIBTMUX_TMUX:-tmux}
reported=$("$tmux_binary" -V)
version=${reported#tmux }
skip=''
case $version in
    3.3a | 3.4 | 3.5 | 3.6)
        case $module in
            .)
                skip='^Example(Server_OpenControl|ControlClient_.*|Session_OpenControl|Pane_OpenObservation|Server_OpenNotifications|Session_OpenNotifications|Pane_Writer|PaneObservation_.*|NotificationStream.*|Running_StreamTo)$'
                ;;
            workspace) skip='^ExampleBuildInto$' ;;
            mcp) skip='^Example_watchingAVisibleCommandAcrossTurns$' ;;
        esac
        ;;
esac
if [[ $version == 3.3a && $module == . ]]; then
    skip="$skip|^ExamplePane_CaptureToFile$"
fi

arguments=(test)
if [[ -n $skip ]]; then
    printf 'test-module: tmux %s; feature examples omitted: %s\n' "$version" "$skip"
    arguments+=(-skip "$skip")
fi
cd "$repository_root/$module"
exec go "${arguments[@]}" "$@" ./...
