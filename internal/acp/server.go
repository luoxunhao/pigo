package acp

import (
	"context"
	"encoding/json"
	"os"
)

// VersionValue is the pigo version advertised during initialize. cmd/pigo
// stamps the real build metadata through NewServer when available.
var VersionValue = "dev"

// Handler dispatches ACP requests and notifications. It is the seam the
// session layer (ticket 03+) plugs into; a nil handler falls back to the
// built-in initialize-only dispatcher.
type Handler interface {
	HandleRequest(ctx context.Context, id RequestID, method string, params json.RawMessage) (any, *Error)
	HandleNotification(ctx context.Context, method string, params json.RawMessage)
}

// DeferredHandler is an optional Handler extension for methods whose response
// is sent asynchronously (session/prompt). When HandleDeferredRequest returns
// true the server skips the synchronous response.
type DeferredHandler interface {
	Handler
	HandleDeferredRequest(ctx context.Context, id RequestID, method string, params json.RawMessage) bool
}

// HandlerFunc adapts a function to the Handler interface.
type HandlerFunc func(ctx context.Context, id RequestID, method string, params json.RawMessage) (any, *Error)

// HandleRequest calls f.
func (f HandlerFunc) HandleRequest(ctx context.Context, id RequestID, method string, params json.RawMessage) (any, *Error) {
	return f(ctx, id, method, params)
}

// HandleNotification is a no-op for function handlers that do not care.
func (f HandlerFunc) HandleNotification(ctx context.Context, method string, params json.RawMessage) {}

// Server drives one ACP transport until the context is cancelled or the
// transport closes. Requests are answered in order; long-running methods
// (session/prompt) are expected to move their work to background tasks so the
// loop stays responsive to session/cancel.
type Server struct {
	transport Transport
	handler   Handler
	version   string
}

// NewServer builds a server over a transport. When handler is nil the built-in
// initialize-only dispatcher is used.
func NewServer(transport Transport, handler Handler) *Server {
	return &Server{transport: transport, handler: handler, version: VersionValue}
}

// SetVersion overrides the version advertised in initialize.
func (s *Server) SetVersion(v string) { s.version = v }

// Serve runs the dispatch loop until ctx is done or the transport closes.
func (s *Server) Serve(ctx context.Context) error {
	for {
		msg, err := s.transport.Recv(ctx)
		if err != nil {
			return err
		}
		switch {
		case msg.Request != nil:
			if dh, ok := s.handler.(DeferredHandler); ok && dh.HandleDeferredRequest(ctx, msg.Request.ID, msg.Request.Method, msg.Request.Params) {
				continue
			}
			result, rpcErr := s.dispatch(ctx, msg.Request.ID, msg.Request.Method, msg.Request.Params)
			if err := s.transport.SendResponse(ctx, msg.Request.ID, result, rpcErr); err != nil {
				return err
			}
		case msg.Notification != nil:
			s.dispatchNotification(ctx, msg.Notification.Method, msg.Notification.Params)
		case msg.Response != nil:
			// Responses are routed by the transport; anything reaching the loop is
			// an unmatched response and can be ignored.
		}
	}
}

func (s *Server) dispatch(ctx context.Context, id RequestID, method string, params json.RawMessage) (any, *Error) {
	if s.handler != nil {
		return s.handler.HandleRequest(ctx, id, method, params)
	}
	if method == MethodInitialize {
		return s.initialize(params)
	}
	return nil, NewError(CodeMethodNotFound, "method not found: "+method)
}

func (s *Server) dispatchNotification(ctx context.Context, method string, params json.RawMessage) {
	if s.handler != nil {
		s.handler.HandleNotification(ctx, method, params)
	}
}

func (s *Server) initialize(params json.RawMessage) (any, *Error) {
	return buildInitializeResponse(s.version), nil
}

func buildInitializeResponse(version string) map[string]any {
	// Protocol version negotiation: pigo speaks v1. The client's declared
	// version and capabilities are intentionally ignored in v1; a client that
	// cannot speak v1 disconnects after reading our response.
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"agentCapabilities": map[string]any{
			"loadSession": true,
			"promptCapabilities": map[string]any{
				"image":           true,
				"audio":           false,
				"embeddedContext": os.Getenv("PIGO_ACP_ENABLE_EMBEDDED_CONTEXT") == "true",
			},
			"sessionCapabilities": map[string]any{
				"close":  map[string]any{},
				"list":   map[string]any{},
				"delete": map[string]any{},
			},
			"mcpCapabilities": map[string]any{"http": false, "sse": false},
			"_meta": map[string]any{
				"pigo.extensions":      true,
				"pigo.event":           true,
				"pigo.command":         true,
				"pigo.status":          true,
				"pigo.models":          true,
				"pigo.models.discover": true,
				"pigo.config":          true,
				"pigo.messages":        true,
				"pigo.providers":       true,
			},
		},
		"authMethods": []any{},
		"agentInfo": map[string]any{
			"name":    "pigo",
			"title":   "pigo ACP",
			"version": version,
		},
	}
}
