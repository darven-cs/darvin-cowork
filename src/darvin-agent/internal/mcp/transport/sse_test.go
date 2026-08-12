// Tests for the MCP-over-SSE transport against a simulated server.

package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// sseTestServer simulates a classic MCP-over-SSE endpoint: a GET stream
// that announces a POST endpoint and carries `message` frames, plus a POST
// handler that acks with 202 and (for requests with an id) echoes the
// response back onto the stream.
type sseTestServer struct {
	srv    *httptest.Server
	url    string
	stream chan string
	posts  chan string

	mu     sync.Mutex
	closed bool
	gotGET chan struct{}
}

func newSSETestServer(t *testing.T, announceRelative bool) *sseTestServer {
	t.Helper()
	s := &sseTestServer{
		stream: make(chan string, 64),
		posts:  make(chan string, 64),
		gotGET: make(chan struct{}),
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			select {
			case <-s.gotGET:
			default:
				close(s.gotGET)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set(httpHeaderSession, "sess-1")
			fl, _ := w.(http.Flusher)
			if announceRelative {
				_, _ = fmt.Fprintf(w, "event: endpoint\ndata: /messages\n\n")
			} else {
				_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/messages\n\n", s.srv.URL)
			}
			if fl != nil {
				fl.Flush()
			}
			for frame := range s.stream {
				_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", frame)
				if fl != nil {
					fl.Flush()
				}
			}
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.posts <- string(body)
		w.WriteHeader(http.StatusAccepted)
		var req map[string]any
		if json.Unmarshal(body, &req) == nil {
			if id, ok := req["id"]; ok {
				s.push(fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":{"ok":true}}`, frameID(id)))
			}
		}
	}))
	s.url = s.srv.URL + "/sse"
	return s
}

// push queues an SSE `message` frame for the GET stream. No-op after close.
func (s *sseTestServer) push(frame string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.stream <- frame
}

func (s *sseTestServer) close() {
	s.mu.Lock()
	s.closed = true
	close(s.stream)
	s.mu.Unlock()
	s.srv.Close()
}

// connectTransport wires a fresh SSETransport and waits for the GET stream
// to be established so pushed frames are guaranteed delivery.
func connectTransport(t *testing.T, s *sseTestServer) *SSETransport {
	t.Helper()
	tp := &SSETransport{URL: s.url}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tp.Close() })
	select {
	case <-s.gotGET:
	case <-time.After(3 * time.Second):
		t.Fatal("GET stream not established")
	}
	return tp
}

func TestSSE_RequestResponse_RoutedByID(t *testing.T) {
	s := newSSETestServer(t, false)
	defer s.close()
	tp := connectTransport(t, s)

	// Send posts and ack 202; the response is routed back via the GET
	// stream `message` event matching the request id.
	err := tp.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	frame, err := tp.Recv(context.Background())
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	var resp map[string]any
	if json.Unmarshal(frame.Body, &resp) != nil {
		t.Fatalf("bad response json: %s", frame.Body)
	}
	if resp["id"] != float64(1) {
		t.Fatalf("response id = %v, want 1", resp["id"])
	}
	if _, ok := resp["result"]; !ok {
		t.Fatalf("response missing result: %s", frame.Body)
	}
}

func TestSSE_NotificationToInbound(t *testing.T) {
	s := newSSETestServer(t, false)
	defer s.close()
	tp := connectTransport(t, s)

	go s.push(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`)
	select {
	case f := <-tp.Inbound():
		var raw map[string]any
		if json.Unmarshal(f.Body, &raw) != nil {
			t.Fatalf("bad inbound json: %s", f.Body)
		}
		if raw["method"] != "notifications/tools/list_changed" {
			t.Fatalf("method = %v", raw["method"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("notification never arrived")
	}
}

func TestSSE_ServerRequestToInboundAndReply(t *testing.T) {
	s := newSSETestServer(t, false)
	defer s.close()
	tp := connectTransport(t, s)

	// A server-initiated request (id not pending) goes to the inbound
	// channel; the Client answers it with SendRaw.
	go s.push(`{"jsonrpc":"2.0","id":99,"method":"ping"}`)
	var req map[string]any
	select {
	case f := <-tp.Inbound():
		if json.Unmarshal(f.Body, &req) != nil {
			t.Fatalf("bad inbound json: %s", f.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server request never arrived")
	}
	if req["method"] != "ping" {
		t.Fatalf("method = %v", req["method"])
	}

	if err := tp.SendRaw([]byte(`{"jsonrpc":"2.0","id":99,"result":{}}`)); err != nil {
		t.Fatalf("SendRaw: %v", err)
	}
	select {
	case post := <-s.posts:
		var out map[string]any
		if json.Unmarshal([]byte(post), &out) != nil {
			t.Fatalf("bad reply json: %s", post)
		}
		if out["id"] != float64(99) {
			t.Fatalf("reply id = %v, want 99", out["id"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reply never posted")
	}
}

func TestSSE_RelativeEndpointResolution(t *testing.T) {
	tp := &SSETransport{URL: "http://example.com/sse"}
	base, _ := url.Parse("http://example.com/sse")
	tp.setEndpoint("/messages", base)
	tp.mu.Lock()
	ep := tp.endpoint
	tp.mu.Unlock()
	if ep == nil || ep.String() != "http://example.com/messages" {
		t.Fatalf("endpoint = %v, want http://example.com/messages", ep)
	}
}

func TestSSE_CrossOriginEndpointRejected(t *testing.T) {
	tp := &SSETransport{URL: "http://example.com/sse"}
	base, _ := url.Parse("http://example.com/sse")
	tp.setEndpoint("http://evil.com/messages", base)
	tp.mu.Lock()
	ep := tp.endpoint
	tp.mu.Unlock()
	if ep != nil {
		t.Fatalf("cross-origin endpoint accepted: %v", ep)
	}
}
