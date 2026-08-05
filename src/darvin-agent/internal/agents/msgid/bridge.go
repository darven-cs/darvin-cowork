// Package msgid wires the in-flight turn ids — messageID, runID and the
// distinct userMessageID — between the loop (agentloop.Loop) and the agent
// runtime (executor, dispatcher).
//
// The Bridge is pure plumbing: every method is either a setter that records
// a getter the runtime calls, or a reader that returns whatever the most
// recent setter installed. There is no logic here, which is the point:
// pulling the field set out of *Agent reduces it by three fields and three
// setters, and lets the bridge be tested in isolation.
package msgid

import "sync"

// Bridge holds the per-turn ids the agent runtime stamps on every emitted
// event. Wired by main.go via the Attach* methods.
type Bridge struct {
	mu           sync.RWMutex
	msgIDSrc     func() string
	runIDSrc     func() string
	userMsgIDSrc func() string
}

// NewBridge returns a Bridge with no sources wired; readers return "" until
// the caller installs one.
func NewBridge() *Bridge { return &Bridge{} }

// AttachMessageID records the source the runtime reads to look up the
// in-flight messageID.
func (b *Bridge) AttachMessageID(src func() string) {
	b.mu.Lock()
	b.msgIDSrc = src
	b.mu.Unlock()
}

// AttachRunID records the source the runtime reads to look up the in-flight
// runID.
func (b *Bridge) AttachRunID(src func() string) {
	b.mu.Lock()
	b.runIDSrc = src
	b.mu.Unlock()
}

// AttachUserMessageID records the source the runtime reads to look up the
// user message's own id, distinct from the assistant messageID so the
// persisted user row survives the assistant row.
func (b *Bridge) AttachUserMessageID(src func() string) {
	b.mu.Lock()
	b.userMsgIDSrc = src
	b.mu.Unlock()
}

// CurrentMessageID returns the in-flight messageID, or "" when no source
// has been wired or the bridge is otherwise idle.
func (b *Bridge) CurrentMessageID() string {
	b.mu.RLock()
	src := b.msgIDSrc
	b.mu.RUnlock()
	if src == nil {
		return ""
	}
	return src()
}

// CurrentRunID returns the in-flight runID, or "" when no source is wired.
func (b *Bridge) CurrentRunID() string {
	b.mu.RLock()
	src := b.runIDSrc
	b.mu.RUnlock()
	if src == nil {
		return ""
	}
	return src()
}

// CurrentUserMessageID returns the in-flight user message id, or "" when
// no source is wired.
func (b *Bridge) CurrentUserMessageID() string {
	b.mu.RLock()
	src := b.userMsgIDSrc
	b.mu.RUnlock()
	if src == nil {
		return ""
	}
	return src()
}
