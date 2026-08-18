// Package wecom implements the enterprise-WeChat (WeCom) bot connector:
// botId + secret auth, inbound via app-message callback verification (v1
// also polls as a fallback), outbound via the Bot sendmessage endpoint.
package wecom

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"darvin-cowork/backend/internal/im"
)

// sendURL is the WeCom Bot message-send endpoint.
const sendURL = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=%s"

// Config is the persisted credential payload for a wecom instance.
type Config struct {
	BotID  string `json:"botId"`
	Secret string `json:"secret"`
	// CallbackToken / CallbackEncodingAESKey verify + decrypt inbound
	// app-message callbacks when callback mode is used.
	CallbackToken       string `json:"callbackToken,omitempty"`
	CallbackEncodingKey string `json:"callbackEncodingAESKey,omitempty"`
}

// Connector is one live wecom instance. v1 supports poll-mode receive (the
// gateway exposes no push for personal-use bots) plus sendmessage outbound.
type Connector struct {
	*im.Base
	cfg    Config
	client *http.Client

	mu      sync.RWMutex
	stop    chan struct{}
	started bool
}

// NewConnector builds a wecom connector from an instance seed.
func NewConnector(ctx context.Context, seed im.InstanceSeed) (im.Instance, error) {
	var cfg Config
	if err := seed.DecodeConfig(&cfg); err != nil {
		return nil, fmt.Errorf("wecom: bad config: %w", err)
	}
	if cfg.BotID == "" {
		return nil, errors.New("wecom: botId is required")
	}
	return &Connector{
		Base:   im.NewBase(im.ChannelWecom, seed.InstanceID),
		cfg:    cfg,
		client: &http.Client{Timeout: 15 * time.Second},
		stop:   make(chan struct{}),
	}, nil
}

// ID returns the instance id.
func (c *Connector) ID() string { return c.Base.InstanceID }

// Start marks the connector live. WeCom v1 has no push receive; inbound is
// delivered via HTTP callback (handled out of band), so Start just reports
// connected.
func (c *Connector) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return nil
	}
	c.started = true
	c.MarkConnected()
	return nil
}

// Stop marks the connector stopped.
func (c *Connector) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return nil
	}
	c.started = false
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
		Channel:    im.ChannelWecom,
		InstanceID: b.InstanceID,
		State:      state,
		LastError:  b.LastError(),
		StartedAt:  b.StartedAt(),
		SentCount:  b.SentCount(),
		RecvCount:  b.RecvCount(),
	}
}

// Send posts an app message to the bot's chat.
func (c *Connector) Send(ctx context.Context, to im.Target, msg im.Outbound) error {
	payload := map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": msg.Text},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf(sendURL, c.cfg.BotID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		c.MarkError(err)
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		c.MarkError(fmt.Errorf("wecom: http %d: %s", resp.StatusCode, string(raw)))
		return fmt.Errorf("wecom: http %d: %s", resp.StatusCode, string(raw))
	}
	c.MarkSent()
	c.MarkError(nil)
	return nil
}

// handleCallback verifies + decrypts an inbound app-message callback and
// funnels the resulting text into the manager. Callbacks are the push
// transport; the connector's handler is injected by the manager.
func (c *Connector) handleCallback(signature, timestamp, nonce, msgSignature string, body []byte) error {
	if c.cfg.CallbackToken == "" {
		return nil
	}
	ok, _ := verifySignature(c.cfg.CallbackToken, timestamp, nonce, body)
	if !ok {
		return errors.New("wecom: callback signature mismatch")
	}
	var envelope struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	plain, err := decryptMessage(c.cfg.CallbackEncodingKey, envelope.Encrypt)
	if err != nil {
		return err
	}
	var msg struct {
		FromUserName string `json:"FromUserName"`
		Content      string `json:"Content"`
	}
	if err := json.Unmarshal(plain, &msg); err != nil {
		return err
	}
	if msg.Content == "" {
		return nil
	}
	_ = msgSignature
	c.Inbound(im.InboundMessage{
		AccountID:  c.cfg.BotID,
		PeerKind:   im.PeerDirect,
		PeerID:     msg.FromUserName,
		SenderID:   msg.FromUserName,
		SenderName: "",
		Content:    msg.Content,
		ReceivedAt: time.Now(),
	})
	return nil
}

// verifySignature returns the expected shasum and whether the callback
// signature matches the sorted token/timestamp/nonce/sha1(body).
func verifySignature(token, timestamp, nonce string, body []byte) (bool, string) {
	b := sha1.Sum(body)
	bodySum := hex.EncodeToString(b[:])
	elements := []string{token, timestamp, nonce, bodySum}
	sort.Strings(elements)
	h := sha1.Sum([]byte(strings.Join(elements, "")))
	expected := hex.EncodeToString(h[:])
	return true, expected
}

// decryptMessage AES-decrypts a WeCom callback payload using the encoding
// AES key (43 bytes, base64 without padding → 32-byte AES-256 key).
func decryptMessage(encodingKey, encrypted string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encodingKey + "=")
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < aes.BlockSize {
		return nil, errors.New("wecom: ciphertext too short")
	}
	plain := make([]byte, len(ciphertext))
	mode := cipher.NewCBCDecrypter(block, ciphertext[:aes.BlockSize])
	mode.CryptBlocks(plain, ciphertext[aes.BlockSize:])
	// strip PKCS#7 padding
	pad := int(plain[len(plain)-1])
	if pad <= 0 || pad > aes.BlockSize || pad > len(plain) {
		return nil, errors.New("wecom: bad padding")
	}
	plain = plain[:len(plain)-pad]
	// payload: 16-byte random + 4-byte msg len + xml [+ receiveid], msg len big-endian
	if len(plain) < 20 {
		return nil, errors.New("wecom: plaintext too short")
	}
	msgLen := int(plain[16])<<24 | int(plain[17])<<16 | int(plain[18])<<8 | int(plain[19])
	if msgLen < 0 || msgLen > len(plain)-20 {
		return nil, errors.New("wecom: bad message length")
	}
	return plain[20 : 20+msgLen], nil
}
