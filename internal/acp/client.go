package acp

import (
	"context"
	"encoding/json"
	"sync"
)

// PermissionHandler answers an incoming session/request_permission request.
// It runs on the client pump goroutine and may block until the user decides.
type PermissionHandler func(req Request) (any, *Error)

// Client is the frontend side of an ACP connection. It pumps incoming
// notifications onto a channel for the UI and routes permission requests to
// the registered handler.
type Client struct {
	transport  Transport
	notif      chan IncomingMessage
	permission PermissionHandler
	mu         sync.Mutex
	cancel     context.CancelFunc
	closeOnce  sync.Once
}

// NewClient starts a client over the given transport.
func NewClient(transport Transport) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		transport: transport,
		notif:     make(chan IncomingMessage, 256),
		cancel:    cancel,
	}
	go c.pump(ctx)
	return c
}

func (c *Client) pump(ctx context.Context) {
	defer close(c.notif)
	for {
		msg, err := c.transport.Recv(ctx)
		if err != nil {
			return
		}
		if msg.Request != nil && msg.Request.Method == MethodRequestPermission {
			c.mu.Lock()
			h := c.permission
			c.mu.Unlock()
			if h != nil {
				result, rpcErr := h(*msg.Request)
				_ = c.transport.SendResponse(ctx, msg.Request.ID, result, rpcErr)
			} else {
				_ = c.transport.SendResponse(ctx, msg.Request.ID, nil, NewError(CodeInternalError, "no permission handler"))
			}
			continue
		}
		select {
		case c.notif <- msg:
		case <-ctx.Done():
			return
		}
	}
}

// SetPermissionHandler installs the handler for permission requests.
func (c *Client) SetPermissionHandler(h PermissionHandler) {
	c.mu.Lock()
	c.permission = h
	c.mu.Unlock()
}

// Notifications returns the channel carrying incoming requests/notifications
// (permission requests are consumed by the handler and never reach this
// channel). The channel is closed when the client is closed.
func (c *Client) Notifications() <-chan IncomingMessage { return c.notif }

// Initialize performs the ACP handshake.
func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.transport.SendRequest(ctx, MethodInitialize, map[string]any{
		"protocolVersion":    ProtocolVersion,
		"clientCapabilities": map[string]any{},
	})
	return err
}

// NewSession creates a session in cwd and returns its id.
func (c *Client) NewSession(ctx context.Context, cwd string) (string, error) {
	raw, err := c.transport.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": cwd})
	if err != nil {
		return "", err
	}
	var resp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	return resp.SessionID, nil
}

// LoadSession restores an existing session and returns its id.
func (c *Client) LoadSession(ctx context.Context, sessionID, cwd string) (string, error) {
	raw, err := c.transport.SendRequest(ctx, MethodSessionLoad, map[string]any{
		"sessionId": sessionID,
		"cwd":       cwd,
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	return resp.SessionID, nil
}

// Prompt sends a text prompt and waits for the turn to finish, returning the
// ACP stop reason.
func (c *Client) Prompt(ctx context.Context, sessionID, text string) (string, error) {
	raw, err := c.transport.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": text}},
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	return resp.StopReason, nil
}

// Cancel cancels the running turn on a session.
func (c *Client) Cancel(sessionID string) error {
	return c.transport.SendNotification(MethodSessionCancel, map[string]any{"sessionId": sessionID})
}

// SetModel switches the session model for the next turn.
func (c *Client) SetModel(ctx context.Context, sessionID, modelID string) error {
	_, err := c.transport.SendRequest(ctx, MethodModelSet, map[string]any{
		"sessionId": sessionID,
		"modelId":   modelID,
	})
	return err
}

// Command executes a pigo slash command and returns its text result.
func (c *Client) Command(ctx context.Context, sessionID, command string) (string, error) {
	raw, err := c.transport.SendRequest(ctx, MethodPigoCommand, map[string]any{
		"sessionId": sessionID,
		"command":   command,
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	return resp.Text, nil
}

// Close shuts the client pump down.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.cancel()
		_ = c.transport.Close()
	})
}
