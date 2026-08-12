// Registry-level integration test: an SSE server registered through the
// registry goes through the full connectServer path (resolve → transport →
// initialize → list tools) and lands in a connected state with tools.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// sseRegistryServer simulates a classic MCP-over-SSE endpoint that answers
// initialize / tools/list / resources/list / prompts/list. Requests are
// acked with 202; responses are pushed back as `message` events on the GET
// stream.
func sseRegistryServer(t *testing.T) *httptest.Server {
	t.Helper()
	stream := make(chan string, 64)
	var mu sync.Mutex
	closed := false
	push := func(frame string) {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			stream <- frame
		}
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Mcp-Session-Id", "sess")
			fl, _ := w.(http.Flusher)
			_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/messages\n\n", srv.URL)
			if fl != nil {
				fl.Flush()
			}
			for f := range stream {
				_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", f)
				if fl != nil {
					fl.Flush()
				}
			}
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		w.WriteHeader(http.StatusAccepted)
		id, hasID := req["id"]
		if !hasID {
			return
		}
		push(sseRegistryResponse(req))
		_ = id
	}))
	t.Cleanup(func() {
		mu.Lock()
		closed = true
		close(stream)
		mu.Unlock()
		srv.Close()
	})
	return srv
}

func sseRegistryResponse(req map[string]any) string {
	method, _ := req["method"].(string)
	var id float64
	switch v := req["id"].(type) {
	case float64:
		id = v
	}
	var result any
	switch method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "probe", "version": "1"},
		}
	case "tools/list":
		result = map[string]any{"tools": []map[string]any{{"name": "echo", "description": "echo", "inputSchema": map[string]any{"type": "object"}}}}
	case "resources/list":
		result = map[string]any{"resources": []map[string]any{{"uri": "greeting://welcome", "name": "welcome"}}}
	case "prompts/list":
		result = map[string]any{"prompts": []map[string]any{{"name": "greet", "description": "greet"}}}
	default:
		result = nil
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	return string(b)
}

func TestRegistry_SSE_ConnectsAndListsTools(t *testing.T) {
	srv := sseRegistryServer(t)
	reg := NewRegistry(NewResolverManager(t.TempDir()), NewInMemoryResolutionPersistence())

	if err := reg.Register(context.Background(), ServerSpec{
		ID: "sse", Name: "sse-probe", Enabled: true, Transport: TransportSSE,
		URL: srv.URL + "/sse",
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := reg.Get("sse")
		if st.Connected {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, ok := reg.Get("sse")
	if !ok {
		t.Fatal("server not registered")
	}
	if !st.Connected {
		t.Fatalf("not connected: %s", st.ConnectionError)
	}
	if len(st.Tools) != 1 || st.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want [echo]", st.Tools)
	}

	// refreshCapabilities fills resources/prompts asynchronously after
	// connect; this also guards the SSE transport's life context surviving
	// connectServer's return.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st, _ = reg.Get("sse")
		if len(st.Resources) > 0 && len(st.Prompts) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(st.Resources) != 1 || st.Resources[0].URI != "greeting://welcome" {
		t.Fatalf("resources = %+v, want [greeting://welcome]", st.Resources)
	}
	if len(st.Prompts) != 1 || st.Prompts[0].Name != "greet" {
		t.Fatalf("prompts = %+v, want [greet]", st.Prompts)
	}
}
