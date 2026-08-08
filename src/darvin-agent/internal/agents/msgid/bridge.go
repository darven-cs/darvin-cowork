// Package msgid wires the in-flight turn ids (messageID, runID,
// userMessageID) between the loop and the agent runtime.
//
// Bridge is pure plumbing: setters record a getter the runtime calls;
// readers return whatever the most recent setter installed.
package msgid

import "sync"

// Bridge holds the per-turn ids the agent runtime stamps on every event.
// Wired by main.go via the Attach* methods.
type Bridge struct {
	mu           sync.RWMutex
	msgIDSrc     func() string
	runIDSrc     func() string
	userMsgIDSrc func() string
}

// NewBridge returns a Bridge with no sources wired; readers return "".
func NewBridge() *Bridge { return &Bridge{} }

func (b *Bridge) AttachMessageID(src func() string) {
	b.mu.Lock()
	b.msgIDSrc = src
	b.mu.Unlock()
}

func (b *Bridge) AttachRunID(src func() string) {
	b.mu.Lock()
	b.runIDSrc = src
	b.mu.Unlock()
}

// AttachUserMessageID records the source for the user message's own id,
// distinct from the assistant messageID so the persisted user row
// survives the assistant row.
func (b *Bridge) AttachUserMessageID(src func() string) {
	b.mu.Lock()
	b.userMsgIDSrc = src
	b.mu.Unlock()
}

func (b *Bridge) CurrentMessageID() string {
	b.mu.RLock()
	src := b.msgIDSrc
	b.mu.RUnlock()
	if src == nil {
		return ""
	}
	return src()
}

func (b *Bridge) CurrentRunID() string {
	b.mu.RLock()
	src := b.runIDSrc
	b.mu.RUnlock()
	if src == nil {
		return ""
	}
	return src()
}

func (b *Bridge) CurrentUserMessageID() string {
	b.mu.RLock()
	src := b.userMsgIDSrc
	b.mu.RUnlock()
	if src == nil {
		return ""
	}
	return src()
}
