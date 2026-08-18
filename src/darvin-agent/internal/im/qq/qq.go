// Package qq implements the QQ official-bot connector: app access-token
// management, a WS gateway (Discord-style opcodes) for inbound events, and
// REST sends for C2C / group messages.
package qq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"darvin-cowork/backend/internal/im"
)

// API bases and WS endpoints for the QQ open-platform bot.
const (
	tokenURL  = "https://bots.qq.com/app/getAppAccessToken"
	apiBase   = "https://api.sgroup.qq.com"
	gatewayEp = apiBase + "/gateway"
)

// WS opcodes used by the QQ gateway (Discord-style).
const (
	opDispatch     = 0
	opHeartbeat    = 1
	opIdentify     = 2
	opResume       = 6
	opReconnect    = 7
	opHello        = 10
	opHeartbeatAck = 11
)

// Config is the persisted credential payload for a qq instance.
type Config struct {
	AppID       string `json:"appId"`
	AppSecret   string `json:"appSecret"`
	AccessToken string `json:"accessToken,omitempty"`
}

// Connector is one live qq instance: it owns a token manager, a gateway WS
// connection, and the REST send client.
type Connector struct {
	*im.Base
	cfg    Config
	client *http.Client

	mu      sync.RWMutex
	stop    chan struct{}
	started bool
	token   string
	expires time.Time
	refresh chan struct{}
}

// NewConnector builds a qq connector from an instance seed.
func NewConnector(ctx context.Context, seed im.InstanceSeed) (im.Instance, error) {
	var cfg Config
	if err := seed.DecodeConfig(&cfg); err != nil {
		return nil, fmt.Errorf("qq: bad config: %w", err)
	}
	if cfg.AppID == "" || cfg.AppSecret == "" {
		return nil, errors.New("qq: appId and appSecret are required")
	}
	return &Connector{
		Base:    im.NewBase(im.ChannelQQ, seed.InstanceID),
		cfg:     cfg,
		client:  &http.Client{Timeout: 15 * time.Second},
		stop:    make(chan struct{}),
		refresh: make(chan struct{}, 1),
	}, nil
}

// ID returns the instance id.
func (c *Connector) ID() string { return c.Base.InstanceID }

// Probe performs a one-shot connectivity check by exchanging an app access
// token with the QQ open platform. The freshly built connector holds no
// cached token, so this always hits the real endpoint.
func (c *Connector) Probe(ctx context.Context) ([]im.Check, error) {
	if err := c.ensureToken(ctx); err != nil {
		return []im.Check{{
			Code: "auth_ok", Title: "App access token", Level: "fail", Detail: err.Error(),
		}}, err
	}
	return []im.Check{{Code: "auth_ok", Title: "App access token", Level: "pass"}}, nil
}

// Start acquires a token and launches the gateway loop.
func (c *Connector) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.mu.Unlock()

	if err := c.ensureToken(ctx); err != nil {
		c.MarkError(err)
		return err
	}
	c.MarkConnected()
	go c.gatewayLoop()
	return nil
}

// Stop halts the gateway loop.
func (c *Connector) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return nil
	}
	c.started = false
	close(c.stop)
	return nil
}

// Status reports the live connection state.
func (c *Connector) Status() im.Status {
	c.mu.RLock()
	started := c.started
	c.mu.RUnlock()
	state := im.StateStopped
	if started {
		state = im.StateConnected
	}
	b := c.Base
	return im.Status{
		Channel:    im.ChannelQQ,
		InstanceID: b.InstanceID,
		State:      state,
		LastError:  b.LastError(),
		StartedAt:  b.StartedAt(),
		SentCount:  b.SentCount(),
		RecvCount:  b.RecvCount(),
	}
}

// Send posts an outbound message to a C2C or group target.
func (c *Connector) Send(ctx context.Context, to im.Target, msg im.Outbound) error {
	if err := c.ensureToken(ctx); err != nil {
		c.MarkError(err)
		return err
	}
	payload := map[string]any{
		"content":  msg.Text,
		"msg_type": 0,
		"msg_seq":  nextMsgSeq(), // QQ uses msg_seq for idempotency/ordering
	}
	var url string
	switch to.PeerKind {
	case im.PeerGroup:
		url = apiBase + "/v2/groups/" + to.PeerID + "/messages"
	case im.PeerDirect:
		url = apiBase + "/v2/users/" + to.PeerID + "/messages"
	default:
		return fmt.Errorf("qq: unsupported peer kind %q", to.PeerKind)
	}
	if err := c.doJSON(ctx, http.MethodPost, url, payload, nil); err != nil {
		c.MarkError(err)
		return err
	}
	c.MarkSent()
	c.MarkError(nil)
	return nil
}

// nextMsgSeq returns a message sequence number in the 0..65535 range, mirroring
// the QQ official SDK: QQ uses msg_seq for idempotency/ordering, so every
// send must carry a fresh one.
func nextMsgSeq() int {
	timePart := time.Now().UnixMilli() % 100_000_000
	return int((timePart ^ int64(rand.Int31n(65536))) % 65536)
}

// gatewayLoop owns the WS connection: connect → identify → pump events →
// heartbeat, with exponential backoff reconnect. Fatal codes (4014) stop
// the loop rather than retry.
func (c *Connector) gatewayLoop() {
	backoff := time.Second
	for {
		select {
		case <-c.stop:
			return
		default:
		}
		fatal, err := c.runGatewayOnce()
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

// runGatewayOnce performs one connect→pump session. Returns fatal=true for
// non-recoverable codes.
func (c *Connector) runGatewayOnce() (bool, error) {
	c.mu.Lock()
	stop := c.stop
	c.mu.Unlock()

	var gwResp struct {
		URL string `json:"url"`
	}
	if err := c.doJSON(context.Background(), http.MethodGet, gatewayEp, nil, &gwResp, func(req *http.Request) {
		req.Header.Set("Authorization", "QQBot "+c.token)
	}); err != nil {
		return false, err
	}
	if gwResp.URL == "" {
		return false, errors.New("qq: empty gateway url")
	}

	conn, _, err := websocket.DefaultDialer.Dial(gwResp.URL, nil)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	// hello handshake
	var hello struct {
		Op   int `json:"op"`
		Data struct {
			HeartbeatInterval int `json:"heartbeat_interval"`
		} `json:"d"`
	}
	if err := conn.ReadJSON(&hello); err != nil {
		return false, err
	}
	if hello.Op != opHello {
		return false, errors.New("qq: expected hello")
	}

	identify := map[string]any{
		"op": opIdentify,
		"d": map[string]any{
			"token":   "QQBot " + c.token,
			"intents": 0x030, // GROUP_AND_C2C | DIRECT_MESSAGE
			"shard":   []int{0, 1},
		},
	}
	if err := conn.WriteJSON(identify); err != nil {
		return false, err
	}

	hbTicker := time.NewTicker(time.Duration(hello.Data.HeartbeatInterval) * time.Millisecond)
	defer hbTicker.Stop()

	for {
		select {
		case <-stop:
			return false, nil
		case <-hbTicker.C:
			if err := conn.WriteJSON(map[string]any{"op": opHeartbeat}); err != nil {
				return false, err
			}
		default:
		}
		var msg struct {
			Op   int             `json:"op"`
			Data json.RawMessage `json:"d"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			return false, err
		}
		switch msg.Op {
		case opHeartbeatAck:
			c.MarkConnected()
		case opReconnect:
			return false, nil
		case opDispatch:
			c.dispatchEvent(msg.Data)
		}
	}
}

// dispatchEvent classifies a DISPATCH payload into an inbound message.
func (c *Connector) dispatchEvent(raw json.RawMessage) {
	var ev struct {
		Type      string `json:"t"`
		ID        string `json:"id"`
		Timestamp int64  `json:"ts"`
		Data      struct {
			Author struct {
				OpenID string `json:"id"`
			} `json:"author"`
			Content     string `json:"content"`
			GroupOpenID string `json:"group_openid"`
			OpenID      string `json:"openid"`
		} `json:"d"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return
	}
	var peerKind, peerID, senderID, senderName string
	switch ev.Type {
	case "C2C_MESSAGE_CREATE":
		peerKind = im.PeerDirect
		peerID = ev.Data.OpenID
		senderID = ev.Data.Author.OpenID
		senderName = ""
	case "GROUP_AT_MESSAGE_CREATE":
		peerKind = im.PeerGroup
		peerID = ev.Data.GroupOpenID
		senderID = ev.Data.Author.OpenID
		senderName = ""
	default:
		return
	}
	if ev.Data.Content == "" {
		return
	}
	c.Inbound(im.InboundMessage{
		AccountID:  c.cfg.AppID,
		PeerKind:   peerKind,
		PeerID:     peerID,
		SenderID:   senderID,
		SenderName: senderName,
		Content:    ev.Data.Content,
		ReceivedAt: time.Now(),
	})
}

// ensureToken refreshes the app access token when near expiry or absent.
func (c *Connector) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	if c.token != "" && time.Until(c.expires) > time.Minute {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	body, _ := json.Marshal(map[string]any{
		"appId":        c.cfg.AppID,
		"clientSecret": c.cfg.AppSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if out.AccessToken == "" {
		return errors.New("qq: failed to fetch app access token")
	}
	c.mu.Lock()
	c.token = out.AccessToken
	c.expires = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	c.mu.Unlock()
	return nil
}

// doJSON performs a JSON request and decodes the response.
func (c *Connector) doJSON(ctx context.Context, method, url string, payload any, out any, mutate ...func(*http.Request)) error {
	var reader io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "QQBot "+c.token)
	}
	for _, m := range mutate {
		m(req)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("qq: http %d: %s", resp.StatusCode, string(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return err
		}
	}
	return nil
}
