// Package wecom implements the enterprise-WeChat (WeCom) AI Bot connector.
// It keeps a WebSocket connection to the WeCom AI Bot gateway
// (wss://openws.work.weixin.qq.com), authenticates with botId+secret, long-
// polls aibot_msg_callback frames for inbound messages, and pushes replies
// back over aibot_send_msg. The wire format mirrors Tencent's
// @wecom/aibot-node-sdk + @wecom/wecom-openclaw-plugin.
package wecom

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"darvin-cowork/backend/internal/im"
)

// wsURL is the fixed AI Bot WebSocket gateway.
const wsURL = "wss://openws.work.weixin.qq.com"

// WS command names used by the AI Bot gateway (mirrors the official SDK.
// commands.go).
const (
	cmdSubscribe = "aibot_subscribe"
	cmdHeartbeat = "ping"
	cmdSendMsg   = "aibot_send_msg"
	cmdCallback  = "aibot_msg_callback"
)

// Timing constants for the WS lifecycle.
const (
	heartbeatInterval = 30 * time.Second
	sendAckTimeout    = 15 * time.Second
	dialTimeout       = 10 * time.Second
	// probeTimeout bounds a one-shot Probe session (handshake + auth ack);
	// waitAuth uses its own WS read deadline, so this is a belt-and-braces cap.
	probeTimeout = 20 * time.Second
	// readDeadline is how long to wait for any frame (incl. heartbeat pong)
	// before considering the connection dead.
	readDeadline = 3 * heartbeatInterval
)

// Config is the persisted credential payload for a wecom instance.
type Config struct {
	BotID  string `json:"botId"`
	Secret string `json:"secret"`
}

// Connector is one live wecom AI Bot instance. It owns the WS connection,
// the inbound read loop, and the outbound active-send path.
type Connector struct {
	*im.Base
	cfg    Config
	log    *zap.Logger
	client *websocket.Dialer

	mu      sync.Mutex
	stop    chan struct{}
	started bool
	conn    *websocket.Conn
	// pending maps an outgoing req_id to the channel that resolves once the
	// gateway acks it. Populated by Send, drained by the read loop.
	pending map[string]chan wsAck
}

// wsAck is the gateway's response to an auth/heartbeat/send frame.
type wsAck struct {
	errcode int
	errmsg  string
}

// errNotConnected reports a send attempted while the gateway socket is down.
var errNotConnected = errors.New("wecom: gateway not connected")

// NewConnector builds a wecom connector from an instance seed.
func NewConnector(ctx context.Context, seed im.InstanceSeed) (im.Instance, error) {
	var cfg Config
	if err := seed.DecodeConfig(&cfg); err != nil {
		return nil, fmt.Errorf("wecom: bad config: %w", err)
	}
	if cfg.BotID == "" || cfg.Secret == "" {
		return nil, errors.New("wecom: botId and secret are required")
	}
	c := &Connector{
		Base:    im.NewBase(im.ChannelWecom, seed.InstanceID),
		cfg:     cfg,
		log:     seed.Logger,
		client:  &websocket.Dialer{HandshakeTimeout: dialTimeout},
		stop:    make(chan struct{}),
		pending: make(map[string]chan wsAck),
	}
	return c, nil
}

// ID returns the instance id.
func (c *Connector) ID() string { return c.Base.InstanceID }

// Probe runs a one-shot authentication against the gateway: dial, send
// aibot_subscribe, wait for the auth ack, then close. waitAuth enforces its
// own read deadline, and probeCtx caps the whole session (waitAuth does not
// observe ctx directly).
func (c *Connector) Probe(ctx context.Context) ([]im.Check, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	conn, _, err := (&websocket.Dialer{HandshakeTimeout: dialTimeout}).DialContext(probeCtx, wsURL, nil)
	if err != nil {
		return []im.Check{{
			Code: "auth_ok", Title: "Gateway auth", Level: "fail", Detail: err.Error(),
		}}, err
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"cmd":     cmdSubscribe,
		"headers": map[string]string{"req_id": newReqID(cmdSubscribe)},
		"body":    map[string]any{"bot_id": c.cfg.BotID, "secret": c.cfg.Secret},
	}); err != nil {
		return []im.Check{{
			Code: "auth_ok", Title: "Gateway auth", Level: "fail", Detail: err.Error(),
		}}, err
	}

	authed, ack := c.waitAuth(conn)
	if !authed {
		return []im.Check{{
			Code: "auth_ok", Title: "Gateway auth", Level: "fail",
			Detail: fmt.Sprintf("errcode=%d errmsg=%s", ack.errcode, ack.errmsg),
		}}, nil
	}
	return []im.Check{{Code: "auth_ok", Title: "Gateway auth", Level: "pass"}}, nil
}

// Start opens the gateway connection and launches the read/heartbeat loop.
func (c *Connector) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.mu.Unlock()
	c.MarkConnected()
	go c.wsLoop()
	return nil
}

// Stop tears down the gateway connection and halts the loop.
func (c *Connector) Stop(ctx context.Context) error {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = false
	close(c.stop)
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil {
		// Close the socket so a blocked ReadMessage returns promptly.
		_ = conn.Close()
	}
	return nil
}

// Status reports the live connection state.
func (c *Connector) Status() im.Status {
	c.mu.Lock()
	started := c.started
	c.mu.Unlock()
	state := im.StateStopped
	if started {
		state = im.StateConnected
	}
	b := c.Base
	return im.Status{
		Channel:    im.ChannelWecom,
		InstanceID: b.InstanceID,
		State:      state,
		LastError:  b.LastError(),
		StartedAt:  b.StartedAt(),
		SentCount:  b.SentCount(),
		RecvCount:  b.RecvCount(),
	}
}

// Send pushes an outbound reply through the active aibot_send_msg channel.
// The gateway only supports markdown/template_card/media for active sends, so
// text goes as markdown — the same body the official plugin uses for event-
// driven replies.
func (c *Connector) Send(ctx context.Context, to im.Target, msg im.Outbound) error {
	conn := c.current()
	if conn == nil {
		return errNotConnected
	}
	reqID := newReqID(cmdSendMsg)
	frame := map[string]any{
		"cmd":     cmdSendMsg,
		"headers": map[string]string{"req_id": reqID},
		"body": map[string]any{
			"chatid":   to.PeerID,
			"msgtype":  "markdown",
			"markdown": map[string]string{"content": msg.Text},
		},
	}
	ack := make(chan wsAck, 1)
	c.mu.Lock()
	c.pending[reqID] = ack
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
	}()

	if err := c.write(conn, frame); err != nil {
		c.MarkError(err)
		return err
	}
	select {
	case a := <-ack:
		if a.errcode != 0 {
			rej := fmt.Errorf("wecom: send rejected errcode=%d errmsg=%s", a.errcode, a.errmsg)
			c.MarkError(rej)
			return rej
		}
		c.MarkSent()
		c.MarkError(nil)
		return nil
	case <-time.After(sendAckTimeout):
		to := errors.New("wecom: send ack timeout")
		c.MarkError(to)
		return to
	}
}

// wsLoop reconnects the gateway socket with exponential backoff until Stop.
func (c *Connector) wsLoop() {
	backoff := time.Second
	for {
		select {
		case <-c.stop:
			return
		default:
		}
		fatal, err := c.runOnce()
		if err != nil {
			c.MarkError(err)
		}
		if fatal {
			return
		}
		select {
		case <-c.stop:
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// runOnce performs one connect → authenticate → read loop session.
func (c *Connector) runOnce() (fatal bool, err error) {
	conn, _, err := c.client.Dial(wsURL, nil)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	c.setConn(conn)

	if err := c.write(conn, map[string]any{
		"cmd":     cmdSubscribe,
		"headers": map[string]string{"req_id": newReqID(cmdSubscribe)},
		"body":    map[string]any{"bot_id": c.cfg.BotID, "secret": c.cfg.Secret},
	}); err != nil {
		return false, err
	}

	// Wait for the auth ack before starting the read loop.
	if authed, ack := c.waitAuth(conn); !authed {
		return true, fmt.Errorf("wecom: auth failed errcode=%d errmsg=%s", ack.errcode, ack.errmsg)
	}
	c.MarkConnected()

	hb := time.NewTicker(heartbeatInterval)
	defer hb.Stop()
	for {
		select {
		case <-c.stop:
			return false, nil
		case <-hb.C:
			if err := c.write(conn, map[string]any{
				"cmd":     cmdHeartbeat,
				"headers": map[string]string{"req_id": newReqID(cmdHeartbeat)},
			}); err != nil {
				return false, err
			}
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(readDeadline))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return false, err
		}
		c.handleFrame(data)
	}
}

// waitAuth reads frames until the auth response (req_id prefix aibot_subscribe)
// or a failure arrives.
func (c *Connector) waitAuth(conn *websocket.Conn) (authed bool, ack wsAck) {
	for {
		_ = conn.SetReadDeadline(time.Now().Add(dialTimeout))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return false, wsAck{errcode: -1, errmsg: err.Error()}
		}
		var f wsFrame
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		if hasPrefix(f.Headers.ReqID, cmdSubscribe) {
			return f.Errcode == 0, wsAck{errcode: f.Errcode, errmsg: f.Errmsg}
		}
	}
}

// handleFrame routes a received gateway frame: message callbacks become
// inbound messages, and req_id-matched responses resolve pending sends.
func (c *Connector) handleFrame(data []byte) {
	var f wsFrame
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	reqID := f.Headers.ReqID
	// Ack responses (auth/heartbeat/send) carry errcode and no cmd.
	if f.Cmd == "" {
		c.resolveAck(reqID, wsAck{errcode: f.Errcode, errmsg: f.Errmsg})
		return
	}
	if f.Cmd == cmdCallback {
		c.dispatchCallback(f.Body)
	}
}

// resolveAck delivers an ack to a pending Send, if any.
func (c *Connector) resolveAck(reqID string, ack wsAck) {
	c.mu.Lock()
	ch, ok := c.pending[reqID]
	c.mu.Unlock()
	if ok {
		select {
		case ch <- ack:
		default:
		}
	}
}

// dispatchCallback normalizes an aibot_msg_callback body into an inbound
// message. Only text messages are forwarded; event callbacks are ignored.
func (c *Connector) dispatchCallback(body json.RawMessage) {
	var m struct {
		MsgID    string `json:"msgid"`
		ChatID   string `json:"chatid"`
		ChatType string `json:"chattype"` // single | group
		From     struct {
			UserID string `json:"userid"`
		} `json:"from"`
		MsgType string `json:"msgtype"`
		Text    struct {
			Content string `json:"content"`
		} `json:"text"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return
	}
	if m.MsgType != "text" || m.Text.Content == "" {
		return
	}
	peerID := m.ChatID
	peerKind := im.PeerGroup
	if m.ChatType == "single" {
		peerKind = im.PeerDirect
		if m.From.UserID != "" {
			peerID = m.From.UserID
		}
	}
	c.Inbound(im.InboundMessage{
		AccountID:  c.cfg.BotID,
		PeerKind:   peerKind,
		PeerID:     peerID,
		SenderID:   m.From.UserID,
		SenderName: "",
		Content:    m.Text.Content,
		ReceivedAt: time.Now(),
	})
}

// wsFrame is the on-the-wire gateway envelope.
type wsFrame struct {
	Cmd     string `json:"cmd"`
	Headers struct {
		ReqID string `json:"req_id"`
	} `json:"headers"`
	Body    json.RawMessage `json:"body"`
	Errcode int             `json:"errcode"`
	Errmsg  string          `json:"errmsg"`
}

// write sends a JSON frame on the WS, serializing concurrent senders.
func (c *Connector) write(conn *websocket.Conn, frame any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return conn.WriteJSON(frame)
}

func (c *Connector) current() *websocket.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

func (c *Connector) setConn(conn *websocket.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = conn
}

// newReqID returns a fresh per-frame request id shaped like the official SDK:
// <prefix>_<unixms>_<8 hex>.
func newReqID(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
