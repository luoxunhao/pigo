# Pending queue and compaction semantics: steering/follow-up yes, /queue and /autocompact no

pigo defines one session-level pending queue consumed by steering at tool boundaries and by follow-up at settle points, each with `one-at-a-time` or `all` delivery. It deliberately omits `/queue` and `/autocompact` as commands: pi-acp documents `/queue` but never implements it, and auto-compaction should be pigo's default configured behavior rather than a per-session toggle.
