// Package im implements the IM-channel subsystem: a pluggable transport
// that connects external IM bots (QQ / WeCom / Weixin) to darvin sessions.
package im

import (
	"context"
	"encoding/json"
	"time"
)

// Channel identifies an IM platform.
const (
	ChannelQQ     = "qq"
	ChannelWecom  = "wecom"
	ChannelWeixin = "weixin"
)

// PeerKind distinguishes a direct message from a group mention.
const (
	PeerDirect = "direct"
	PeerGroup  = "group"
)

// Status is the wire-visible connection state of one instance.
type Status struct {
	Channel    string `json:"channel"`
	InstanceID string `json:"instanceId"`
	Enabled    bool   `json:"enabled"`
	State      string `json:"state"` // connected | connected_waiting | disconnected | error | login_expired | stopped
	LastError  string `json:"lastError,omitempty"`
	StartedAt  int64  `json:"startedAt,omitempty"` // unix ms
	SentCount  int64  `json:"sentCount"`
	RecvCount  int64  `json:"recvCount"`
}

// Connection states.
const (
	StateConnected    = "connected"
	StateDisconnected = "disconnected"
	StateError        = "error"
	StateStopped      = "stopped"
	StateLoginExpired = "login_expired"
)

// Target identifies the outbound recipient of a generated message.
type Target struct {
	Channel    string
	InstanceID string
	PeerKind   string // direct | group
	PeerID     string
}

// Outbound is a message to send through a channel's transport.
type Outbound struct {
	Text string
	// ImageURLs optional media to attach; left empty for text-only v1.
	ImageURLs []string
}

// InboundMessage is one message received from a peer, normalized to a
// channel-independent shape the manager can dispatch on.
type InboundMessage struct {
	Channel    string
	InstanceID string
	AccountID  string
	PeerKind   string
	PeerID     string
	SenderID   string
	SenderName string
	Content    string
	ReceivedAt time.Time
	Raw        json.RawMessage
}

// InboundHandler receives normalized inbound messages from any instance.
// It is invoked serially per peer by the manager's dispatch goroutine.
type InboundHandler func(ctx context.Context, msg InboundMessage)

// Instance is one live connector. Implementations are built per channel
// and registered with the manager; the manager drives lifecycle and routes
// outbound sends back here.
type Instance interface {
	ID() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Status() Status
	Send(ctx context.Context, to Target, msg Outbound) error
	SetInboundHandler(h InboundHandler)
}

// Prober performs a one-shot connectivity check for the candidate config
// without persisting or staying connected. Connectors able to probe
// implement it; others fall back to a build-only pass.
type Prober interface {
	Probe(ctx context.Context) ([]Check, error)
}

// Check is one granular connectivity check inside a probe report.
type Check struct {
	Code   string `json:"code"`
	Title  string `json:"title"`
	Level  string `json:"level"` // pass | warn | fail
	Detail string `json:"detail,omitempty"`
}

// Config is the persisted, channel-agnostic record one instance needs to
// construct its connector. Channel-specific fields ride in Raw.
type Config struct {
	Channel      string          `json:"channel"`
	ConfigJSON   json.RawMessage `json:"config"`
	AccessPolicy AccessPolicy    `json:"accessPolicy"`
}

// AccessPolicy controls who may talk to an instance.
type AccessPolicy struct {
	Mode      string   `json:"mode"` // open | allowlist | disabled
	AllowFrom []string `json:"allowFrom,omitempty"`
}
