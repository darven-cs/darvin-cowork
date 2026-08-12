// Speaks MCP's legacy HTTP+SSE transport (2024-11-05): a long-lived GET
// stream for server-initiated messages, outbound JSON-RPC POSTed to the
// endpoint the server announces via `event: endpoint`. Responses come back
// as `message` frames on the GET stream.

package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

const (
	maxSSEBuffer     = 1 << 20
	ssePostTimeout   = 30 * time.Second
	sseInboundBuffer = 32
)

// SSETransport speaks MCP-over-SSE. It keeps a long-lived GET stream open
// and POSTs every outbound JSON-RPC message to the endpoint announced by
// the server (falling back to the configured URL). The reader goroutine
// routes each `message` frame by JSON-RPC ID to the pending request channel
// (Send/Recv); server-initiated requests (ping, roots/list) and
// notifications are pushed to the inbound channel for the Client to answer
// or forward — the same shape as StdioTransport, so the client stays
// transport-agnostic.
type SSETransport struct {
	URL     string
	Headers map[string]string
	Logger  *zap.Logger

	client *http.Client
	alive  atomic.Bool

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	nextID    int64
	pending   map[int64]chan Frame
	lastFrame Frame
	endpoint  *url.URL
	sessionID string
	inbound   chan Frame

	endpointReady chan struct{}
	endpointOnce  sync.Once
}

// Connect validates the URL and starts the GET stream reader goroutine. The
// inbound channel is created here so Client.inboundLoop can consume it as
// soon as Connect returns.
func (s *SSETransport) Connect(ctx context.Context) error {
	if s.alive.Load() {
		return nil
	}
	getURL, err := url.Parse(strings.TrimSpace(s.URL))
	if err != nil || getURL.Scheme == "" || getURL.Host == "" {
		return fmt.Errorf("mcp sse: invalid url %q", s.URL)
	}
	// The transport's life context must outlive the caller's Connect ctx:
	// the registry cancels connectServer's ctx when connectServer returns,
	// but the SSE stream keeps running until Close. stdio/HTTP transports
	// likewise do not tie their lifetime to the Connect ctx.
	lifeCtx, cancel := context.WithCancel(context.Background())
	s.ctx = lifeCtx
	s.cancel = cancel
	s.client = &http.Client{}
	s.pending = map[int64]chan Frame{}
	s.inbound = make(chan Frame, sseInboundBuffer)
	s.endpointReady = make(chan struct{})
	s.alive.Store(true)

	go s.streamLoop(getURL)
	return nil
}

// streamLoop owns the GET connection: it reconnects on drop and forwards
// every SSE frame to handleEvent. Runs until the transport is closed.
func (s *SSETransport) streamLoop(getURL *url.URL) {
	for s.alive.Load() {
		req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, getURL.String(), nil)
		if err != nil {
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Cache-Control", "no-cache")
		s.mu.Lock()
		if s.sessionID != "" {
			req.Header.Set(httpHeaderSession, s.sessionID)
		}
		s.mu.Unlock()
		for k, v := range s.Headers {
			req.Header.Set(k, v)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			if s.ctx.Err() != nil || !s.retryWait() {
				return
			}
			continue
		}
		if resp.StatusCode/100 != 2 {
			resp.Body.Close()
			if !s.retryWait() {
				return
			}
			continue
		}

		s.mu.Lock()
		if sid := resp.Header.Get(httpHeaderSession); sid != "" {
			s.sessionID = sid
		}
		s.mu.Unlock()

		// The final (post-redirect) URL is the base for relative endpoints.
		baseURL := resp.Request.URL
		s.readSSEStream(resp.Body, baseURL)
		resp.Body.Close()
		if !s.retryWait() {
			return
		}
	}
}

// retryWait sleeps one second between reconnects, or returns false when the
// transport is being shut down.
func (s *SSETransport) retryWait() bool {
	select {
	case <-s.ctx.Done():
		return false
	case <-time.After(time.Second):
		return true
	}
}

// readSSEStream parses SSE frames from r and dispatches each complete event
// to handleEvent. Consecutive data: lines within one event are joined with
// "\n"; an event fires on the blank line that terminates it.
func (s *SSETransport) readSSEStream(r io.Reader, baseURL *url.URL) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 128<<10), maxSSEBuffer)

	var eventName, eventData string
	flush := func() {
		if eventData == "" {
			eventName = ""
			return
		}
		if strings.TrimSpace(eventName) == "" {
			eventName = "message"
		}
		s.handleEvent(strings.TrimSpace(eventName), strings.TrimSpace(eventData), baseURL)
		eventName = ""
		eventData = ""
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if line[0] == ':' {
			continue // comment
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		field := strings.TrimSpace(line[:colon])
		value := line[colon+1:]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		switch field {
		case "event":
			eventName = value
		case "data":
			if eventData != "" {
				eventData += "\n"
			}
			eventData += value
		}
	}
	flush()
}

// handleEvent routes an SSE event. "endpoint" updates the POST target;
// "message" delivers a JSON-RPC frame to a pending request or the inbound
// channel. Anything else is logged and discarded.
func (s *SSETransport) handleEvent(name, data string, baseURL *url.URL) {
	switch name {
	case "endpoint":
		s.setEndpoint(data, baseURL)
	case "message":
		s.handleMessage([]byte(data))
	default:
		if s.Logger != nil {
			s.Logger.Debug("mcp-sse-unknown-event", zap.String("event", name))
		}
	}
}

// setEndpoint records the POST endpoint announced by the server. Relative
// endpoints are resolved against the GET stream's final URL; a cross-origin
// endpoint is rejected so a compromised server cannot redirect credentials.
func (s *SSETransport) setEndpoint(data string, baseURL *url.URL) {
	endpoint, err := url.Parse(strings.TrimSpace(data))
	if err != nil {
		return
	}
	if !endpoint.IsAbs() {
		if baseURL == nil {
			return
		}
		endpoint = baseURL.ResolveReference(endpoint)
	}
	base := baseURL
	if base == nil {
		base, _ = url.Parse(s.URL)
	}
	if base != nil && !sameSSEOrigin(base, endpoint) {
		if s.Logger != nil {
			s.Logger.Debug("mcp-sse-endpoint-rejected", zap.String("endpoint", endpoint.String()))
		}
		return
	}
	s.mu.Lock()
	s.endpoint = endpoint
	s.mu.Unlock()
	if s.endpointReady != nil {
		s.endpointOnce.Do(func() { close(s.endpointReady) })
	}
}

// sameSSEOrigin reports whether two URLs share scheme, host and port — the
// boundary across which an announced endpoint may not redirect requests.
func sameSSEOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || !strings.EqualFold(a.Scheme, b.Scheme) || !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		switch strings.ToLower(u.Scheme) {
		case "https":
			return "443"
		default:
			return "80"
		}
	}
	return port(a) == port(b)
}

// handleMessage routes one `message` frame: a response with a pending ID is
// delivered to its waiting channel; a frame with an ID no request is waiting
// on is a server-initiated request (the Client replies via SendRaw); a frame
// without an ID is a notification.
func (s *SSETransport) handleMessage(payload []byte) {
	var raw map[string]any
	if json.Unmarshal(payload, &raw) != nil {
		return
	}
	if rawID, ok := raw["id"]; ok {
		id := frameID(rawID)
		if s.dispatchResponse(id, Frame{Body: payload}) {
			return
		}
		// Unknown ID → server-initiated request (ping, roots/list, ...).
		s.pushInbound(Frame{Body: payload})
		return
	}
	s.pushInbound(Frame{Body: payload})
}

// frameID coerces a JSON-RPC id (int / float) to int64.
func frameID(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

// dispatchResponse routes a response to the pending channel for id. Returns
// true if a request was waiting on it.
func (s *SSETransport) dispatchResponse(id int64, frame Frame) bool {
	s.mu.Lock()
	ch, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	s.mu.Unlock()
	if ok {
		select {
		case ch <- frame:
		default:
			// Channel already closed; discard.
		}
	}
	return ok
}

// pushInbound delivers a server-initiated frame without blocking the reader
// goroutine — a full buffer drops the frame.
func (s *SSETransport) pushInbound(f Frame) {
	select {
	case s.inbound <- f:
	default:
		if s.Logger != nil {
			s.Logger.Debug("mcp-sse-inbound-dropped", zap.ByteString("msg", f.Body))
		}
	}
}

// Send POSTs one JSON-RPC message and waits for the matching response to be
// routed back by the reader goroutine. Notifications (no id) are posted
// without waiting. The POST itself only needs a 2xx ack — under MCP-over-SSE
// the response arrives later as a `message` frame on the GET stream.
func (s *SSETransport) Send(ctx context.Context, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.alive.Load() {
		return ErrTransportClosed
	}

	var rawBody map[string]any
	if err := json.Unmarshal(body, &rawBody); err != nil {
		return s.post(ctx, body)
	}
	rawID, hasID := rawBody["id"]
	if !hasID {
		return s.post(ctx, body)
	}
	id := frameID(rawID)

	// Register the pending channel before POSTing so a fast server response
	// cannot arrive before we are ready to receive it.
	ch := make(chan Frame, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()

	if err := s.post(ctx, body); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return err
	}

	select {
	case frame, ok := <-ch:
		if !ok {
			return ErrTransportClosed
		}
		if frame.Err != nil {
			return frame.Err
		}
		s.mu.Lock()
		s.lastFrame = frame
		s.mu.Unlock()
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return ctx.Err()
	}
}

// Recv returns the frame collected by Send. The Client holds the mutex
// across the Send+Recv pair, but the transport guards lastFrame anyway so a
// stray concurrent read cannot race the reader goroutine.
func (s *SSETransport) Recv(ctx context.Context) (Frame, error) {
	if err := ctx.Err(); err != nil {
		return Frame{}, err
	}
	if !s.alive.Load() {
		return Frame{}, ErrTransportClosed
	}
	s.mu.Lock()
	f := s.lastFrame
	s.mu.Unlock()
	if f.Body == nil && f.Err == nil {
		return Frame{}, ErrTransportClosed
	}
	return f, nil
}

// Inbound returns the channel of server-initiated frames (requests and
// notifications). Only valid after Connect; the client owns the read side.
func (s *SSETransport) Inbound() <-chan Frame { return s.inbound }

// SendRaw POSTs a complete JSON-RPC frame without waiting for a response.
// Used to answer server-initiated requests (ping, roots/list).
func (s *SSETransport) SendRaw(body []byte) error {
	if !s.alive.Load() {
		return ErrTransportClosed
	}
	ctx, cancel := context.WithTimeout(s.ctx, ssePostTimeout)
	defer cancel()
	return s.post(ctx, body)
}

// Close shuts down the GET stream and wakes every pending request.
func (s *SSETransport) Close() error {
	s.alive.Store(false)
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	for id, ch := range s.pending {
		select {
		case ch <- Frame{Err: ErrTransportClosed}:
		default:
		}
		close(ch)
		delete(s.pending, id)
	}
	s.mu.Unlock()
	return nil
}

// Alive reports whether Send / Recv can still make progress.
func (s *SSETransport) Alive() bool { return s.alive.Load() }

// post POSTs one JSON-RPC body to the announced endpoint and requires a 2xx
// ack. 202 Accepted is the normal MCP-over-SSE confirmation — the response
// arrives separately on the GET stream, so only the ack is validated here.
//
// The POST target comes from the server's `endpoint` event, which under
// MCP-over-SSE is the first SSE frame. POSTing to the GET URL before that
// event is parsed 404s on servers that only accept GET on the stream route,
// so post waits for the endpoint to be announced first.
func (s *SSETransport) post(ctx context.Context, body []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.endpointReady:
	}

	s.mu.Lock()
	endpoint := s.endpoint
	sessionID := s.sessionID
	s.mu.Unlock()

	pctx, cancel := context.WithTimeout(ctx, ssePostTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mcp sse: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set(httpHeaderSession, sessionID)
	}
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp sse: post: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("mcp sse: status %d %s", resp.StatusCode, resp.Status)
	}
	s.mu.Lock()
	if sid := resp.Header.Get(httpHeaderSession); sid != "" {
		s.sessionID = sid
	}
	s.mu.Unlock()
	return nil
}
