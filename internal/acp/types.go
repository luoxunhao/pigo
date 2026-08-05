// Package acp implements the Agent Client Protocol server for pigo. The
// package follows peri's layered shape: a transport abstraction (in-process
// channels and stdio), a dispatch loop, session management, event mapping and
// a permission broker. Frontends connect through ACP only; agent core is never
// called directly by a frontend.
package acp

import (
	"encoding/json"
	"fmt"
)

// Version is the JSON-RPC version spoken on the wire.
const Version = "2.0"

// ProtocolVersion is the ACP protocol version pigo negotiates.
const ProtocolVersion = 1

// JSON-RPC error codes used by the ACP server.
const (
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	CodeNotImplemented = -32001
)

// Standard ACP methods and notifications.
const (
	MethodInitialize        = "initialize"
	MethodSessionNew        = "session/new"
	MethodSessionLoad       = "session/load"
	MethodSessionList       = "session/list"
	MethodSessionDelete     = "session/delete"
	MethodSessionClose      = "session/close"
	MethodSessionPrompt     = "session/prompt"
	MethodSessionCancel     = "session/cancel"
	MethodSessionMode       = "session/set_mode"
	MethodSessionConfigOpt  = "session/set_config_option"
	MethodModelSet          = "model/set"
	MethodRequestPermission = "session/request_permission"

	NotificationSessionUpdate = "session/update"

	MethodPigoEvent         = "pigo/event"
	MethodPigoCommand       = "pigo/command"
	MethodPigoStatus        = "pigo/status"
	MethodPigoRewind        = "pigo/rewind"
	MethodPigoFork          = "pigo/fork"
	MethodPigoTree          = "pigo/tree"
	MethodPigoGoal          = "pigo/goal"
	MethodPigoBtw           = "pigo/btw"
	MethodPigoDream         = "pigo/dream"
	MethodPigoRemoteControl = "pigo/remotecontrol"
)

// RequestID is a JSON-RPC request identifier: a number or a string.
type RequestID struct {
	num   int64
	str   string
	isStr bool
}

// NumID returns a numeric request id.
func NumID(n int64) RequestID { return RequestID{num: n} }

// StrID returns a string request id.
func StrID(s string) RequestID { return RequestID{str: s, isStr: true} }

// String renders the id for use as a correlation key.
func (id RequestID) String() string {
	if id.isStr {
		return "s:" + id.str
	}
	return fmt.Sprintf("n:%d", id.num)
}

// MarshalJSON emits the id as its underlying JSON scalar.
func (id RequestID) MarshalJSON() ([]byte, error) {
	if id.isStr {
		return json.Marshal(id.str)
	}
	return json.Marshal(id.num)
}

// UnmarshalJSON accepts either a JSON number or string id.
func (id *RequestID) UnmarshalJSON(data []byte) error {
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		id.num, id.isStr, id.str = n, false, ""
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		id.str, id.isStr, id.num = s, true, 0
		return nil
	}
	return fmt.Errorf("acp: id is neither number nor string: %s", data)
}

// Error is a JSON-RPC error object compatible with ACP clients.
type Error struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("acp error [%d]: %s", e.Code, e.Message)
}

// NewError builds an error with the given JSON-RPC code and message.
func NewError(code int64, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Request is an incoming JSON-RPC request.
type Request struct {
	ID     RequestID
	Method string
	Params json.RawMessage
}

// Notification is an incoming JSON-RPC notification.
type Notification struct {
	Method string
	Params json.RawMessage
}

// Response is an incoming JSON-RPC response (matched by the transport router).
type Response struct {
	ID     RequestID
	Result json.RawMessage
	Err    *Error
}

// IncomingMessage is one message received from the peer.
type IncomingMessage struct {
	Request      *Request
	Notification *Notification
	Response     *Response
}

// envelope is the wire shape of every JSON-RPC message.
type envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *RequestID      `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func (e envelope) incoming() (IncomingMessage, error) {
	switch {
	case e.ID != nil && e.Method != "":
		return IncomingMessage{Request: &Request{ID: *e.ID, Method: e.Method, Params: e.Params}}, nil
	case e.ID == nil && e.Method != "":
		return IncomingMessage{Notification: &Notification{Method: e.Method, Params: e.Params}}, nil
	case e.ID != nil:
		return IncomingMessage{Response: &Response{ID: *e.ID, Result: e.Result, Err: e.Error}}, nil
	default:
		return IncomingMessage{}, fmt.Errorf("acp: malformed envelope")
	}
}
