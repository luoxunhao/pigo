# pigo ACP Context

pigo exposes its agent core through an Agent Client Protocol (ACP) service layer used by the in-process TUI/REPL and external stdio clients. This context covers the protocol surface and the behaviors an ACP client can observe.

## Language

**Agent Client Protocol (ACP)**:
A JSON-RPC 2.0 protocol over stdio used by agent clients such as Zed to drive a coding agent.
_Avoid_: ACP server, wire protocol

**ACP server**:
The pigo side that owns session lifecycle, dispatch, event mapping, and permissions.
_Avoid_: backend, daemon

**ACP client**:
The frontend side, in-process TUI/REPL or external IDE, that sends requests and consumes notifications.
_Avoid_: shell, UI layer

**pi-acp**:
An external reference adapter that bridges pi's RPC mode to ACP; pigo uses its observable behavior as the parity target.
_Avoid_: pi adapter, reference agent

**ACP surface**:
The standard methods, capabilities, and notifications an external client can observe: initialize, session lifecycle, config options, available commands, auth, and session/update events.
_Avoid_: ACP API, protocol feature

**Headless bypass**:
The `-p` / `--output-format` path that drives the agent core directly without ACP; it is a deliberate exception to the single-frontend architecture.
_Avoid_: headless ACP, non-interactive ACP

**Steering message**:
A prompt submitted while a turn is running, held in the session's pending queue and injected as a user message at a tool boundary so the current run continues instead of ending.
_Avoid_: running prompt, queued turn

**Follow-up message**:
A prompt still queued when the agent would naturally stop; injected after the inner loop settles so the run continues instead of ending.
_Avoid_: steering message, queued turn

**Pending queue**:
The session-level FIFO that holds prompts submitted while a turn is active; steering consumes it at tool boundaries and follow-up consumes the remainder at settle points.
_Avoid_: prompt queue, turn queue

**Auto-compaction**:
pigo's default context-window management behavior, enabled by resolved configuration whenever the model context window is known; it is not exposed as an ACP slash command.
_Avoid_: autocompact command, manual compaction

**Context building**:
The pipeline that assembles a provider request's system prompt, session messages, tools, and per-turn dynamic context from persisted session state and project resources.
_Avoid_: context management, context storage, context compaction

**Startup info**:
A plain-text assistant block emitted after `session/new` with pigo version, model/provider, cwd, and capability counts; suppressed by `PIGO_QUIET_STARTUP=true` and never emitted on `session/load`.
_Avoid_: banner, splash, welcome message

**Configured provider**:
A built-in provider in the provider registry, selected explicitly via `--provider` or through a configured model entry; its credential may come from the environment or config.
_Avoid_: default provider, authenticated provider

**Configured model**:
An enabled `provider/model_id` entry under `[[models]]` in `config.toml`, carrying its own endpoint, protocol, and credential metadata; `/models` lists only these and `/model` switches only to these.
_Avoid_: preset model, built-in model, available model

**Embedded context**:
ACP resource blocks embedded in prompt content; advertised only when `PIGO_ACP_ENABLE_EMBEDDED_CONTEXT=true` and otherwise degraded to plain text.
_Avoid_: attached files, resource prompt

**Image prompt block**:
An ACP prompt content block carrying an image; accepted when the active model supports image input.
_Avoid_: picture, attachment

**Available commands**:
The `available_commands_update` notification that advertises the full slash registry, including file templates, skills, and plugin commands, to an ACP client.
_Avoid_: command palette, slash list

**Tool location**:
A resolved file path, plus an optional line, attached to an ACP tool call so clients can open the referenced file.
_Avoid_: file link, tool context

**Structured diff**:
ACP `type: diff` tool content carrying `oldText` and `newText` for a file mutation, emitted instead of raw output when a change is observable.
_Avoid_: diff text, raw edit output

**Bash terminal card**:
ACP terminal content and metadata emitted for `execute` tools so clients like Zed render them as display-only terminals.
_Avoid_: shell card, terminal output

**History replay**:
The `session/load` behavior that replays stored user, assistant, and tool messages as ACP update events so a restored session is visible in the client.
_Avoid_: transcript reload, session restore

**Export resource link**:
An ACP `resource_link` content block with a `file://` URI emitted after `/export` so clients can open or download the exported session file.
_Avoid_: export path, download link

**EntryProjector**:
A registered projection that converts a custom session entry into agent messages for the request context; at most one projector is registered per custom entry type.
_Avoid_: custom message mapper, entry converter

**TransformContext**:
A per-request message-list transformation applied in registration order before conversion to provider messages; output is never written back to persisted history.
_Avoid_: context hook, message preprocessor

**Extension registry**:
The session-scoped registry that collects EntryProjector and TransformContext seams from built-ins, hooks, and plugin declarations.
_Avoid_: context plugin system, hook registry
