#!/usr/bin/env bash
# Example PreToolUse hook for pigo: block dangerous bash commands before they
# run in the TUI, headless, and ACP modes. Copy it to ~/.pigo/hooks/ (global)
# or ./.pigo/hooks/ (project, trusted only) and wire it in config.json:
#
#   {
#     "hooks": {
#       "PreToolUse": [
#         { "matcher": "bash", "hooks": [{ "type": "command", "command": "./.pigo/hooks/block-dangerous-commands.sh" }] }
#       ]
#     }
#   }
#
# The hook reads the tool call JSON from stdin and exits 2 to block, with the
# reason written to stderr. pigo does not ship this as a forced default; adjust
# the rules to your own threat model.
set -euo pipefail

payload=$(cat)
command_text=$(printf '%s' "$payload" | sed -n 's/.*"command"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')

if printf '%s' "$command_text" | grep -Eq '(^|[[:space:]]|;|&&|\|)rm([[:space:]]+-[a-zA-Z]*[rf][a-zA-Z]*|$)' ||
   printf '%s' "$command_text" | grep -Eq '(^|[[:space:]]|;|&&|\|)(dd|mkfs|shutdown|reboot)([[:space:]]|$)'; then
  echo "blocked: dangerous command is not allowed by project policy" >&2
  exit 2
fi

exit 0
