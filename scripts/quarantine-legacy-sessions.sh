#!/usr/bin/env sh
set -eu

PIGO_HOME="${PIGO_HOME:-$HOME/.pigo}"
LEGACY_ROOT="$PIGO_HOME/legacy-sessions"
mkdir -p "$LEGACY_ROOT"

move_if_exists() {
    src="$1"
    name="$2"
    dest="$LEGACY_ROOT/$name"
    if [ -e "$src" ]; then
        if [ -e "$dest" ]; then
            echo "skip $src -> $dest (already exists)"
        else
            mv "$src" "$dest"
            echo "moved $src -> $dest"
        fi
    fi
}

move_if_exists "$PIGO_HOME/sessions" "sessions"
if [ -d "$PIGO_HOME/projects" ]; then
    for project in "$PIGO_HOME"/projects/*; do
        [ -d "$project" ] || continue
        move_if_exists "$project/sessions" "projects-$(basename "$project")-sessions"
    done
fi
