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

**Scope**:
An agent's layered registration context: each session and sub-agent owns a scope whose registries (prompt sections, transforms, tools) inherit from its parent scope's registries and may shadow entries by name.
_Avoid_: context tree, registry chain, inheritance stack

**Fork**:
A sub-agent context semantic in which the child session is seeded with the parent session's completed-turn history, so the child continues on inherited context; the in-flight turn is excluded.
_Avoid_: inherited subagent, context clone, context carry-over

**Spawn**:
A sub-agent context semantic in which the child starts with zero parent context — its own messages, its own prompt; the historical default for pigo sub-agents.
_Avoid_: fresh subagent, isolated subagent, clean context

**Persona**:
The named lead section of a system prompt; a sub-agent inherits its parent's persona by default and may shadow it with its own declaration (skill body, plugin spec, or explicit text).
_Avoid_: base instruction, prompt override, role text

**Sub-agent descriptor**:
The durable snapshot of a sub-agent's declared composition — model, provider, persona, tool filter, delegation depth, and inheritance facts — recorded in the child session and reconstructable on cold resume.
_Avoid_: subagent config, subagent metadata, child header

**Delegation depth**:
A sub-agent's recursion depth, persisted with the session so a resumed child cannot reset it; a parent passes depth+1 to its children and a configured maximum rejects deeper delegation.
_Avoid_: recursion count, nesting level, depth counter

**Tool filter**:
The allow/deny pair that narrows a sub-agent's inherited tool set: allow keeps only listed names, deny removes listed names, applied to the scope-inherited registry.
_Avoid_: tool allowlist, tool restriction, tool policy

**Structured output**:
A sub-agent result mode in which the child must finish by calling a scoped capture tool whose arguments match a JSON Schema, yielding a validated structured result to the parent.
_Avoid_: output schema tool, JSON result, capture contract

**Sandbox mode**:
The session's file-boundary policy, one of three levels — read-only (no writes), workspace-write (writes under the workspace root and platform temp areas), danger-full-access (unrestricted); the default is workspace-write and the current mode is injected into the model's context each turn.
_Avoid_: permission level, file policy, security mode

**Workspace root**:
The writable root that defines workspace-write mode: the session workspace plus the platform temporary areas, canonicalized so every enforcement dialect compares the same resolved paths.
_Avoid_: project root, allowed root, cwd

**File fence**:
The in-process path-containment check applied to file tools (read/write/edit/search): a target is allowed only when it is a writable root or lies beneath one, with filesystem-identity fallback for alias spellings.
_Avoid_: path check, containment filter, root guard

**Escalation**:
The single-call approval flow that widens sandbox enforcement: the model retries the exact denied operation with sandbox_permissions and a justification, the user approves once, and only that call runs under the wider mode; non-widening requests never prompt a human.
_Avoid_: permission override, mode switch, sandbox bypass

**SANDBOX_UNAVAILABLE**:
The fail-closed error raised when a requested sandbox mode has no usable backend on this host; the operation is refused rather than run unconfined.
_Avoid_: sandbox missing, confinement error, backend absent

**Policy reminder**:
The per-turn model-facing injection that states the current sandbox mode, workspace root, and escalation channel; it rides the reminder mechanism, never enters persisted history, and never invalidates the system-prompt fingerprint cache.
_Avoid_: sandbox notice, policy context, mode hint

**Prompt section**:
A named static system-prompt part registered with an order; sections sort by order, a child scope may shadow a parent section by name, a complete section replaces the entire prompt, and a suppressor removes the dynamic context block.
_Avoid_: prompt block, prompt part, system segment

**Prompt context**:
A named dynamic prompt part rendered per request into the request copy only, never into persisted history; tool guidance and policy statements are contexts.
_Avoid_: dynamic section, per-request injection, context block

**Prompt variable**:
A `{{name}}` reference interpolated when a section or context renders; model and cwd are built-in, other names are open for registration and shadowing.
_Avoid_: template variable, placeholder, macro

**Tool guidance**:
The per-tool context segment (tool:read, tool:write, tool:bash, ...) that tells the model how and when to use a tool beyond its schema description.
_Avoid_: tool hint, tool instructions, tool tips

**Golden snapshot**:
A frozen expected system-prompt output generated from pigo's own registry configuration and refreshed deliberately; the replacement for the pi byte-parity corpus.
_Avoid_: parity fixture, golden corpus, expected output
