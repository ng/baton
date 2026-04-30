# shuttle.plugin.zsh — auto-detect Mac paths pasted into remote terminal
#
# When you drag a file from Mac Finder into a terminal, the local path
# (e.g., /Users/you/Desktop/file.html) gets pasted. This widget intercepts
# that paste, uploads the file via the reverse SSH tunnel, and replaces
# the path with the remote location.
#
# Source this in your .zshrc:
#   source /path/to/shuttle.plugin.zsh

SHUTTLE_PORT="${SHUTTLE_PORT:-19222}"
SHUTTLE_MAC_USER="${SHUTTLE_MAC_USER:-}"
SHUTTLE_INBOX="${SHUTTLE_INBOX:-/workspaces/.inbox}"

_shuttle_grab_file() {
    local mac_path="$1"
    local filename="${mac_path:t}"
    local dest="${SHUTTLE_INBOX}/${filename}"

    mkdir -p "$SHUTTLE_INBOX" 2>/dev/null

    local mac_user="$SHUTTLE_MAC_USER"
    if [[ -z "$mac_user" ]]; then
        mac_user="${${mac_path#/Users/}%%/*}"
    fi
    if [[ -z "$mac_user" ]]; then
        return 1
    fi

    scp -P "$SHUTTLE_PORT" -o StrictHostKeyChecking=no -o LogLevel=ERROR \
        "${mac_user}@localhost:${mac_path}" "$dest" 2>/dev/null
    if [[ $? -eq 0 ]]; then
        echo "$dest"
        return 0
    fi
    return 1
}

_shuttle_bracketed_paste() {
    local pasted
    zle .bracketed-paste pasted

    # Check if the pasted text looks like a Mac file path
    if [[ "$pasted" =~ ^/Users/[^/]+/.+ ]]; then
        # Single path, no spaces or newlines (simple case)
        if [[ "$pasted" != *$'\n'* ]]; then
            zle -M "shuttle: uploading ${pasted:t}..."
            local remote_path
            remote_path="$(_shuttle_grab_file "$pasted")"
            if [[ $? -eq 0 && -n "$remote_path" ]]; then
                LBUFFER+="$remote_path"
                zle -M "shuttle: ${pasted:t} → ${remote_path}"
                return
            else
                zle -M "shuttle: upload failed, pasting local path"
            fi
        fi
    fi

    # Default: paste as-is
    LBUFFER+="$pasted"
}

zle -N bracketed-paste _shuttle_bracketed_paste
