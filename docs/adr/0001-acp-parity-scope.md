# ACP parity scope: match pi-acp, skip fs/terminal/native MCP

pigo's ACP layer is being aligned to pi-acp's observable protocol surface so external clients (Zed first) get the same feature set. We explicitly exclude ACP `fs/*` and `terminal/*` delegation plus native MCP wiring: those are either pi-acp's own declared limitations or pigo's separate roadmap, and the parity target is the adapter behavior, not the protocol's full theoretical surface. Terminal auth (`authenticate`, `authMethods`, `AUTH_REQUIRED` mapping) is also deferred because pigo has no OAuth or interactive setup flow yet.
