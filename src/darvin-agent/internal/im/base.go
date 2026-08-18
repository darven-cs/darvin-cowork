// Shared connector scaffolding: Base carries the status / inbound / counter
// state every live Instance needs, so the per-channel connectors only
// implement transport-specific start / stop / send.

package im

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// Base is embedded by each connector to provide the common status and
// inbound plumbing. The channel's state transitions + send logic live in
// the embedding type.
type Base struct {
	Channel    string
	InstanceID string

	mu        sync.RWMutex
	handler   InboundHandler
	lastError string
	startedAt int64

	sent int64
	recv int64
}

// NewBase builds the shared connector base.
func NewBase(channel, id string) *Base {
	return &Base{Channel: channel, InstanceID: id}
}

// SetInboundHandler installs the manager's inbound funnel.
func (b *Base) SetInboundHandler(h InboundHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handler = h
}

// Inbound delivers a normalized message to the manager handler, if set.
func (b *Base) Inbound(msg InboundMessage) {
	msg.Channel = b.Channel
	msg.InstanceID = b.InstanceID
	atomic.AddInt64(&b.recv, 1)
	b.mu.RLock()
	h := b.handler
	b.mu.RUnlock()
	if h != nil {
		h(context.Background(), msg)
	}
}

// MarkConnected clears the error and stamps the connect time.
func (b *Base) MarkConnected() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastError = ""
	b.startedAt = time.Now().UnixMilli()
}

// MarkError records the latest transport error.
func (b *Base) MarkError(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.lastError = err.Error()
	}
}

// MarkSent increments the outbound counter.
func (b *Base) MarkSent() { atomic.AddInt64(&b.sent, 1) }

// LastError returns the latest transport error string.
func (b *Base) LastError() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastError
}

// StartedAt returns the last connect timestamp (unix ms).
func (b *Base) StartedAt() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.startedAt
}

// SentCount returns the outbound message count.
func (b *Base) SentCount() int64 { return atomic.LoadInt64(&b.sent) }

// RecvCount returns the inbound message count.
func (b *Base) RecvCount() int64 { return atomic.LoadInt64(&b.recv) }

// DecodeConfig unmarshals the channel-specific credential payload into out.
func (s *InstanceSeed) DecodeConfig(out any) error {
	if len(s.Config) == 0 {
		return nil
	}
	raw, err := json.Marshal(s.Config)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
