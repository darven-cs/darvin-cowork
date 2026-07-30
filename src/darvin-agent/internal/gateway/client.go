package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// errClosed is returned from writeJSON / writeControl when the conn
// pointer has been cleared by run's defer Close. It is a sentinel —
// callers log at debug and continue.
var errClosed = errors.New("client: connection closed")

// client represents one open WebSocket connection. All shared mutable
// state (the write pump, the in-flight response counter) lives behind
// writeMu so handlers and the read loop can both call SendNotification
// without serialising at a higher level.
type client struct {
	conn     *websocket.Conn
	sessions *SessionManager
	ledger   *EventLedger
	log      *zap.Logger

	// writeMu guards ws writes. The gorilla/websocket package forbids
	// concurrent writers on a single connection — without this mutex a
	// handler reply and a notification could interleave and corrupt the
	// frame.
	writeMu sync.Mutex
}

// writeJSON serialises payload and writes it as a single text frame.
// Centralised so the read loop and SendNotification share the same lock.
//
// A nil conn is treated as a closed connection: callers that ignore
// the error (notably SendNotification, which only logs) won't panic
// when EmitStub's goroutine fires after the read loop has already torn
// down the connection.
func (c *client) writeJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errClosed
	}
	return c.conn.WriteJSON(v)
}

// writeControl sends a control frame (ping/pong/close). It is safe to
// call concurrently with writeJSON; the mutex serialises them.
func (c *client) writeControl(msgType int, payload []byte, deadline time.Time) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errClosed
	}
	return c.conn.WriteControl(msgType, payload, deadline)
}

// SendNotification is called by EventLedger.publishLocked to push a
// server-initiated event to the renderer. Errors here are logged and
// swallowed — the next read error from the peer will trigger the
// deferred UnsubscribeAll.
func (c *client) SendNotification(method string, params any) {
	if err := c.writeJSON(newNotification(method, params)); err != nil {
		c.log.Debug("notification write failed", zap.Error(err))
	}
}

// run is the connection's lifetime loop. It owns:
//   - read pump: decode JSON-RPC frames, dispatch to handlers, write back responses
//   - ping ticker: 30s pings to keep middleboxes / NAT happy
//   - pong handler: deadline reset on every pong frame
//   - shutdown: defer unsubscribe + close
//
// The read goroutine and the ping ticker share the same connection, so
// WriteControl (used for ping) must be serialised against WriteJSON
// (used for responses / notifications) — that's the writeMu contract.
func (c *client) run(ctx context.Context) {
	// Set an initial read deadline; every pong refreshes it.
	_ = c.conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		return nil
	})

	pingDone := make(chan struct{})
	defer close(pingDone)
	go c.pingLoop(ctx, pingDone)

	defer func() {
		c.ledger.UnsubscribeAll(c)
		// Clear the conn pointer before Close so any goroutine still
		// holding *client (EmitStub's background write) short-circuits
		// in writeJSON rather than calling into a half-closed *Conn.
		conn := c.conn
		c.conn = nil
		_ = conn.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				c.log.Debug("client closed cleanly")
			} else {
				c.log.Debug("client read error", zap.Error(err))
			}
			return
		}

		reqs, batch, err := parseFrame(data)
		if err != nil {
			// Parse error → reply with id=null per JSON-RPC 2.0 §5.
			_ = c.writeJSON(errorResp(nullID, CodeParseError, "parse error", nil))
			continue
		}
		if len(reqs) == 0 {
			continue
		}

		// A notification is a request with no id — no response. Otherwise
		// we collect responses into a slice; if the inbound was a batch
		// we reply as a JSON array, else a single object.
		responses := make([]*Response, 0, len(reqs))
		for _, req := range reqs {
			resp := dispatchRequest(ctx, req, c)
			if resp == nil {
				continue
			}
			responses = append(responses, resp)
		}

		if len(responses) == 0 {
			continue
		}
		if batch {
			if err := c.writeJSON(responses); err != nil {
				c.log.Debug("batch write failed", zap.Error(err))
				return
			}
		} else {
			if err := c.writeJSON(responses[0]); err != nil {
				c.log.Debug("response write failed", zap.Error(err))
				return
			}
		}
	}
}

// pingLoop emits a ping every 30s and exits when the connection finishes
// (pingDone is closed by the defer in run) or when ctx is cancelled.
func (c *client) pingLoop(ctx context.Context, done <-chan struct{}) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			deadline := time.Now().Add(5 * time.Second)
			if err := c.writeControl(websocket.PingMessage, nil, deadline); err != nil {
				// Peer hung up; the read loop will notice next.
				return
			}
		}
	}
}

// marshalIDLossy is a tiny helper for the response id: the gorilla
// decoder hands us a json.RawMessage that may carry a string or number;
// marshalling it back through any loses the original shape. Tests that
// need a faithful round-trip should assert on the wire bytes directly.
func marshalIDLossy(id json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), id...)
}
