// Speaks MCP JSON-RPC over one HTTP POST per request.

package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// HTTPTransport speaks MCP-over-HTTP: one POST per JSON-RPC request, the
// response body becomes the next Recv frame. Responses may be application/json
// (the common synchronous case) or text/event-stream (streamable HTTP); Recv
// parses the SSE `message` event for the latter. A 401/410 response signals a
// stale MCP session (ErrSessionExpired) so the Client can re-initialize and
// replay the call.
type HTTPTransport struct {
	URL     string
	Headers map[string]string
	Timeout time.Duration

	mu           sync.Mutex // guards sessionID + lastResponse across Send/Recv.
	sessionID    string
	lastResponse []byte
	// lastContentType records the Content-Type of the last response so Recv
	// can decide whether to parse SSE frames.
	lastContentType string

	client *http.Client
	alive  atomic.Bool
}

const (
	httpHeaderSession  = "Mcp-Session-Id"
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

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusGone {
		// MCP session expired (401/410) — the client must re-initialize
		// before this call can be replayed. Signalled as a sentinel error
		// so the Client can distinguish it from a hard connection break.
		h.alive.Store(false)
		return ErrSessionExpired
	}
	if resp.StatusCode/100 != 2 {
		// Any 2xx is a success. 202 Accepted in particular is the
		// streamable-HTTP ack for notifications (a POST with no id); a
		// strict 200 check would kill the transport mid-handshake.
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
	h.lastContentType = resp.Header.Get("Content-Type")
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
	body := h.lastResponse
	h.lastResponse = nil // consume; the next Send repopulates it
	if strings.Contains(strings.ToLower(h.lastContentType), "text/event-stream") {
		msg, err := parseSSEMessage(body)
		if err != nil {
			return Frame{}, err
		}
		return Frame{Body: msg}, nil
	}
	return Frame{Body: body}, nil
}

// parseSSEMessage extracts the JSON-RPC `message` event from a
// text/event-stream body. Streamable-HTTP servers respond to a POST with
// an SSE stream whose `event: message` frame carries the JSON-RPC response
// (or a notification). The first message event wins — the synchronous
// client only needs the response for the in-flight request.
func parseSSEMessage(body []byte) ([]byte, error) {
	events := bytes.Split(body, []byte("\n\n"))
	for _, ev := range events {
		if len(bytes.TrimSpace(ev)) == 0 {
			continue
		}
		eventType := ""
		var dataLines [][]byte
		for _, line := range bytes.Split(ev, []byte("\n")) {
			trimmed := bytes.TrimSpace(line)
			switch {
			case bytes.HasPrefix(trimmed, []byte("event:")):
				eventType = strings.TrimSpace(string(trimmed[len("event:"):]))
			case bytes.HasPrefix(trimmed, []byte("data:")):
				dataLines = append(dataLines, bytes.TrimSpace(trimmed[len("data:"):]))
			}
		}
		if len(dataLines) == 0 {
			continue
		}
		data := bytes.Join(dataLines, nil)
		// A `message` event is the JSON-RPC payload; a lone data block with
		// no event type is treated as one too.
		if eventType == "message" || eventType == "" {
			return data, nil
		}
		// Non-message events (notifications) are ignored by the synchronous
		// client; keep scanning for the message event.
	}
	return nil, fmt.Errorf("mcp http: no message event in SSE response")
}

func (h *HTTPTransport) Close() error {
	h.alive.Store(false)
	h.client = nil
	return nil
}

func (h *HTTPTransport) Alive() bool {
	return h.alive.Load()
}
