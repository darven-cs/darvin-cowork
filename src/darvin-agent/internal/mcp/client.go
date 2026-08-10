// Package mcp implements the Go side of the Model Context Protocol: a
// JSON-RPC 2.0 client that talks to MCP servers over stdio or HTTP,
// plus the server registry and launch resolution. Transport lives in
// the transport subpackage; this file covers the JSON-RPC client.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"darvin-cowork/backend/internal/mcp/transport"
)

// Client is the JSON-RPC 2.0 client side of the MCP wire. One Client per
// transport; the transport owns the byte stream and the Client owns the
// envelope. Call serializes Send/Recv with a mutex because stdio framing
// is a strict request/response protocol — a second Call interleaving its
// Send before the first Call's Recv would desync the reader.
type Client struct {
	transport transport.Transport
	mu        sync.Mutex
	nextID    atomic.Int64

	// roots is the workspace-root list exposed to servers via roots/list.
	// The runtime refreshes it when the workspace moves.
	roots []Root

	// notifications receives server-initiated notifications (no id).
	notifications chan []byte

	// stopInbound cancels the current inbound reader goroutine. Reconnect
	// replaces the transport, so each generation gets its own stop signal.
	stopMu      sync.Mutex
	stopInbound chan struct{}

	// reconnectFactory, if set, is invoked to rebuild the transport after
	// a connection error. Callers wire it in (e.g. the registry spec) so
	// the Client does not need to know how to spawn the server again.
	reconnectFactory func() (transport.Transport, error)
}

// Root is one workspace root advertised to MCP servers. URI is a file://
// or https:// URI; Name is an optional display label.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// inboundReader is implemented by transports that can receive
// server-initiated messages (stdio); HTTP/SSE are stateless and do not.
type inboundReader interface {
	Inbound() <-chan transport.Frame
}

// rawSender is implemented by transports that can write a frame without
// waiting for a reply (needed to answer server-initiated requests).
type rawSender interface {
	SendRaw(body []byte) error
}

// NewClient wraps a transport. The caller still owns the transport's
// lifecycle — Close on the Client closes the transport.
func NewClient(t transport.Transport) *Client {
	return &Client{
		transport:     t,
		notifications: make(chan []byte, 32),
	}
}

// SetRoots refreshes the workspace roots advertised to servers. Safe to
// call after Connect; the value is read on each roots/list request.
func (c *Client) SetRoots(roots []Root) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.roots = roots
}

// Notifications returns the channel of server-initiated notifications
// (raw JSON-RPC bodies with no id, e.g. tools/list_changed). Drop when the
// channel is full — notifications are advisory.
func (c *Client) Notifications() <-chan []byte { return c.notifications }

// WithReconnectFactory returns the receiver with a factory installed; it
// is safe to call before Connect, but not after the Client is shared.
// Provided as a builder step rather than a constructor parameter so
// NewClient stays a one-liner for tests and the trivial registry case.
func (c *Client) WithReconnectFactory(f func() (transport.Transport, error)) *Client {
	c.reconnectFactory = f
	return c
}

// Transport exposes the underlying transport for inspection (e.g. the
// launcher checks Alive() to decide if a reconnect is needed).
func (c *Client) Transport() transport.Transport { return c.transport }

func (c *Client) Connect(ctx context.Context) error {
	if err := c.transport.Connect(ctx); err != nil {
		return err
	}
	c.startInbound()
	return nil
}

// Close tears down the transport and stops the inbound reader.
func (c *Client) Close() error {
	c.stopMu.Lock()
	if c.stopInbound != nil {
		close(c.stopInbound)
		c.stopInbound = nil
	}
	c.stopMu.Unlock()
	return c.transport.Close()
}

// startInbound spins up a fresh reader goroutine for the current
// transport. A prior generation is cancelled first so reconnect does not
// leak goroutines.
func (c *Client) startInbound() {
	c.stopMu.Lock()
	if c.stopInbound != nil {
		close(c.stopInbound)
	}
	stop := make(chan struct{})
	c.stopInbound = stop
	c.stopMu.Unlock()
	go c.inboundLoop(stop)
}

func (c *Client) inboundLoop(stop chan struct{}) {
	t, ok := c.transport.(inboundReader)
	if !ok {
		return
	}
	ch := t.Inbound()
	for {
		select {
		case <-stop:
			return
		case f, ok := <-ch:
			if !ok {
				return
			}
			c.handleInbound(f)
		}
	}
}

// handleInbound routes a server-initiated frame: requests (an id field)
// are answered in place; notifications are forwarded to subscribers.
func (c *Client) handleInbound(f transport.Frame) {
	var msg map[string]any
	if json.Unmarshal(f.Body, &msg) != nil {
		return
	}
	if id, ok := msg["id"]; ok {
		method, _ := msg["method"].(string)
		c.replyToRequest(id, method)
		return
	}
	select {
	case c.notifications <- f.Body:
	default:
		// Full buffer; the notification is advisory.
	}
}

// replyToRequest answers a server-initiated request. ping → pong,
// roots/list → the configured roots, anything else → method-not-found.
func (c *Client) replyToRequest(id any, method string) {
	switch method {
	case "ping":
		c.sendReply(id, map[string]any{}, nil)
	case "roots/list":
		c.mu.Lock()
		roots := append([]Root(nil), c.roots...)
		c.mu.Unlock()
		c.sendReply(id, map[string]any{"roots": roots}, nil)
	default:
		c.sendReply(id, nil, &RPCError{Code: -32601, Message: "method not found: " + method})
	}
}

// sendReply writes a JSON-RPC response for a server request over the raw
// sender path. No-op when the transport cannot send raw frames.
func (c *Client) sendReply(id any, result any, rpcErr *RPCError) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		resp["result"] = result
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return
	}
	if rs, ok := c.transport.(rawSender); ok {
		_ = rs.SendRaw(body)
	}
}

// Call sends a JSON-RPC request and returns the raw result. The Client
// holds the mutex for the whole Send/Recv pair so concurrent callers
// are serialized.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.transport.Alive() {
		return nil, ErrTransportClosed
	}

	id := c.nextID.Add(1)
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	if err := c.transport.Send(ctx, body); err != nil {
		return nil, err
	}

	frame, err := c.transport.Recv(ctx)
	if err != nil {
		return nil, err
	}

	var resp Response
	if err := json.Unmarshal(frame.Body, &resp); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal response: %w", err)
	}
	if resp.ID != id {
		return nil, fmt.Errorf("mcp: response id %d does not match request id %d", resp.ID, id)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

// CallWithRetry wraps Call with an exponential-backoff retry loop. RPC
// errors (server returned an error) are not retried — only transport
// connection errors trigger a reconnect + retry. maxRetries is the
// number of *additional* attempts after the first; total calls =
// 1 + maxRetries. Backoff starts at backoffBase and doubles each round.
func (c *Client) CallWithRetry(ctx context.Context, method string, params any, maxRetries int, backoffBase time.Duration) (json.RawMessage, error) {
	if backoffBase <= 0 {
		backoffBase = time.Second
	}
	if maxRetries < 0 {
		maxRetries = 0
	}

	var lastErr error
	backoff := backoffBase
	for attempt := 0; attempt <= maxRetries; attempt++ {
		raw, err := c.Call(ctx, method, params)
		if err == nil {
			return raw, nil
		}
		lastErr = err

		if !isConnectionError(err) {
			return nil, err
		}

		// Last attempt: do not waste a sleep cycle.
		if attempt == maxRetries {
			break
		}

		if rerr := c.reconnect(ctx); rerr != nil {
			// Reconnect failure is still a connection error; record it
			// and continue so the caller's maxRetries is the real cap.
			lastErr = fmt.Errorf("reconnect: %w", rerr)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return nil, fmt.Errorf("%w: %v", ErrRPCMaxRetries, lastErr)
}

func (c *Client) reconnect(ctx context.Context) error {
	if c.reconnectFactory == nil {
		return ErrNoReconnectFactory
	}
	_ = c.transport.Close()
	next, err := c.reconnectFactory()
	if err != nil {
		return err
	}
	if err := next.Connect(ctx); err != nil {
		return err
	}
	c.transport = next
	c.startInbound()
	return nil
}

// Initialize performs the MCP handshake. The params mirror the
// 2024-11-05 protocol version; clientInfo identifies darvin-cowork so
// servers can log / rate-limit us. After a successful initialize it sends
// the spec-mandated `notifications/initialized` so the server starts
// streaming events.
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{
			"roots": map[string]any{},
		},
		"clientInfo": map[string]any{
			"name":    "darvin-cowork",
			"version": "0.1.0",
		},
	}
	raw, err := c.Call(ctx, "initialize", params)
	if err != nil {
		return nil, err
	}
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal initialize result: %w", err)
	}
	_ = c.notify(ctx, "notifications/initialized", nil)
	return &result, nil
}

// notify sends a JSON-RPC notification (a request with no id — the server
// does not respond). The body is marshalled from a map so no `id` field is
// emitted; the stdio transport relies on that to skip waiting for a reply.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		payload["params"] = params
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mcp: marshal notification: %w", err)
	}
	if !c.transport.Alive() {
		return ErrTransportClosed
	}
	return c.transport.Send(ctx, body)
}

// ListTools asks the server for the tools it exposes.
func (c *Client) ListTools(ctx context.Context) ([]ToolDescriptor, error) {
	raw, err := c.Call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []ToolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal tools/list result: %w", err)
	}
	return result.Tools, nil
}

// CallTool invokes a single tool by name. args is the MCP `arguments`
// object — its shape is server-defined, so we accept any map.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	raw, err := c.Call(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}
	var result CallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal tools/call result: %w", err)
	}
	return &result, nil
}

// isConnectionError decides whether a Call error is worth retrying. RPC
// errors and validation errors are *not* retried because re-running the
// same request against the same server gets the same answer.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrTransportClosed) {
		return true
	}
	if errors.Is(err, transport.ErrTransportClosed) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// Subprocess pipes return wrapped os errors; the message is the
	// cheapest reliable signal without an errors.As chain per case.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "EOF"):
		return true
	}
	return false
}
