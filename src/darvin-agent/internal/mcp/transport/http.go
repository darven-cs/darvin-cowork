package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// HTTPTransport speaks MCP-over-HTTP: one POST per JSON-RPC request, the
// response body becomes the next Recv frame. SSE is accepted via the Accept
// header per the MCP spec but v0 does not parse event frames — servers that
// stream via SSE will hang in Recv until they time out. Spec 35 will revisit
// if a real server needs it.
type HTTPTransport struct {
	URL     string
	Headers map[string]string
	Timeout time.Duration

	mu           sync.Mutex // guards sessionID + lastResponse across Send/Recv.
	sessionID    string
	lastResponse []byte

	client *http.Client
	alive  atomic.Bool
}

const (
	httpHeaderSession = "Mcp-Session-Id"
	defaultHTTPTimeout = 30 * time.Second
)

func (h *HTTPTransport) Connect(_ context.Context) error {
	if h.alive.Load() {
		return nil
	}
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	h.client = &http.Client{Timeout: timeout}
	h.alive.Store(true)
	return nil
}

func (h *HTTPTransport) Send(ctx context.Context, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !h.alive.Load() {
		return ErrTransportClosed
	}
	if h.client == nil {
		return ErrTransportClosed
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mcp http: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}

	h.mu.Lock()
	if h.sessionID != "" {
		req.Header.Set(httpHeaderSession, h.sessionID)
	}
	h.mu.Unlock()

	resp, err := h.client.Do(req)
	if err != nil {
		h.alive.Store(false)
		return fmt.Errorf("mcp http: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		h.alive.Store(false)
		// Drain a small slice so the connection can be reused, then return.
		_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
		return fmt.Errorf("mcp http: status %d %s", resp.StatusCode, resp.Status)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		h.alive.Store(false)
		return fmt.Errorf("mcp http: read body: %w", err)
	}

	h.mu.Lock()
	h.lastResponse = respBody
	if sid := resp.Header.Get(httpHeaderSession); sid != "" {
		h.sessionID = sid
	}
	h.mu.Unlock()

	return nil
}

func (h *HTTPTransport) Recv(ctx context.Context) (Frame, error) {
	if err := ctx.Err(); err != nil {
		return Frame{}, err
	}
	if !h.alive.Load() {
		return Frame{}, ErrTransportClosed
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastResponse == nil {
		return Frame{}, ErrTransportClosed
	}
	return Frame{Body: h.lastResponse}, nil
}

func (h *HTTPTransport) Close() error {
	h.alive.Store(false)
	h.client = nil
	return nil
}

func (h *HTTPTransport) Alive() bool {
	return h.alive.Load()
}
