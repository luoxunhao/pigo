package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// ErrClosed is returned by transport calls after the transport is closed.
var ErrClosed = errors.New("acp: transport closed")

// Transport is the bidirectional JSON-RPC transport abstraction. Both the
// in-process channel transport and the stdio transport implement it, so the
// dispatch layer never cares which frontend is connected.
type Transport interface {
	// SendRequest sends a request and waits for the matching response.
	SendRequest(ctx context.Context, method string, params any) (json.RawMessage, error)
	// SendNotification sends a notification (no response expected).
	SendNotification(method string, params any) error
	// Recv returns the next incoming message from the peer.
	Recv(ctx context.Context) (IncomingMessage, error)
	// SendResponse replies to a previously received request.
	SendResponse(ctx context.Context, id RequestID, result any, rpcErr *Error) error
	// Close shuts the transport down.
	Close() error
}

// response is the result of a pending request.
type response struct {
	result json.RawMessage
	err    *Error
}

// requestRouter correlates outgoing request ids with their waiters. It is
// shared by the two halves of an in-process transport pair, exactly like peri's
// RequestRouter.
type requestRouter struct {
	mu      sync.Mutex
	nextID  atomic.Int64
	pending map[string]chan response
}

func newRequestRouter() *requestRouter {
	return &requestRouter{pending: make(map[string]chan response)}
}

func (r *requestRouter) register() (RequestID, chan response) {
	id := NumID(r.nextID.Add(1))
	ch := make(chan response, 1)
	r.mu.Lock()
	r.pending[id.String()] = ch
	r.mu.Unlock()
	return id, ch
}

// dispatch completes a pending request. It reports whether the id was pending.
func (r *requestRouter) dispatch(id RequestID, resp response) bool {
	r.mu.Lock()
	ch, ok := r.pending[id.String()]
	if ok {
		delete(r.pending, id.String())
	}
	r.mu.Unlock()
	if ok {
		ch <- resp
	}
	return ok
}

// failAll completes every pending request with a transport error.
func (r *requestRouter) failAll(err error) {
	r.mu.Lock()
	pending := r.pending
	r.pending = make(map[string]chan response)
	r.mu.Unlock()
	for _, ch := range pending {
		ch <- response{err: NewError(CodeInternalError, err.Error())}
	}
}

// channelTransport is one half of an in-process transport pair.
type channelTransport struct {
	router   *requestRouter
	out      chan envelope
	incoming chan IncomingMessage
	done     chan struct{}
	closeOne sync.Once
}

// NewChannelPair returns a connected pair of in-process transports. The first
// is the client side (frontend), the second is the server side (ACP server).
func NewChannelPair() (Transport, Transport) {
	clientToServer := make(chan envelope, 64)
	serverToClient := make(chan envelope, 64)
	router := newRequestRouter()
	client := newChannelTransport(router, serverToClient, clientToServer)
	server := newChannelTransport(router, clientToServer, serverToClient)
	return client, server
}

func newChannelTransport(router *requestRouter, in, out chan envelope) *channelTransport {
	t := &channelTransport{
		router:   router,
		out:      out,
		incoming: make(chan IncomingMessage, 64),
		done:     make(chan struct{}),
	}
	go t.pump(in)
	return t
}

func (t *channelTransport) pump(in chan envelope) {
	defer close(t.incoming)
	for {
		select {
		case <-t.done:
			return
		case env, ok := <-in:
			if !ok {
				return
			}
			msg, err := env.incoming()
			if err != nil {
				continue
			}
			if msg.Response != nil {
				resp := response{result: msg.Response.Result, err: msg.Response.Err}
				if !t.router.dispatch(msg.Response.ID, resp) {
					continue
				}
				continue
			}
			select {
			case t.incoming <- msg:
			case <-t.done:
				return
			}
		}
	}
}

func (t *channelTransport) SendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	id, ch := t.router.register()
	env := envelope{JSONRPC: Version, ID: &id, Method: method, Params: raw}
	select {
	case t.out <- env:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, ErrClosed
	}
	select {
	case resp := <-ch:
		if resp.err != nil {
			return nil, resp.err
		}
		return resp.result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, ErrClosed
	}
}

func (t *channelTransport) SendNotification(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	env := envelope{JSONRPC: Version, Method: method, Params: raw}
	select {
	case t.out <- env:
		return nil
	case <-t.done:
		return ErrClosed
	}
}

func (t *channelTransport) Recv(ctx context.Context) (IncomingMessage, error) {
	select {
	case msg, ok := <-t.incoming:
		if !ok {
			return IncomingMessage{}, ErrClosed
		}
		return msg, nil
	case <-ctx.Done():
		return IncomingMessage{}, ctx.Err()
	case <-t.done:
		return IncomingMessage{}, ErrClosed
	}
}

func (t *channelTransport) SendResponse(ctx context.Context, id RequestID, result any, rpcErr *Error) error {
	var raw json.RawMessage
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		raw = b
	}
	env := envelope{JSONRPC: Version, ID: &id, Result: raw, Error: rpcErr}
	select {
	case t.out <- env:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return ErrClosed
	}
}

func (t *channelTransport) Close() error {
	t.closeOne.Do(func() { close(t.done) })
	return nil
}

// StdioTransport speaks newline-delimited JSON-RPC 2.0 over an io.Reader /
// io.Writer pair (typically os.Stdin / os.Stdout). It is the wire transport
// used by external ACP clients such as IDEs and the future desktop shell.
type StdioTransport struct {
	router   *requestRouter
	incoming chan IncomingMessage
	writer   io.Writer
	writeMu  sync.Mutex
	done     chan struct{}
	closeOne sync.Once
}

// NewStdioTransport creates a stdio transport. The reader and writer must stay
// valid for the transport's lifetime; a nil writer disables writes (tests).
func NewStdioTransport(in io.Reader, out io.Writer) *StdioTransport {
	t := &StdioTransport{
		router:   newRequestRouter(),
		incoming: make(chan IncomingMessage, 64),
		writer:   out,
		done:     make(chan struct{}),
	}
	go t.pump(in)
	return t
}

func (t *StdioTransport) pump(in io.Reader) {
	defer close(t.incoming)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env envelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		msg, err := env.incoming()
		if err != nil {
			continue
		}
		if msg.Response != nil {
			resp := response{result: msg.Response.Result, err: msg.Response.Err}
			if !t.router.dispatch(msg.Response.ID, resp) {
				continue
			}
			continue
		}
		select {
		case t.incoming <- msg:
		case <-t.done:
			return
		}
	}
	if err := scanner.Err(); err != nil {
		t.router.failAll(err)
	}
}

func (t *StdioTransport) SendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	id, ch := t.router.register()
	env := envelope{JSONRPC: Version, ID: &id, Method: method, Params: raw}
	if err := t.write(env); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.err != nil {
			return nil, resp.err
		}
		return resp.result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, ErrClosed
	}
}

func (t *StdioTransport) SendNotification(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	return t.write(envelope{JSONRPC: Version, Method: method, Params: raw})
}

func (t *StdioTransport) Recv(ctx context.Context) (IncomingMessage, error) {
	select {
	case msg, ok := <-t.incoming:
		if !ok {
			return IncomingMessage{}, ErrClosed
		}
		return msg, nil
	case <-ctx.Done():
		return IncomingMessage{}, ctx.Err()
	case <-t.done:
		return IncomingMessage{}, ErrClosed
	}
}

func (t *StdioTransport) SendResponse(ctx context.Context, id RequestID, result any, rpcErr *Error) error {
	var raw json.RawMessage
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		raw = b
	}
	return t.write(envelope{JSONRPC: Version, ID: &id, Result: raw, Error: rpcErr})
}

func (t *StdioTransport) Close() error {
	t.closeOne.Do(func() { close(t.done) })
	return nil
}

func (t *StdioTransport) write(env envelope) error {
	if t.writer == nil {
		return nil
	}
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("acp: marshal: %w", err)
	}
	data = append(data, '\n')
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.writer.Write(data)
	return err
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("acp: marshal params: %w", err)
	}
	return data, nil
}
