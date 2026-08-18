// QR login state machine for the weixin (iLink) connector. A start creates
// a session with a QR url + session id; poll advances it through the scan /
// confirm lifecycle until it yields a bot_token, which the caller persists.

package im

import (
	"context"
	"sync"
	"time"
)

// QRState is the wire-visible login progress.
const (
	QRWaiting   = "waiting"   // QR shown, awaiting scan
	QRScanned   = "scanned"   // user scanned, awaiting confirm
	QRConfirmed = "confirmed" // login succeeded; token issued
	QRExpired   = "expired"   // QR timed out, regenerate
	QRCancelled = "cancelled" // user cancelled
	QRError     = "error"
)

// QRLoginResult is the agent.im.login_poll payload.
type QRLoginResult struct {
	State     string `json:"state"`
	SessionID string `json:"sessionId,omitempty"`
	QRURL     string `json:"qrUrl,omitempty"`
	Token     string `json:"token,omitempty"`
	BotID     string `json:"botId,omitempty"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	ExpiresIn int64  `json:"expiresIn,omitempty"` // ms until expiry
}

// QRLoginSession is one in-progress login.
type QRLoginSession struct {
	ID         string
	Channel    string
	InstanceID string
	State      string
	QRURL      string
	AvatarURL  string
	Token      string
	BotID      string
	ExpiresAt  time.Time
	// advance is a per-channel hook that mutates the session toward a new
	// state based on external polling; nil for channels without QR login.
	advance func(s *QRLoginSession, now time.Time)
}

// QRLoginProvider abstracts the transport-specific scan/confirm polling.
type QRLoginProvider interface {
	// Begin issues a fresh QR; poll updates the session until done.
	Begin(channel, instanceID string) (*QRLoginSession, error)
	Poll(ctx context.Context, s *QRLoginSession) error
}

// QRManager owns the set of in-flight login sessions.
type QRManager struct {
	mu       sync.Mutex
	sessions map[string]*QRLoginSession
	provider QRLoginProvider
	ttl      time.Duration
}

// NewQRManager builds a QRManager. provider may be nil; Start then returns
// a not-supported error.
func NewQRManager(provider QRLoginProvider) *QRManager {
	return &QRManager{
		sessions: make(map[string]*QRLoginSession),
		provider: provider,
		ttl:      2 * time.Minute,
	}
}

// Start begins a new login for channel + instance.
func (m *QRManager) Start(ctx context.Context, p LoginStartParams) (QRLoginResult, error) {
	if m.provider == nil {
		return QRLoginResult{}, errNoQRProvider
	}
	s, err := m.provider.Begin(p.Channel, p.InstanceID)
	if err != nil {
		return QRLoginResult{}, err
	}
	if s.ID == "" {
		s.ID = newID("qr")
	}
	if s.ExpiresAt.IsZero() {
		s.ExpiresAt = time.Now().Add(m.ttl)
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return m.result(s), nil
}

// Poll advances a session and returns its latest state.
func (m *QRManager) Poll(ctx context.Context, workspaceID, sessionID string) (QRLoginResult, error) {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return QRLoginResult{State: QRError}, nil
	}
	if s.advance != nil {
		s.advance(s, time.Now())
	}
	if m.provider != nil {
		if err := m.provider.Poll(ctx, s); err != nil {
			s.State = QRError
		}
	}
	if !s.ExpiresAt.IsZero() && time.Now().After(s.ExpiresAt) && s.State != QRConfirmed {
		s.State = QRExpired
	}
	return m.result(s), nil
}

func (m *QRManager) result(s *QRLoginSession) QRLoginResult {
	return QRLoginResult{
		State:     s.State,
		SessionID: s.ID,
		QRURL:     s.QRURL,
		Token:     s.Token,
		BotID:     s.BotID,
		AvatarURL: s.AvatarURL,
		ExpiresIn: msUntil(s.ExpiresAt),
	}
}

// Reap drops sessions past expiry to bound memory.
func (m *QRManager) Reap() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, s := range m.sessions {
		if !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt) {
			delete(m.sessions, id)
		}
	}
}

func msUntil(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return time.Until(t).Milliseconds()
}

// errNoQRProvider is returned when the channel has no QR login backend.
var errNoQRProvider = &qrError{"qr login not supported for this channel"}

type qrError struct{ msg string }

func (e *qrError) Error() string { return e.msg }
