// Package weixin implements the personal-WeChat (iLink) connector. The
// transport is a small set of HTTP endpoints against the fixed iLink base
// URL: QR login issues a bot token, getupdates long-polls for inbound
// messages, sendmessage pushes outbound text.
package weixin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/im"
)

// baseURL is the fixed iLink gateway for personal WeChat bots.
const baseURL = "https://ilinkai.weixin.qq.com"

// endpoint paths on the iLink gateway (QR login is GET, messaging POST).
const (
	pathQRCode   = "/ilink/bot/get_bot_qrcode"
	pathQRStatus = "/ilink/bot/get_qrcode_status"
	pathUpdates  = "/ilink/bot/getupdates"
	pathSend     = "/ilink/bot/sendmessage"
	pathTyping   = "/ilink/bot/sendtyping"
	// probeTimeout bounds a one-shot Probe getupdates (3s poll window + slack).
	probeTimeout = 12 * time.Second
)

// Config is the persisted credential payload for a weixin instance.
type Config struct {
	BotToken string `json:"botToken"`
	BotID    string `json:"botId"`
}

// Connector is one live weixin instance.
type Connector struct {
	*im.Base
	cfg    Config
	client *http.Client
	log    *zap.Logger

	mu       sync.RWMutex
	stop     chan struct{}
	started  bool
	cursor   string            // get_updates_buf 长轮询游标（字符串）
	ctxToken map[string]string // peerID -> context_token（BOT 消息必须回显）
}

// errSessionTimeout reports an iLink -14 session timeout; the bot token /
// session is dead and a fresh QR re-login is required.
var errSessionTimeout = errors.New("weixin: session timeout, re-login required")

// weixinMsg is one item from the getupdates msgs envelope.
type weixinMsg struct {
	MessageID    int64             `json:"message_id"`
	FromUserID   string            `json:"from_user_id"`
	ToUserID     string            `json:"to_user_id"`
	ContextToken string            `json:"context_token"`
	MessageType  int               `json:"message_type"`
	ItemList     []json.RawMessage `json:"item_list"`
}

// Text returns the text payload of a message's first textual item.
// iLink item shapes vary: text lives at text_item.text (observed live),
// with fallbacks for text/content at root or nested data.
func (m weixinMsg) Text() string {
	for _, raw := range m.ItemList {
		var it struct {
			Type     int    `json:"type"`
			Text     string `json:"text"`
			Content  string `json:"content"`
			TextItem struct {
				Text string `json:"text"`
			} `json:"text_item"`
			Data struct {
				Text    string `json:"text"`
				Content string `json:"content"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &it) != nil {
			continue
		}
		if it.TextItem.Text != "" {
			return it.TextItem.Text
		}
		if it.Text != "" {
			return it.Text
		}
		if it.Content != "" {
			return it.Content
		}
		if it.Data.Text != "" {
			return it.Data.Text
		}
		if it.Data.Content != "" {
			return it.Data.Content
		}
	}
	return ""
}

// NewConnector builds a weixin connector from an instance seed.
func NewConnector(ctx context.Context, seed im.InstanceSeed) (im.Instance, error) {
	var cfg Config
	if err := seed.DecodeConfig(&cfg); err != nil {
		return nil, fmt.Errorf("weixin: bad config: %w", err)
	}
	return &Connector{
		Base: im.NewBase(im.ChannelWeixin, seed.InstanceID),
		cfg:  cfg,
		log:  seed.Logger,
		// 客户端超时必须大于服务端 getupdates 长轮询窗口（timeout:50），
		// 否则每个周期在 30s 被强杀成 context deadline exceeded，轮询退避拖慢收消息。
		client:   &http.Client{Timeout: 90 * time.Second},
		stop:     make(chan struct{}),
		ctxToken: make(map[string]string),
	}, nil
}

// ID returns the instance id.
func (c *Connector) ID() string { return c.Base.InstanceID }

// Probe runs a one-shot getupdates against the gateway with a short poll
// window (timeout:3) to verify the bot token without blocking. Unlike the
// live pollLoop it does not touch the cursor, cache context tokens, or
// dispatch inbound messages. A missing token fails without any request.
func (c *Connector) Probe(ctx context.Context) ([]im.Check, error) {
	if c.cfg.BotToken == "" {
		return []im.Check{{
			Code: "login_ok", Title: "Bot token", Level: "fail", Detail: "missing token",
		}}, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	resp, err := c.post(probeCtx, pathUpdates, map[string]any{
		"timeout":   3,
		"base_info": map[string]any{"channel_version": "1.0.3"},
	})
	if err != nil {
		return []im.Check{{
			Code: "auth_ok", Title: "Get updates", Level: "fail", Detail: err.Error(),
		}}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return []im.Check{{
			Code: "auth_ok", Title: "Get updates", Level: "fail",
			Detail: fmt.Sprintf("updates http %d", resp.StatusCode),
		}}, nil
	}
	var out struct {
		Ret int `json:"ret"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return []im.Check{{
			Code: "auth_ok", Title: "Get updates", Level: "fail", Detail: err.Error(),
		}}, err
	}
	if out.Ret == -14 {
		return []im.Check{{
			Code: "login_ok", Title: "Session", Level: "fail", Detail: errSessionTimeout.Error(),
		}}, nil
	}
	if out.Ret != 0 {
		return []im.Check{{
			Code: "auth_ok", Title: "Get updates", Level: "fail",
			Detail: fmt.Sprintf("ret=%d", out.Ret),
		}}, nil
	}
	return []im.Check{{Code: "auth_ok", Title: "Get updates", Level: "pass"}}, nil
}

// Start launches the long-poll receive loop.
func (c *Connector) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.mu.Unlock()

	if c.cfg.BotToken == "" {
		c.MarkError(errors.New("weixin: missing bot token"))
		return errors.New("weixin: missing bot token (scan to log in)")
	}
	c.MarkConnected()
	go c.pollLoop()
	return nil
}

// Stop halts the receive loop.
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
		Channel:    im.ChannelWeixin,
		InstanceID: b.InstanceID,
		State:      state,
		LastError:  b.LastError(),
		StartedAt:  b.StartedAt(),
		SentCount:  b.SentCount(),
		RecvCount:  b.RecvCount(),
	}
}

// Send pushes a text reply back to a peer. The msg envelope must carry the
// bot's message_type/state and a freshly generated client_id (not the
// inbound peer's), and the text lives inside text_item — mirroring Tencent's
// openclaw-weixin plugin wire format, otherwise iLink accepts the message
// but never routes it to the peer's WeChat window.
func (c *Connector) Send(ctx context.Context, to im.Target, msg im.Outbound) error {
	c.mu.RLock()
	ct := c.ctxToken[to.PeerID]
	c.mu.RUnlock()
	payload := map[string]any{
		"base_info": map[string]any{"channel_version": "1.0.3"},
		"msg": map[string]any{
			"from_user_id":  "",
			"to_user_id":    to.PeerID,
			"client_id":     newClientID(),
			"message_type":  2, // BOT
			"message_state": 2, // FINISH
			"context_token": ct,
			"item_list":     []any{map[string]any{"type": 1, "text_item": map[string]any{"text": msg.Text}}},
		},
	}
	resp, err := c.post(ctx, pathSend, payload)
	if err != nil {
		c.MarkError(err)
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Ret  int    `json:"ret"`
		Msg  string `json:"message"`
		Desc string `json:"desc"`
	}
	_ = json.Unmarshal(body, &out)
	if c.log != nil {
		c.log.Info("weixin send done",
			zap.String("instance_id", c.Base.InstanceID),
			zap.String("peer", to.PeerID),
			zap.Bool("has_ctx_token", ct != ""),
			zap.Int("ret", out.Ret),
			zap.String("resp", string(body)),
		)
	}
	if out.Ret == -14 {
		c.MarkError(errSessionTimeout)
		return errSessionTimeout
	}
	if out.Ret != 0 {
		// 非 0 ret 即 iLink 拒投（context_token 失效 / to_user_id 非法等），
		// 记录下来避免被误判为成功。
		rej := fmt.Errorf("weixin: sendmessage rejected ret=%d resp=%s", out.Ret, string(body))
		c.MarkError(rej)
		return rej
	}
	c.MarkSent()
	c.MarkError(nil)
	return nil
}

// newClientID returns a fresh per-message client id in the shape the iLink
// plugin uses (prefix:timestamp-hex), one per outbound message.
func newClientID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("darvin-weixin:%d-%x", time.Now().UnixMilli(), b)
}

// pollLoop long-polls getupdates and funnels inbound messages to the
// manager handler. Session timeouts (-14) clear local cursor + cached
// context_tokens (server side needs a re-association) and keep polling.
func (c *Connector) pollLoop() {
	backoff := time.Second
	for {
		select {
		case <-c.stop:
			return
		default:
		}
		// context 必须比服务端 getupdates 长轮询窗口（timeout:50）留有足够余量，
		// 否则偶发阻塞超 60s 会被强杀成 deadline exceeded，连接器持续显示 error。
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
		_, err := c.fetchUpdates(ctx)
		cancel()
		if err != nil {
			c.MarkError(err)
			if errors.Is(err, errSessionTimeout) {
				// iLink 会话需要重连：清掉本地游标和 context_token 缓存，
				// 下一次 poll 用空 cursor 重新建立上下文，不停止连接器。
				c.mu.Lock()
				c.cursor = ""
				c.ctxToken = make(map[string]string)
				c.mu.Unlock()
				backoff = time.Second
				continue
			}
			time.Sleep(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		// 成功轮询清除上次的瞬时错误，连接器稳定显示 connected。
		c.MarkError(nil)
		backoff = time.Second
	}
}

// fetchUpdates pulls one getupdates batch and dispatches each message.
func (c *Connector) fetchUpdates(ctx context.Context) (bool, error) {
	payload := map[string]any{
		"timeout":   50,
		"base_info": map[string]any{"channel_version": "1.0.3"},
	}
	c.mu.RLock()
	cursor := c.cursor
	c.mu.RUnlock()
	if cursor != "" {
		payload["get_updates_buf"] = cursor
	}
	resp, err := c.post(ctx, pathUpdates, payload)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("weixin: updates http %d", resp.StatusCode)
	}
	var out struct {
		Ret    int         `json:"ret"`
		Msgs   []weixinMsg `json:"msgs"`
		Cursor string      `json:"get_updates_buf"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false, err
	}
	if out.Ret == -14 {
		// iLink 会话超时：token 失效，需重新扫码登录
		return false, errSessionTimeout
	}
	if out.Cursor != "" && out.Cursor != cursor {
		c.mu.Lock()
		c.cursor = out.Cursor
		c.mu.Unlock()
	}
	for i := range out.Msgs {
		m := out.Msgs[i]
		if m.FromUserID != "" && m.ContextToken != "" {
			c.mu.Lock()
			c.ctxToken[m.FromUserID] = m.ContextToken
			c.mu.Unlock()
		}
		text := m.Text()
		if text == "" {
			continue
		}
		c.Inbound(im.InboundMessage{
			AccountID:  c.cfg.BotID,
			PeerKind:   im.PeerDirect,
			PeerID:     m.FromUserID,
			SenderID:   m.FromUserID,
			SenderName: "",
			Content:    text,
			ReceivedAt: time.Now(),
		})
	}
	return len(out.Msgs) > 0, nil
}

func (c *Connector) post(ctx context.Context, path string, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.BotToken != "" {
		req.Header.Set("AuthorizationType", "ilink_bot_token")
		req.Header.Set("Authorization", "Bearer "+c.cfg.BotToken)
	}
	// 每次请求随机（与腾讯 openclaw-weixin 一样）；UIN 只用于请求序列关联，不需跨请求稳定。
	req.Header.Set("X-WECHAT-UIN", base64.StdEncoding.EncodeToString([]byte(wechatUIN())))
	return c.client.Do(req)
}

// wechatUIN returns a stable-ish numeric random string for X-WECHAT-UIN.
func wechatUIN() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%08d", uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]))
}

// QRProvider implements im.QRLoginProvider for weixin scan-to-login.
type QRProvider struct {
	client *http.Client
}

// NewQRProvider builds a QR provider against the iLink gateway.
func NewQRProvider() *QRProvider {
	return &QRProvider{client: &http.Client{Timeout: 30 * time.Second}}
}

// Begin issues a fresh QR code (GET with bot_type) and returns its login
// session carrying the QR image URL + the qrcode poll key as its id.
func (p *QRProvider) Begin(channel, instanceID string) (*im.QRLoginSession, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+pathQRCode+"?bot_type=3", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weixin: get_bot_qrcode http %d", resp.StatusCode)
	}
	var out struct {
		QRCode  string `json:"qrcode"`
		QRImage string `json:"qrcode_img_content"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.QRCode == "" || out.QRImage == "" {
		return nil, errors.New("weixin: no qrcode returned")
	}
	return &im.QRLoginSession{
		ID:        out.QRCode,
		Channel:   channel,
		State:     im.QRWaiting,
		QRURL:     out.QRImage,
		ExpiresAt: time.Now().Add(2 * time.Minute),
	}, nil
}

// Poll advances the session state by querying the QR status endpoint (GET,
// long-polling until confirmed / expired).
func (p *QRProvider) Poll(ctx context.Context, s *im.QRLoginSession) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+pathQRStatus+"?qrcode="+s.ID, nil)
	if err != nil {
		return err
	}
	req.Header.Set("iLink-App-ClientVersion", "1")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Status   string `json:"status"`
		BotToken string `json:"bot_token"`
		BotID    string `json:"ilink_bot_id"`
		BaseURL  string `json:"baseurl"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	switch out.Status {
	case "scaned":
		s.State = im.QRScanned
	case "confirmed":
		s.State = im.QRConfirmed
		s.Token = out.BotToken
		s.BotID = out.BotID
	case "expired":
		s.State = im.QRExpired
	}
	return nil
}
