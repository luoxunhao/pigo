# PRD: /status Slash Command

## Introduction

Add a new `/status` slash command to the pigo interactive REPL that prints a single,
colored, at-a-glance status report combining **telemetry data** and **key configuration**.

Today, runtime facts about a pigo session are scattered: `/model` shows the active
model, `/session` shows message/token/compaction counts, and structured telemetry
(`TelemetryEvent`: per-tool durations, turn count, truncation/compaction counts,
context utilization) is emitted once at each run's end into the stream but **never
retained**—so a user cannot inspect it after the fact. `/status` consolidates the
run-time model configuration, context usage, project/environment, credentials, and
telemetry into one readable report, surfacing both a **cumulative-since-session-start**
view and the **last run's** snapshot.

It is REPL-only (colored text table). It coexists with `/session` and `/model`; it
does not replace them.

## Goals

- Let a user see, in one command, the active model/provider/protocol/base URL/thinking
  level/context window without running several commands.
- Surface retained telemetry (per-tool call counts & durations, turns, truncation and
  compaction counts, context utilization) both cumulatively since session start and for
  the most recent run.
- Retain telemetry across runs in REPL state (currently discarded after each run's emit).
- Show project/environment context: cwd, trust status, counts (and names) of loaded
  skills and plugins.
- Show credential presence (masked, never plaintext) and the configured provider
  endpoint.
- Degrade gracefully on a fresh session where no run has completed yet.

## User Stories

### US-001: Retain telemetry across runs on REPL state
**Description:** As a developer, I need the per-run `TelemetryEvent` summary to be
captured and accumulated on REPL state after each run, so that `/status` can report
both the last run and a cumulative total since session start.

**Acceptance Criteria:**
- [ ] A retained telemetry holder (e.g. on `replDeps` or `liveRunConfig`) stores the
      most recent run's `TelemetryEvent` summary after the run ends.
- [ ] The holder also maintains a cumulative accumulator (total turns, per-tool
      `ToolTiming` summed by name, truncation count, compaction count, latest context
      tokens/window) folded from each run's summary.
- [ ] After draining two synthetic `TelemetryEvent`s in a test, the cumulative fields
      equal the sum of the two (turns add, tool counts/durations add, compaction/truncation add).
- [ ] The holder is reset to "no telemetry yet" on session switches that match the
      `lastBtw` reset semantics (`/fork`, `/clone`, `/import`), so cumulative stats do
      not bleed across conversations.
- [ ] `go build ./...` passes.
- [ ] `go test ./...` passes, including a new unit test for the retention/accumulation logic.

### US-002: Register `/status` and render runtime config + context/compaction sections
**Description:** As a user, I want `/status` to exist and show my active model
configuration and current context usage so I can verify my session is configured and
sized as expected.

**Acceptance Criteria:**
- [ ] Typing `/status` (exact match) in the REPL prints a multi-section colored report
      and returns to the prompt without invoking the model.
- [ ] A "runtime config" section shows: model id, provider name, base URL, protocol,
      thinking level, and context window (tokens).
- [ ] A "context" section shows: current context tokens, context window, utilization
      ratio (e.g. `42%`), compaction count, and remaining tokens before the auto-compact
      threshold (window − reserve).
- [ ] `/status` is listed in `/help` output with a one-line description.
- [ ] `/statusfoo` is NOT matched as `/status` (exact-or-space-prefix guard, matching
      the `/btw` precedent).
- [ ] `go build ./...` passes.
- [ ] `go test ./...` passes, including a test that runs `/status` and asserts the
      runtime-config and context section strings appear in the output.

### US-003: Render project/environment + credentials/connectivity sections
**Description:** As a user, I want `/status` to show my project trust state, loaded
skills/plugins, and credential presence so I can confirm what is active and whether my
API key is configured.

**Acceptance Criteria:**
- [ ] A "project & environment" section shows: cwd, trust status (one of
      `trusted` / `untrusted` / `prompt` / `disabled`), the count of loaded skills, and
      the count of loaded plugins, each followed by their names in parentheses when non-empty.
- [ ] A "credentials & connectivity" section shows: API key presence as `set` or
      `not set` for the active provider, the key's last 4 characters masked as
      `••••abcd` when set, and the provider endpoint URL (base URL).
- [ ] The API key is NEVER printed in plaintext in any section or test output.
- [ ] When trust is disabled (manager nil), the section shows `trust: disabled` rather
      than crashing.
- [ ] When no plugins/skills are loaded, the section shows `0` with no names list.
- [ ] `go build ./...` passes.
- [ ] `go test ./...` passes, including a test asserting `set`/`not set` and that no
      full key substring appears in output.

### US-004: Render telemetry section (cumulative + last run) with colored formatting
**Description:** As a user, I want `/status` to show telemetry—both cumulative since
session start and the last run—so I can understand my session's cost and behavior over
time and on the most recent turn.

**Acceptance Criteria:**
- [ ] A "telemetry" section renders two sub-blocks: `since session start` (cumulative)
      and `last run`, each showing: turn count, truncation count, compaction count,
      context utilization, and a per-tool table (tool name, call count, total ms).
- [ ] On a fresh session with no completed run, the telemetry section prints
      `no telemetry yet` for both sub-blocks instead of empty/zero garbage.
- [ ] After one completed run, the `last run` and `since session start` blocks show
      identical numbers; after two runs, cumulative numbers exceed last-run numbers.
- [ ] The report uses `colorize`/`colorEnabled` and the existing ANSI codes
      (`ansiBold`/`ansiDim`/`ansiCyan`/`ansiGreen`/`ansiYellow`/`ansiRed`) for section
      headers and values, and degrades to plain text when color is disabled.
- [ ] Sections are aligned and readable (headers in bold, labels dim, values default)
      consistent with the `/help` aesthetic.
- [ ] `go build ./...` passes.
- [ ] `go test ./...` passes, including a test that injects a retained telemetry holder
      with known values and asserts the rendered tool table and counts.

### US-005: End-to-end REPL behavior and edge cases
**Description:** As a user, I want `/status` to behave reliably across session
lifecycles (fresh session, after a run, after a session switch) so the report is always
safe to run.

**Acceptance Criteria:**
- [ ] Running `/status` on a brand-new session before any model turn prints the report
      with config/context/credentials populated and `no telemetry yet` in the telemetry
      section, and does not panic.
- [ ] Running `/status` after a `/model <id>` switch reflects the newly switched model
      and provider in the runtime-config section.
- [ ] Running `/status` after `/fork` or `/clone` shows reset cumulative telemetry
      (`no telemetry yet` or zeros) while config sections remain populated.
- [ ] `/status` is NOT exposed in headless mode (no `--status` flag, no stream-json
      change); headless invocation ignores it as an unknown command path.
- [ ] `/status` completes in under 50ms for a typical session (no disk/network I/O on
      the hot path—passive reporting only).
- [ ] `go build ./...` passes.
- [ ] `go test ./...` passes, including an end-to-style REPL test driving `/status`
      through the loop intercept and asserting section presence.

## Functional Requirements

- FR-1: The system must capture the `TelemetryEvent` emitted at each run's end into a
  retained holder on REPL state.
- FR-2: The system must fold each captured summary into a cumulative accumulator
  (turns, per-tool count/total-ms by tool name, truncation count, compaction count,
  latest context tokens/window) that spans all runs in the current session.
- FR-3: The system must reset the retained telemetry holder on session switches
  (`/fork`, `/clone`, `/import`) consistent with `lastBtw` reset semantics.
- FR-4: The system must register a `/status` slash command, intercepted in the REPL
  loop (like `/session`), that prints a colored multi-section status report and returns
  to the prompt without running the model.
- FR-5: `/status` must display a runtime-config section containing model id, provider
  name, base URL, protocol, thinking level, and context window.
- FR-6: `/status` must display a context section containing current context tokens,
  context window, utilization ratio, compaction count, and remaining tokens before the
  auto-compact threshold.
- FR-7: `/status` must display a project & environment section containing cwd, trust
  status, loaded skill count with names, and loaded plugin count with names.
- FR-8: `/status` must display a credentials & connectivity section containing API key
  presence (`set`/`not set`), a masked key hint (last 4 chars) when set, and the
  provider endpoint URL.
- FR-9: `/status` must display a telemetry section with two sub-blocks (`since session
  start` cumulative and `last run`) each showing turn count, truncation count,
  compaction count, context utilization, and a per-tool call-count/total-ms table.
- FR-10: `/status` must print `no telemetry yet` for telemetry sub-blocks when no run
  has completed, and must not panic on a fresh session.
- FR-11: `/status` must be listed in `/help` output with a one-line description.
- FR-12: `/status` must match only on exact `/status` or `/status ` prefix, so
  `/statusfoo` is not treated as `/status`.
- FR-13: `/status` must never print an API key in plaintext in any section.
- FR-14: `/status` must remain REPL-only; it must not add a headless `--status` flag or
  alter the stream-json event protocol.

## Non-Goals (Out of Scope)

- No JSON / machine-readable output (`/status json`)—REPL colored text only.
- No headless support: no `--status` CLI flag and no new stream-json event.
- No modification or deprecation of `/session` or `/model`; they coexist unchanged.
- No persistence of telemetry to disk across pigo restarts (in-memory, reset on exit).
- No active network health-check / ping of the provider endpoint (connectivity is
  reported passively: endpoint URL + key presence). An active probe is out of scope to
  avoid latency and blocking I/O inside `/status`.
- No new external dependencies (Prometheus / OTLP / table-writer libs); reuse existing
  `TelemetryEvent`, `ToolTiming`, `colorize`, and `CredentialStore`.
- No per-tool average/p99 latency breakdowns beyond count and total ms (extensible later).
- No telemetry for `/btw` side threads (they are tracked in their own runs; `/status`
  reflects the main conversation only).

## Design Considerations

- **Command plumbing:** Follow the `/session` precedent exactly—intercept `/status` in
  `runREPL`'s loop via a `runStatus(out, &deps)` helper, and register a stub
  `SlashCommand` in `registerLiveCommands` only so `/help` lists it. This gives full
  `replDeps` access (trust, `agentCtx`, creds, slash registry, retained telemetry
  holder) without widening `registerLiveCommands`'s signature.
- **Output style:** Reuse `colorize`/`colorEnabled` and the existing ANSI codes from
  `cmd/pigo/color.go`; match the `/help` aesthetic (bold section headers, dim labels,
  default values). Render as aligned plain-text sections, not an external table library.
- **Data reuse:** Reuse `agentcore.TelemetryEvent` / `ToolTiming` as the retained data
  types; add only a small holder struct (last snapshot + cumulative fields), not new
  event types.
- **Credential masking:** Use `CredentialStore.HasCredential(ctx, provider)` for
  presence and `GetAPIKey` only to derive a masked `••••<last4>` hint; never print the
  full key. Ensure test fixtures use throwaway keys and assert no full-key substring
  leaks.
- **Section ordering (top to bottom):** runtime config → context → project &
  environment → credentials & connectivity → telemetry. Telemetry last since it is the
  richest and benefits from the sections above for interpretation.

## Technical Considerations

- **Telemetry retention gap:** Today `internal/runtime/loop.go` creates a per-run
  `telemetry` accumulator and emits `tel.summary()` once at run end (loop.go:150), then
  discards it. The REPL's drain path (`DrainStream`/`OnEvent`) consumes events; a hook
  there must capture `TelemetryEvent` into the retained holder. Implementation must
  decide whether to fold at the drain site (REPL-side) or expose the accumulator—REPL
  drain-side folding is preferred (keeps `runtime` package focused, no new API).
- **Mid-run vs end-of-run values:** `/status` is typically run between turns. "Current
  context tokens" and utilization should reflect the latest known values, which the last
  `TelemetryEvent` carries (`ContextTokens`/`ContextWindow`); do not recompute.
- **State availability in `replDeps`:** trust (`deps.trust`), `agentCtx` (message
  count), `creds`, `slash` registry (for skills/plugins counts via `List()`), and `cwd`
  are all already on `replDeps`. Skills/plugins counts may need filtering on command
  kind—verify the registry exposes source/kind metadata, or fall back to counting
  built-in vs user vs plugin commands.
- **Concurrency:** `liveRunConfig` carries no lock by design (single main goroutine).
  The retained telemetry holder is mutated on the drain path and read by `/status` on
  the same main goroutine—keep it lock-free and consistent with the existing model, or
  add a mutex only if the drain runs on a separate goroutine (verify `DrainStream`'s
  invocation thread).
- **Reset points:** Mirror `lastBtw` reset semantics (repl.go `lastBtw` comment) so
  `/fork`/`/clone`/`/import` clear cumulative telemetry; `/resume`/`--continue` start
  fresh since retained state is in-memory only.

## Success Metrics

- `/status` renders all five sections in under 50ms with no disk/network I/O.
- After one completed run, all telemetry fields are populated (non-zero where activity
  occurred); on a fresh session, telemetry degrades to `no telemetry yet` without errors.
- Cumulative telemetry correctly exceeds last-run telemetry after two or more runs.
- No regression in existing `/session`, `/model`, `/help`, or stream-json behavior
  (`go test ./...` green; existing telemetry tests unchanged).
- API key never appears in plaintext in any `/status` output or test artifact.

## Open Questions

- Should cumulative telemetry reset on `/resume` / `--continue` (resumed sessions), or
  only on in-REPL session switches? Default: reset on all session switches (in-memory
  state, fresh start). Confirm during implementation.
- Should the per-tool table show average ms (`total/count`) in addition to count and
  total? Cheap to add; default: include count + total only, leave average as a future
  extension unless trivial.
- For "loaded skills/plugins names", what is the right cap before truncating (e.g., show
  first N then `+k more`)? Default: show all if ≤ 8, else first 8 + `+k more`.
- Trust status taxonomy: confirm the exact states the `trust.Manager` exposes
  (`trusted`/`untrusted`/`prompt`/`disabled`) versus what `/trust` reports today, so
  `/status` reuses the same labels.
