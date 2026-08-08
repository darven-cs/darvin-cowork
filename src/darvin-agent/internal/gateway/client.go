// Per-connection WebSocket client that serialises writes and control frames.

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
// pointer has been cleared by run's defer Close.
var errClosed = errors.New("client: connection closed")

// client represents one open WebSocket connection. All shared mutable
// state lives behind writeMu so handlers and the read loop can both
// call SendNotification without higher-level serialisation.
type client struct {
	conn     *websocket.Conn
	sessions *SessionManager
	ledger   *EventLedger
	handler  *Handler
	log      *zap.Logger

	// writeMu guards ws writes; gorilla/websocket forbids concurrent
	// writers on a single connection.
	writeMu sync.Mutex
}

// writeJSON serialises v and writes it as a single text frame. A nil
// conn is treated as closed so callers that ignore the error (notably
// SendNotification) do not panic when a goroutine fires after the
// connection has been torn down.
func (c *client) writeJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errClosed
	}
	return c.conn.WriteJSON(v)
}

// writeControl sends a control frame (ping / pong / close). Safe to
// call concurrently with writeJSON (mutex serialises them).
func (c *client) writeControl(msgType int, payload []byte, deadline time.Time) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errClosed
	}
	return c.conn.WriteControl(msgType, payload, deadline)
}

// SendNotification pushes a server-initiated event to the renderer.
// Errors are logged and swallowed; the next read error triggers the
// deferred UnsubscribeAll.
func (c *client) SendNotification(method string, params any) {
	if err := c.writeJSON(newNotification(method, params)); err != nil {
		c.log.Debug("notification write failed", zap.Error(err))
	}
}

// run is the connection's lifetime loop. It owns the read pump
// (decode JSON-RPC frames → dispatch → write back), the ping ticker
// (30s pings to keep middleboxes happy), the pong handler (deadline
// reset), and the shutdown defer (unsubscribe + close).
func (c *client) run(ctx context.Context) {
	// Initial read deadline; every pong refreshes it.
	_ = c.conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		return nil
	})

	pingDone := make(chan struct{})
	defer close(pingDone)
	go c.pingLoop(ctx, pingDone)

	c.ledger.RegisterConnection(c)

	defer func() {
		c.ledger.UnsubscribeAll(c)
		// Clear the conn pointer before Close so any goroutine still
		// holding *client short-circuits in writeJSON rather than
		// calling into a half-closed *Conn.
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

		// A notification is a request with no id (no response). Otherwise we
		// collect responses and reply as a JSON array iff the inbound
		// was a batch.
		responses := make([]*Response, 0, len(reqs))
		for _, req := range reqs {
			resp := dispatchRequest(ctx, req, c, c.handler)
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

// pingLoop emits a ping every 30s and exits when the connection
// finishes or ctx is cancelled.
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

// marshalIDLossy copies the response id without re-marshalling so the
// original JSON shape (string vs number) survives the round-trip.
func marshalIDLossy(id json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), id...)
}
