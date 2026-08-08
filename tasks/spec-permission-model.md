# SPEC: pigo 三级权限模型（Permission Model）

> Technical specification derived from: tasks/prd-pigo-permission-model.md
> Generated: 2026-08-07

## Problem Statement

As a pigo user, I can only express trust as "directory trusted or not" plus a
one-shot `--approve` switch. There is no way to say "ask me for every risky
action", "approve file edits on my behalf but still ask before commands", or
"give this run full access", and no way to switch that intent while the process
is running. ACP clients such as pi-web or Zed cannot see or change the current
permission posture through the protocol, and headless mode silently ignores the
confirmation gate that interactive sessions have.

## Solution

pigo gains a process-global permission mode with three levels:

- `ask`（请求批准）: `bash` / `write` / `edit` / `webfetch` / `websearch` all
  request approval.
- `auto_approve_edits`（替我审批）: `write` / `edit` run automatically, while
  `bash` / `webfetch` / `websearch` request approval.
- `full_access`（完全访问权限）: all gated tools run without asking.

The initial mode comes from CLI flag, then config file, then the existing
trust-based default. The mode can be switched at runtime from the TUI, the
REPL, or any ACP client through `session/set_config_option`, and the change is
broadcast to every open session. Runtime switches never write back to
`config.toml`. When no mode has been explicitly set, pigo keeps today's
directory-trust behavior exactly: first-run trust prompt, `/trust`, and
per-call confirmation for `bash` / `write` / `edit`.

## User Stories

1. As a pigo user, I want to choose `ask` so that every side-effect and network
   tool call asks me before running.
2. As a pigo user, I want to choose `auto_approve_edits` so that file writes
   proceed without interrupting me while shell commands still ask.
3. As a pigo user, I want to choose `full_access` so that nothing asks during
   this process.
4. As a pigo user, I want to see the current permission level so that I know
   how much the agent can do on its own.
5. As a pigo user, I want to set the level at startup with
   `--permission-mode ask|auto_approve_edits|full_access` so that one-off runs
   do not require editing config.
6. As a pigo user, I want to set a default level in `config.toml` so that every
   future launch starts with my preferred posture.
7. As an existing pigo user, I want `--approve` and `approve = true` to keep
   working as `full_access` so that my scripts and config are not broken.
8. As a pigo user, I want an invalid mode value to fail fast with exit code 2
   so that a typo cannot silently leave me in the wrong posture.
9. As a TUI user, I want `/permission` to show and switch the level so that I
   can change my mind without restarting.
10. As a REPL user, I want `/permission` to work identically so that line mode
    is not a second-class frontend.
11. As an ACP client user, I want to read the current level from
    `session/set_config_option` so that pi-web can render it.
12. As an ACP client user, I want to switch the level through
    `session/set_config_option` so that clients do not need a private protocol.
13. As an ACP client user, I want every other open session to receive
    `config_options_update` after a switch so that no client shows a stale
    posture.
14. As a TUI user, I want the status bar to show the current level and update
    immediately after a switch so that the posture is always visible.
15. As a script user, I want headless mode to respect an explicit `ask` or
    `auto_approve_edits` level by blocking calls that would need approval so
    that my script cannot silently exceed the declared posture.
16. As a script user, I want headless mode with no explicit level to keep
    today's behavior so that existing scripts do not regress.
17. As a pigo user in explicit `ask` mode, I want `allow_always` to apply only
    to the tool I approved so that approving `webfetch` never also approves
    `bash`.
18. As a pigo user in explicit `ask` mode, I want `reject_always` to apply only
    to the tool I rejected so that I can block one tool without affecting
    others.
19. As a pigo user without an explicit level, I want the four ACP options to
    keep today's directory-level semantics so that trust behavior is stable.
20. As a pigo user, I want switching levels to clear session tool-level
    allow/reject memory so that the new level is applied cleanly.
21. As a pigo user, I want a pending permission request to be re-evaluated when
    I switch levels so that switching to `full_access` does not cancel the
    action I just authorized.
22. As a pigo user, I want the first-run trust prompt skipped once a level is
    explicit so that I am not asked the same question twice.
23. As a pigo user, I want directory trust to remain the behavior when no level
    is explicit so that `/trust` and `trust.json` keep their meaning.
24. As a pigo user, I want `allowed_tools` / `disallowed_tools` to remain hard
    boundaries under every level so that a mode cannot widen the tool set.
25. As a pigo user, I want PreToolUse hooks to keep running before permission
    requests so that hook denials still short-circuit the gate.
26. As a pigo user, I want `write` / `edit` to stay inside the existing tool
    root bounds under `auto_approve_edits` so that workspace escape remains
    impossible.
27. As a maintainer, I want the permission matrix covered by tests so that each
    mode/tool combination is pinned.
28. As a maintainer, I want docs updated so that README, config examples, and
    the capability matrix no longer describe the old binary trust model.
29. As a developer, I want a real-client end-to-end check through pi-web so
    that the ACP wire behavior is verified outside unit tests.

## Implementation Decisions

- Introduce a permission level value with three internal IDs
  (`ask`, `auto_approve_edits`, `full_access`) plus an unset sentinel. The
  unset sentinel means "use the existing trust layer". Display names are
  请求批准 / 替我审批 / 完全访问权限.
- Startup precedence is CLI flag > `config.toml` > unset. `--approve` and
  `approve = true` are aliases for `full_access`; an explicit permission mode
  wins over the alias. Unknown values are a usage error with exit code 2.
- The current level is process-global state owned by the ACP server layer.
  TUI and REPL act as ACP clients and switch through
  `session/set_config_option`, so all frontends share one code path. Headless
  mode reads startup config directly because it is a separate process and has
  no ACP server.
- The permission gate keeps its existing `BeforeToolCall` seam. The decision
  rule is:
  - explicit `full_access`: allow.
  - explicit `auto_approve_edits` + `write` / `edit`: allow.
  - explicit `ask` or `auto_approve_edits` + `bash` / `webfetch` / `websearch`:
    issue a permission request.
  - unset: delegate to the trust manager with today's `bash` / `write` /
    `edit` gating and directory semantics.
- The gated tool set is level-aware. Explicit `ask` / `auto_approve_edits`
  gate `bash` / `write` / `edit` / `webfetch` / `websearch`. The unset path
  keeps today's three-tool set, so adding network confirmation is an explicit
  mode behavior and never a silent default regression.
- In explicit modes, `allow_always` / `reject_always` become session-scoped
  tool-level memory owned by the permission gate: one decision per tool name,
  consulted only when that tool would otherwise request approval. The unset
  path keeps the existing four-option semantics (`allow_always` = directory
  session trust, `reject_always` = persisted untrusted).
- Switching levels clears the tool-level session memory. The permission gate
  keeps a registry of pending requests (request identity, tool name, response
  channel); on a level switch every pending request is re-evaluated against
  the new level, and requests that no longer need approval are resolved as
  allowed.
- `session/set_config_option` gains a `permission_mode` select option with the
  three levels. `currentValue` is `"default"` while unset; `set_config_option`
  rejects `"default"` and any unknown value. `session/set_mode` continues to
  control thinking level only and is not reused.
- Broadcasting requires enumerating live sessions; the session manager gains a
  snapshot accessor so the dispatcher can send `config_options_update` to
  every session after a switch.
- The TUI status bar gains a permission segment, and the shared slash registry
  gains `/permission` for both TUI and REPL. `/permission` with no argument
  prints the current level and usage.
- Headless wiring: when the startup mode is explicit, install the same
  permission gate with fail-closed behavior (calls that need approval return a
  blocked tool error). When unset, leave the gate uninstalled exactly as today.
- No new persistent store. Tool-level memory is in-memory only; `trust.json`
  schema and `/trust` behavior are unchanged.
- Tool policy filtering and hook execution order are unchanged: registration
  layer still enforces `allowed_tools` / `disallowed_tools`, and PreToolUse
  hooks still run before permission requests.

## Testing Decisions

- A good test asserts external behavior: what the ACP client observes
  (`request_permission` appears or not, `config_options_update` is broadcast,
  response outcome maps to allow/block), and what the permission gate decides
  for a given level, tool, and trust state. Tests should not assert internal
  bookkeeping shapes.
- Proposed test seams, from highest to narrowest:
  - ACP protocol layer: in-process transport exercises `session/new`,
    `session/set_config_option`, `session/request_permission`, and
    `config_options_update` together. This is the primary seam.
  - Headless run layer: direct-run tests assert blocked tool results under
    explicit `ask` / `auto_approve_edits` and unchanged behavior when unset.
  - CLI/config layer: flag and file overlay tests assert precedence, alias
    mapping, and exit code 2 on unknown values.
  - TUI/REPL layer: slash command and status-bar tests assert visible state
    and updates.
- Modules under test: ACP protocol layer, permission gate, CLI config parsing,
  TUI/REPL slash and status bar, headless run wiring, and trust regression.
- Prior art to follow: existing ACP permission tests with a mock transport,
  dispatcher integration tests over the in-process transport, CLI flag overlay
  tests, TUI permission view tests, and tool-policy registration tests.
- The mode/tool matrix is table-driven: each combination of level and gated
  tool asserts allow / request / block. Additional cases cover unset plus
  `webfetch` (must not request), mode switch clearing memory, pending request
  re-evaluation, and broadcast to multiple sessions.
- Windows notes: the local machine has no CGO, so `-race` is unavailable; use
  ordered concurrent tests locally and run `-race` in CI. Full `go test ./...`
  has pre-existing Windows failures in unrelated packages; verification uses
  the affected packages plus `go build ./...`.

## Out of Scope

- Per-session or per-directory permission levels; the level is global to the
  process.
- A runtime `default` level that switches back to trust behavior; after the
  first explicit switch, only the three levels exist until restart.
- Persisting tool-level allow/reject decisions across restarts.
- Parameter-level rules such as `Bash(git log:*)` or command risk
  classification.
- Per-domain network rules; `webfetch` / `websearch` are gated as tools only.
- OS-level sandboxing or isolation; the process-level gate and tool root
  bounds remain the boundary.
- Changes to read-only tool gating, `bash_output`, `kill_bash`, hooks
  protocol/order, or `session/set_mode` thinking semantics.
- Changes to MCP / plugin tool registration or `allowed_tools` validation.

## Further Notes

- The PRD records one product assumption: Zed may not expose agent config
  option switching, so the real-client end-to-end verification is anchored on
  pi-web, with Zed verified only if its UI supports it.
- Publishing to the project issue tracker is deferred until the user confirms
  this spec; when published, the issue should carry the `ready-for-agent`
  triage label.
- Implementation order should start with the permission value and startup
  parsing, then the gate decision rule, then ACP config option and broadcast,
  then `/permission` + status bar, then headless wiring, then docs and e2e.
