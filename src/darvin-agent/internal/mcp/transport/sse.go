// Speaks MCP over a long-lived SSE stream with POSTed outbound requests.

package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SSETransport speaks MCP-over-SSE. It opens a long-lived GET stream on
// construction (Connect) and multiplexes incoming SSE events to the
// notification handler. Outbound requests are POSTed as JSON.
//
// Supports:
//   - MCP 1.x SSE: GET stream for server-initiated events, POST for requests.
//   - streamable-http: Accept: application/json, text/event-stream on POST.
//   - Legacy JSON sync: Accept: application/json on POST.
//
// SSE event name → JSON-RPC method mapping:
//
//	"message" → notification delivered to the notification handler.
//	"endpoint" → streamable-http endpoint hint; updates the POST URL.
//	Other event names → logged and discarded.
type SSETransport struct {
	URL     string
	Headers map[string]string
	Timeout time.Duration

	client       *http.Client
	alive        atomic.Bool
	mu           sync.Mutex
	sessionID    string
	lastResponse []byte

	// notificationHandler is called for each incoming SSE event whose
	// data is valid JSON (a JSON-RPC notification). Set via Option.
	notificationHandler func(data []byte)
	// streamURL is the URL for the SSE stream; updated by "endpoint" events.
	streamURL string
}

// SSETransportOption configures an SSETransport.
type SSETransportOption func(*SSETransport)

// WithNotificationHandler sets the callback invoked for each incoming
// SSE "message" event whose data is valid JSON-RPC. If not set, events
// are logged at debug level.
func WithNotificationHandler(fn func([]byte)) SSETransportOption {
	return func(t *SSETransport) {
		t.notificationHandler = fn
	}
}

const (
	sseHeaderSession  = "Mcp-Session-Id"
	defaultSSETimeout = 0 // no HTTP-level timeout on SSE stream; controlled by context.
)

// Connect opens the SSE stream via GET and starts the event reader goroutine.
func (s *SSETransport) Connect(ctx context.Context) error {
	if s.alive.Load() {
		return nil
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = defaultSSETimeout
	}
	if timeout > 0 {
		s.client = &http.Client{Timeout: timeout}
	} else {
		// No HTTP timeout; we use a long-lived request with context propagation.
		s.client = &http.Client{}
	}
	s.streamURL = s.URL
	s.alive.Store(true)

	go s.streamLoop(ctx)
	return nil
}

// streamLoop runs the SSE GET stream and reads events until the context
// is cancelled or the connection drops.
func (s *SSETransport) streamLoop(ctx context.Context) {
	for s.alive.Load() {
		url := s.streamURL
		if url == "" {
			url = s.URL
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			s.alive.Store(false)
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Connection", "keep-alive")
		s.mu.Lock()
		if s.sessionID != "" {
			req.Header.Set(sseHeaderSession, s.sessionID)
		}
		s.mu.Unlock()

		resp, err := s.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				// Context cancelled — expected shutdown path.
				return
			}
			// Retry on transient errors.
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}
		if resp == nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}

		// Extract session ID from response.
		if sid := resp.Header.Get(sseHeaderSession); sid != "" {
			s.mu.Lock()
			s.sessionID = sid
			s.mu.Unlock()
		}

		// Read SSE events until the stream closes.
		s.readSSEStream(resp.Body)
		resp.Body.Close()

		// If the stream closed without context cancellation, retry.
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// readSSEStream parses SSE frames from r until EOF.
func (s *SSETransport) readSSEStream(r io.Reader) {
	scanner := bufio.NewScanner(r)
	// SSE events can be large (tool results, etc.); use a large buffer.
	scanner.Buffer(make([]byte, 0, 128<<10), 1<<20)

	var eventName, eventData string
	flush := func() {
		eventName = strings.TrimSpace(eventName)
		eventData = strings.TrimSpace(eventData)
		if eventName == "" || eventData == "" {
			eventName = ""
			eventData = ""
			return
		}
		switch eventName {
		case "message":
			s.deliverNotification([]byte(eventData))
		case "endpoint":
			// streamable-http: server is telling us the URL to use for POST.
			var ep struct {
				Endpoint string `json:"endpoint"`
			}
			if json.Unmarshal([]byte(eventData), &ep) == nil && ep.Endpoint != "" {
				s.mu.Lock()
				s.streamURL = ep.Endpoint
				s.mu.Unlock()
			}
		default:
			// Unknown event type — log and discard.
		}
		eventName = ""
		eventData = ""
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if len(line) < 6 {
			continue
		}
		// SSE field format: "field-name: value\r\n"
		if line[0] == ':' {
			// Comment line — skip.
			continue
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
		case "id", "retry":
			// Ignored for now.
		}
	}
	// Flush any remaining event on EOF.
	flush()
}

// deliverNotification routes a JSON-RPC notification to the handler.
func (s *SSETransport) deliverNotification(data []byte) {
	s.mu.Lock()
	handler := s.notificationHandler
	s.mu.Unlock()
	if handler != nil {
		handler(data)
	}
}

// Send POSTs a JSON-RPC request and caches the response body so Recv
// can return it to the synchronous client. For streamable-http servers,
// the POST URL may be updated by "endpoint" events.
func (s *SSETransport) Send(ctx context.Context, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.alive.Load() {
		return ErrTransportClosed
	}
	if s.client == nil {
		return ErrTransportClosed
	}

	url := s.URL
	s.mu.Lock()
	if s.streamURL != "" {
		url = s.streamURL
	}
	sessionID := s.sessionID
	s.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mcp sse: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set(sseHeaderSession, sessionID)
	}
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp sse: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
		return fmt.Errorf("mcp sse: status %d %s", resp.StatusCode, resp.Status)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("mcp sse: read body: %w", err)
	}

	s.mu.Lock()
	s.lastResponse = respBody
	if sid := resp.Header.Get(sseHeaderSession); sid != "" {
		s.sessionID = sid
	}
	s.mu.Unlock()

	return nil
}

// Recv returns the response cached by the most recent Send.
func (s *SSETransport) Recv(ctx context.Context) (Frame, error) {
	if err := ctx.Err(); err != nil {
		return Frame{}, err
	}
	if !s.alive.Load() {
		return Frame{}, ErrTransportClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastResponse == nil {
		return Frame{}, ErrTransportClosed
	}
	return Frame{Body: s.lastResponse}, nil
}

// Close cancels the SSE stream and shuts down the transport.
func (s *SSETransport) Close() error {
	s.alive.Store(false)
	s.client = nil
	return nil
}

// Alive reports whether the transport is alive.
func (s *SSETransport) Alive() bool {
	return s.alive.Load()
}
