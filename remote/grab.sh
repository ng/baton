#!/usr/bin/env bash
#
# grab — pull a file from your Mac to this remote workspace
#
# Requires: shuttle running on Mac with reverse tunnel (port 19222)
#
# Usage:
#   grab /Users/you/Desktop/file.html              → /workspaces/.inbox/file.html
#   grab /Users/you/Desktop/file.html ./local-dir/  → ./local-dir/file.html

set -euo pipefail

SHUTTLE_PORT="${SHUTTLE_PORT:-19222}"
SHUTTLE_MAC_USER="${SHUTTLE_MAC_USER:-}"
SHUTTLE_INBOX="${SHUTTLE_INBOX:-/workspaces/.inbox}"

grab() {
    local mac_path="${1:?usage: grab <mac-path> [dest]}"
    local filename
    filename="$(basename "$mac_path")"
    local dest="${2:-$SHUTTLE_INBOX}"

    if [[ "$dest" == */ ]]; then
        dest="${dest}${filename}"
    elif [[ -d "$dest" ]]; then
        dest="${dest}/${filename}"
    fi

    mkdir -p "$(dirname "$dest")"

    local mac_user="$SHUTTLE_MAC_USER"
    if [[ -z "$mac_user" ]]; then
        mac_user="$(echo "$mac_path" | sed -n 's|^/Users/\([^/]*\)/.*|\1|p')"
    fi
    if [[ -z "$mac_user" ]]; then
        echo "error: cannot determine Mac username from path. Set SHUTTLE_MAC_USER." >&2
        return 1
    fi

    echo -n "grabbing ${filename}..." >&2
    if scp -P "$SHUTTLE_PORT" -o StrictHostKeyChecking=no -o LogLevel=ERROR \
        "${mac_user}@localhost:${mac_path}" "$dest" 2>/dev/null; then
        echo " done" >&2
        echo "$dest"
    else
        echo " failed" >&2
        echo "error: could not fetch file. Is shuttle running on your Mac?" >&2
        return 1
    fi
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    grab "$@"
fi
