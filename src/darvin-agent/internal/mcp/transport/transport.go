// Package transport provides the byte-stream layer underneath the MCP JSON-RPC
// client. StdioTransport speaks newline-delimited JSON (with Content-Length
// fallback) using a reader goroutine and per-request-ID pending channels.
// HTTPTransport speaks a single POST per request and caches the response so
// the synchronous client can read it back via Recv.
package transport

import (
	"context"
	"errors"
)

// ErrTransportClosed is returned by Send / Recv once the underlying byte
// stream has been shut down (subprocess exited, http client closed, etc.).
var ErrTransportClosed = errors.New("mcp transport closed")

// Frame is one wire-level MCP message — the JSON-RPC envelope has already
// been unwrapped from its Content-Length (stdio) or HTTP (http) framing.
// Err is set by the stdio reader goroutine to signal fatal read errors
// to pending request goroutines.
type Frame struct {
	Body []byte
	Err  error
}

// Transport abstracts the byte stream that carries MCP messages. Both
// stdio and http satisfy it; the client only depends on the interface so
// tests can plug in a fake.
type Transport interface {
	// Connect prepares the underlying stream (spawns the child process for
	// stdio, builds the http.Client for http). It does not exchange any
	// MCP message.
	Connect(ctx context.Context) error

	// Send writes one frame's body. Stdio frames it with Content-Length
	// headers; HTTP posts the body as JSON. Returns ErrTransportClosed if
	// the stream is no longer alive.
	Send(ctx context.Context, body []byte) error

	// Recv blocks for the next frame's body. For stdio this reads the next
	// framed message; for http it returns the response cached by the most
	// recent Send. Returns io.EOF / ErrTransportClosed when the stream is
	// exhausted.
	Recv(ctx context.Context) (Frame, error)

	// Close tears down the stream. Idempotent; safe to call multiple times.
	Close() error

	// Alive reports whether Send / Recv can still make progress.
	Alive() bool
}
