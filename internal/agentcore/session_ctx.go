package agentcore

import "context"

type sessionIDKey struct{}

// WithSessionID returns a child context carrying the owning session id. The
// session manager injects it before a run starts so tools (notably the task
// tool) can create child sessions that record their parent.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, sessionID)
}

// SessionIDFromContext returns the session id carried by ctx, or "" when none
// was injected (headless runs without a backing session).
func SessionIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(sessionIDKey{}).(string); ok {
		return v
	}
	return ""
}
