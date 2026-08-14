# PRD: OpenAI Protocol Variants (Chat Completions vs Response API)

## Introduction

pigo selects a wire protocol for talking to a model endpoint via the `--protocol`
flag / `protocol` config key, today accepting only two flat values: `openai` and
`anthropic`. The `openai` value always means the **Chat Completions** API
(`POST {base_url}/chat/completions`).

OpenAI now offers a second, distinct wire API — the **Responses API**
(`POST {base_url}/responses`) — with a different request/response shape
(input/output items, first-class reasoning, server-managed tool state). pigo has
no support for it today: every provider marked "responses" (e.g.
`azure-openai-responses`) actually still speaks Chat Completions.

This feature extends the `protocol` value to distinguish the two OpenAI wire
APIs and adds a real Responses API driver, implemented with the official
`github.com/openai/openai-go` SDK.

## Goals

- Accept three OpenAI protocol values: `openai`, `openai/chat`, `openai/resp_api`.
- Treat `openai` and `openai/chat` as identical — the existing Chat Completions driver.
- Route `openai/resp_api` to a new Responses API driver built on the official openai-go SDK.
- Reach full feature parity with the Chat Completions driver: streaming, tool/function calls, image inputs, and reasoning/thinking.
- Keep resolution precedence and existing behavior unchanged for every current value (`openai`, `anthropic`, and all provider names).
- Surface the selected variant accurately in the startup banner.

## User Stories

### US-001: Parse the extended protocol value
**Description:** As a developer, I need protocol parsing to accept the new slash-suffixed OpenAI variants so downstream resolution can branch on them.

**Acceptance Criteria:**
- [ ] `--protocol`/`protocol` accepts `openai`, `openai/chat`, `openai/resp_api` (case-insensitive, trimmed)
- [ ] `openai` and `openai/chat` normalize to the same internal Chat Completions selector
- [ ] `openai/resp_api` normalizes to a distinct Responses selector
- [ ] `anthropic` and empty are unchanged
- [ ] An unknown value (e.g. `openai/foo`) returns a clear error naming the accepted values
- [ ] Unit test covers each accepted value + one rejected value
- [ ] Typecheck / `go vet` / lint passes

### US-002: Add the official openai-go dependency
**Description:** As a developer, I need the official OpenAI Go SDK pinned so the Responses driver can use it.

**Acceptance Criteria:**
- [ ] `github.com/openai/openai-go` added to `go.mod` at an exact pinned version
- [ ] `go mod tidy` leaves a clean, buildable module (`go build ./...` green)
- [ ] `go.sum` updated; no other dependency ranges loosened
- [ ] Typecheck / `go vet` passes

### US-003: Responses API driver — non-streaming text
**Description:** As a user, I want `--protocol openai/resp_api` to complete a request via `POST /responses` so the new wire API works end to end.

**Acceptance Criteria:**
- [ ] New driver posts to the Responses endpoint via the openai-go SDK, honoring the resolved `base_url` and API key
- [ ] A plain text prompt returns assistant text mapped into pigo's existing message/response types
- [ ] Base-URL and auth precedence match the Chat Completions driver (`ResolveBaseURL`, provider env vars)
- [ ] Errors (non-2xx, auth failure) surface with the same shape/exit mapping as the chat driver
- [ ] Unit test with a stubbed HTTP transport verifies the request targets `/responses` and the response maps correctly
- [ ] Typecheck / `go vet` / lint passes

### US-004: Responses API driver — streaming
**Description:** As a user, I want streamed output from the Responses API so the TUI/REPL renders tokens incrementally like it does for chat.

**Acceptance Criteria:**
- [ ] Driver consumes the Responses streaming events and emits pigo's existing streaming partial type
- [ ] Final accumulated output equals the non-streamed result for the same prompt
- [ ] Cancellation via context stops the stream promptly
- [ ] Unit test with a stubbed event stream verifies incremental deltas and final aggregation
- [ ] Typecheck / `go vet` / lint passes

### US-005: Responses API driver — tool/function calls
**Description:** As a user, I want tool calls to work over the Responses API so the agent loop functions identically to chat.

**Acceptance Criteria:**
- [ ] pigo tool schemas are sent as Responses-API tools/functions
- [ ] Model-emitted tool calls are parsed into pigo's tool-call type (name + arguments + id)
- [ ] Tool results are fed back in the Responses-API format for the follow-up turn
- [ ] A multi-turn tool-call round-trip completes in a unit/integration test with a stubbed transport
- [ ] Typecheck / `go vet` / lint passes

### US-006: Responses API driver — image inputs & reasoning
**Description:** As a user, I want image inputs and reasoning/thinking to work over the Responses API for full parity with chat.

**Acceptance Criteria:**
- [ ] Image content parts are mapped to Responses-API input items
- [ ] Reasoning/thinking level maps to the Responses-API reasoning field
- [ ] Reasoning output (if returned) is surfaced consistently with the chat driver's handling
- [ ] Unit test covers an image input request and a reasoning-enabled request shape
- [ ] Typecheck / `go vet` / lint passes

### US-007: Wire the variant through resolution
**Description:** As a user, I want `--protocol openai/resp_api` (or the config equivalent) to actually construct the Responses driver.

**Acceptance Criteria:**
- [ ] `ResolveProvider` routes the resp_api selector to the new driver against `base_url`
- [ ] `openai` / `openai/chat` continue to construct the existing Chat Completions driver
- [ ] `--protocol openai/resp_api` with an empty `base_url` behaves per the agreed rule (see Open Questions) — errors or targets the public OpenAI endpoint, whichever is chosen
- [ ] Existing `--provider <name>` behavior is unchanged (out of scope: provider-name selection of resp_api)
- [ ] `resolve_test.go` covers resp_api selection and the unchanged chat/anthropic paths
- [ ] Typecheck / `go vet` / lint passes

### US-008: Banner + config display
**Description:** As a user, I want the startup banner to show which OpenAI wire API is in use so the displayed Protocol matches reality.

**Acceptance Criteria:**
- [ ] Banner Protocol row shows `openai/chat` or `openai/resp_api` when that variant is selected
- [ ] `openai` input still displays meaningfully (normalized label documented)
- [ ] No regression for `anthropic` / provider-name displays
- [ ] Typecheck / `go vet` / lint passes

### US-009: Docs
**Description:** As a user, I want the configuration docs to explain the new protocol values.

**Acceptance Criteria:**
- [ ] `docs/web/configuration.html` protocol references list `openai` / `openai/chat` / `openai/resp_api` with a one-line meaning for each (EN + ZH `data-*`)
- [ ] Sample notes when to pick resp_api vs chat
- [ ] No broken markup

## Functional Requirements

- FR-1: The system must accept `openai`, `openai/chat`, and `openai/resp_api` as `--protocol`/`protocol` values.
- FR-2: The system must treat `openai` and `openai/chat` as the existing Chat Completions protocol.
- FR-3: The system must route `openai/resp_api` to a Responses API driver that posts to `{base_url}/responses`.
- FR-4: The system must reject unknown OpenAI sub-variants with an error naming the accepted values.
- FR-5: The system must implement the Responses driver using the official `github.com/openai/openai-go` SDK pinned to an exact version.
- FR-6: The Responses driver must support streaming output.
- FR-7: The Responses driver must support tool/function calls in the agent loop.
- FR-8: The Responses driver must support image inputs.
- FR-9: The Responses driver must support reasoning/thinking level.
- FR-10: The Responses driver must resolve base URL and API key via the same precedence as the Chat Completions driver.
- FR-11: The startup banner must display the selected OpenAI wire variant.
- FR-12: The configuration docs must document the three OpenAI protocol values.

## Non-Goals (Out of Scope)

- Migrating `azure-openai-responses` (or any existing provider) to the Responses API — it stays on Chat Completions for now.
- Adding a new `--provider` name for the Responses API — selection is only via `--protocol`/`protocol`.
- Making the built-in `openai` provider default to the Responses API — the default OpenAI wire stays Chat Completions unless resp_api is explicitly requested.
- Changing model-name inference, config precedence, or any Anthropic-protocol behavior.
- Rewriting the existing hand-rolled Chat Completions driver to use the SDK.

## Technical Considerations

- Existing drivers are hand-rolled `http` clients (`internal/provider/providers.go`); the SDK-based Responses driver will coexist as a new driver type behind the same `Provider` interface.
- Protocol values currently live as flat constants (`ProtocolOpenAI`, `ProtocolAnthropic` in `registry.go:50`); the slash variants need a normalization/parse step, likely near `ResolveProvider` (`resolve.go:49`) so the switch can branch on chat vs resp_api.
- Base-URL/auth reuse: the SDK client must be pointed at the resolved `base_url` and given the resolved key rather than reading env directly, to preserve `ResolveBaseURL` precedence.
- Banner reads a raw `opts.Protocol` today (`banner.go:64`); showing the normalized variant may require surfacing the normalized value (ties into the separately-noted resolved-protocol gap).
- `go 1.27rc1` — confirm the pinned openai-go version builds on this toolchain.

## Success Metrics

- `pigo --protocol openai/resp_api -u <endpoint> -m <model>` completes a streamed, tool-using turn with parity to `--protocol openai`.
- Zero behavior change for existing `openai` / `anthropic` / provider-name users (existing provider tests stay green).
- Banner Protocol field matches the actual wire API used.

## Open Questions

- For `--protocol openai/resp_api` with **no** `--base-url`: should it target the public OpenAI endpoint (`api.openai.com/v1`) like `--protocol anthropic` targets the public Anthropic API, or require `--base-url` like `--protocol openai` does today (`resolve.go:51-53`)? (Leaning: mirror `openai` and require base-url, or default to the OpenAI public endpoint — needs a decision in US-007.)
- Normalized display label for a bare `openai` input in the banner — show `openai/chat` for clarity, or preserve `openai`?
- Does the target endpoint in your setup (e.g. the Baidu OneAPI gateway) actually implement `/responses`, or is resp_api intended only for genuine OpenAI/Azure endpoints? Affects which base_url combinations to test.
