// Manager owns the live instance set: it builds connectors from the
// persisted config, drives their lifecycle, dispatches inbound messages to
// their pinned darvin session, and routes outbound replies back to the
// originating peer. Events fan out through the injected Broadcaster.

package im

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
)

// SessionRunner submits headless turns. It is satisfied by
// *gateway.SessionManager (defined here so the im package does not import
// the gateway). ReplySink is invoked with the final assistant text when
// the turn settles.
type SessionRunner interface {
	SubmitForIM(ctx context.Context, imKey, prompt string, sink func(ctx context.Context, reply string, runID string)) (string, error)
}

// Broadcaster fans a notification out to every active WS client.
type Broadcaster interface {
	Broadcast(method string, params any)
}

// Builder assembles one live Instance from a channel name + config. It is
// resolved per channel from the registry; a channel with no builder reports
// a clear error rather than silently failing.
type Builder func(ctx context.Context, cfg InstanceSeed) (Instance, error)

// InstanceSeed carries everything a connector needs to build + wire itself.
type InstanceSeed struct {
	Store        *store.IMChannelStore
	Broadcaster  Broadcaster
	Channel      string
	InstanceID   string
	AccessPolicy AccessPolicy
	Config       map[string]any
	Logger       *zap.Logger
}

// IMWorkspaceStore narrows the workspace store surface the manager needs
// to create one dedicated workspace per IM channel.
type IMWorkspaceStore interface {
	Create(ctx context.Context, w store.Workspace) error
	GetByID(ctx context.Context, id string) (store.Workspace, error)
}

// IMSessionStore narrows the session-store surface for IM sessions.
type IMSessionStore interface {
	Save(ctx context.Context, s *session.Session) error
	BindWorkspace(ctx context.Context, sessionID, workspaceID string) error
}

// Manager configures, starts and stops channel instances and routes
// inbound / outbound traffic.
type Manager struct {
	store    IMChannels
	sessions SessionRunner
	bcast    Broadcaster
	log      *zap.Logger
	builders map[string]Builder

	wsStore  IMWorkspaceStore
	sessStor IMSessionStore
	imDir    string

	mu        sync.Mutex
	instances map[string]Instance
	byID      map[string]*store.IMChannel
	// imWorkspaces tracks the workspace id we created per IM instance.
	imWorkspaces map[string]string
}

// IMChannels narrows the store surface the manager needs.
type IMChannels interface {
	List(ctx context.Context, workspaceID string) ([]store.IMChannel, error)
	Get(ctx context.Context, id string) (store.IMChannel, error)
	Create(ctx context.Context, ch *store.IMChannel) error
	Update(ctx context.Context, id string, patch map[string]any) error
	Delete(ctx context.Context, id string) error
	SetEnabled(ctx context.Context, id string, enabled bool) error
	SaveToken(ctx context.Context, id, channel, tokenJSON string) error
	TokenFor(ctx context.Context, id string) (store.IMChannelToken, bool, error)
}

// NewManager builds a Manager. Builders is a channel→Builder map; the
// runtime injects the three built-in connectors (see qq / wecom / weixin
// subpackages). wsStore + sessStor enable per-channel workspace bootstrap
// (one dedicated workspace per IM channel so each channel's sessions live
// under a stable, named workspace visible in the UI).
func NewManager(
	s IMChannels,
	sessions SessionRunner,
	bcast Broadcaster,
	builders map[string]Builder,
	wsStore IMWorkspaceStore,
	sessStor IMSessionStore,
	imDir string,
	log *zap.Logger,
) *Manager {
	return &Manager{
		store:        s,
		sessions:     sessions,
		bcast:        bcast,
		log:          log,
		builders:     builders,
		wsStore:      wsStore,
		sessStor:     sessStor,
		imDir:        imDir,
		instances:    make(map[string]Instance),
		byID:         make(map[string]*store.IMChannel),
		imWorkspaces: make(map[string]string),
	}
}

// StopAll halts every live instance without clearing the persisted config.
func (m *Manager) StopAll(ctx context.Context) {
	m.mu.Lock()
	instances := m.instances
	m.instances = make(map[string]Instance)
	m.mu.Unlock()
	for _, inst := range instances {
		_ = inst.Stop(ctx)
	}
}

// Reload stops every live instance and resumes per the persisted enabled
// flag. It is the entry point called by the runtime on start. IM instances
// are app-global, so the workspaceID param is accepted but ignored.
func (m *Manager) Reload(ctx context.Context, workspaceID string) error {
	m.mu.Lock()
	for id, inst := range m.instances {
		_ = inst.Stop(ctx)
		delete(m.instances, id)
	}
	m.byID = make(map[string]*store.IMChannel)
	m.mu.Unlock()

	rows, err := m.store.List(ctx, "")
	if err != nil {
		return err
	}
	for i := range rows {
		ch := rows[i]
		m.byID[ch.ID] = &ch
		if ch.Enabled {
			if err := m.buildInstance(ctx, &ch); err != nil {
				m.log.Warn("im instance start failed",
					zap.String("instance_id", ch.ID), zap.Error(err))
			}
		}
	}
	return nil
}

// ListStatuses returns the wire status snapshot for every instance.
func (m *Manager) ListStatuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.byID))
	for id, ch := range m.byID {
		s := Status{
			Channel:    ch.Channel,
			InstanceID: id,
			Enabled:    ch.Enabled,
			State:      StateStopped,
		}
		if inst, ok := m.instances[id]; ok {
			s = inst.Status()
		}
		out = append(out, s)
	}
	return out
}

// Create persists a new instance and starts it when enabled.
func (m *Manager) Create(ctx context.Context, ch *store.IMChannel) error {
	if err := m.store.Create(ctx, ch); err != nil {
		return err
	}
	m.mu.Lock()
	m.byID[ch.ID] = ch
	m.mu.Unlock()
	if ch.Enabled {
		if err := m.buildInstance(ctx, ch); err != nil {
			return err
		}
	}
	m.bcastEvents()
	return nil
}

// Update persists a config patch and hot-reloads the instance.
func (m *Manager) Update(ctx context.Context, id string, patch map[string]any) error {
	ch, err := m.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := m.store.Update(ctx, id, patch); err != nil {
		return err
	}
	// refresh the in-memory config snapshot so status reflects the patch
	if v, ok := patch["config_json"].(string); ok && v != "" {
		ch.ConfigJSON = v
	}
	if v, ok := patch["access_mode"].(string); ok {
		ch.AccessMode = v
	}
	if v, ok := patch["name"].(string); ok {
		ch.Name = v
	}
	m.mu.Lock()
	m.byID[id] = &ch
	wasRunning := m.instances[id] != nil
	inst := m.instances[id]
	m.mu.Unlock()
	// hot-reload: rebuild the connector when it is enabled and running
	if ch.Enabled && wasRunning {
		m.mu.Lock()
		delete(m.instances, id)
		m.mu.Unlock()
		if inst != nil {
			_ = inst.Stop(ctx)
		}
		if err := m.buildInstance(ctx, &ch); err != nil {
			m.log.Warn("im hot-reload failed", zap.String("instance_id", id), zap.Error(err))
		}
	}
	m.bcastEvents()
	return nil
}

// Delete stops and removes an instance; the darvin session it produced is
// left intact.
func (m *Manager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	inst, ok := m.instances[id]
	if ok {
		delete(m.instances, id)
	}
	delete(m.byID, id)
	m.mu.Unlock()
	if ok && inst != nil {
		if err := inst.Stop(ctx); err != nil {
			m.log.Warn("im stop on delete failed", zap.String("instance_id", id), zap.Error(err))
		}
	}
	if err := m.store.Delete(ctx, id); err != nil {
		return err
	}
	m.bcastEvents()
	return nil
}

// SetEnabled toggles a running instance on/off.
func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool) error {
	ch, err := m.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := m.store.SetEnabled(ctx, id, enabled); err != nil {
		return err
	}
	ch.Enabled = enabled
	m.mu.Lock()
	m.byID[id] = &ch
	m.mu.Unlock()

	if enabled {
		if err := m.buildInstance(ctx, &ch); err != nil {
			return err
		}
	} else {
		m.mu.Lock()
		inst := m.instances[id]
		delete(m.instances, id)
		m.mu.Unlock()
		if inst != nil {
			_ = inst.Stop(ctx)
		}
	}
	m.bcastEvents()
	return nil
}

// buildInstance resolves the builder for the channel and wires a live
// instance whose inbound handler routes to the session runner.
func (m *Manager) buildInstance(ctx context.Context, ch *store.IMChannel) error {
	build, ok := m.builders[ch.Channel]
	if !ok {
		return fmt.Errorf("im: no builder for channel %q", ch.Channel)
	}
	cfg := make(map[string]any)
	if ch.ConfigJSON != "" {
		_ = decodeConfig(ch.ConfigJSON, &cfg)
	}
	seed := InstanceSeed{
		Store:        nil, // connectors use optional token persistence; nil-safe
		Broadcaster:  m.bcast,
		Channel:      ch.Channel,
		InstanceID:   ch.ID,
		AccessPolicy: accessPolicy(ch),
		Config:       cfg,
		Logger:       m.log,
	}
	inst, err := build(ctx, seed)
	if err != nil {
		return err
	}
	inst.SetInboundHandler(m.handleInbound)
	m.mu.Lock()
	m.instances[ch.ID] = inst
	m.mu.Unlock()
	if err := inst.Start(ctx); err != nil {
		m.log.Warn("im instance start failed",
			zap.String("instance_id", ch.ID), zap.Error(err))
	}
	m.bcastStatus()
	return nil
}

// handleInbound is the single inbound funnel: it builds the session key
// from the normalized message and submits a headless turn whose reply sinks
// back to the originating instance + peer.
func (m *Manager) handleInbound(ctx context.Context, msg InboundMessage) {
	if !m.authorized(msg) {
		m.log.Debug("im inbound rejected by policy",
			zap.String("channel", msg.Channel), zap.String("peer_id", msg.PeerID))
		return
	}
	imKey := SessionKey(msg.Channel, msg.AccountID, msg.PeerKind, msg.PeerID)
	target := Target{
		Channel:    msg.Channel,
		InstanceID: msg.InstanceID,
		PeerKind:   msg.PeerKind,
		PeerID:     msg.PeerID,
	}
	if err := m.ensureIMWorkspace(ctx, msg.InstanceID, msg.Channel, msg.AccountID, msg.PeerKind, msg.PeerID, msg.SenderName); err != nil {
		m.log.Warn("im workspace ensure failed",
			zap.String("instance_id", msg.InstanceID), zap.Error(err))
	}
	sink := func(sctx context.Context, reply, runID string) {
		if reply == "" {
			return
		}
		m.sendReply(sctx, target, reply)
	}
	if _, err := m.sessions.SubmitForIM(sctxBounded(ctx), imKey, msg.Content, sink); err != nil {
		m.log.Warn("im submit failed", zap.String("im_key", imKey), zap.Error(err))
	}
}

// ensureIMWorkspace guarantees a dedicated workspace per IM instance and
// binds the IM session for (peer) to it. The first call for an instance
// mints the workspace; later calls reuse the cached id.
func (m *Manager) ensureIMWorkspace(ctx context.Context, instanceID, channel, accountID, peerKind, peerID, peerName string) error {
	m.mu.Lock()
	if m.wsStore == nil || m.sessStor == nil {
		m.mu.Unlock()
		return nil
	}
	wid, ok := m.imWorkspaces[instanceID]
	m.mu.Unlock()
	if ok && wid != "" {
		m.bindIMSession(ctx, channel, accountID, peerKind, peerID, peerName, wid)
		return nil
	}
	if err := os.MkdirAll(m.imDir, 0o700); err != nil {
		return fmt.Errorf("mkdir im root: %w", err)
	}
	rootPath := filepath.Join(m.imDir, "im-"+channel+"-"+instanceID)
	if err := os.MkdirAll(rootPath, 0o700); err != nil {
		return fmt.Errorf("mkdir im dir: %w", err)
	}
	wid = "imws-" + channel + "-" + instanceID
	name := imWorkspaceName(channel)
	row := store.Workspace{ID: wid, Name: name, RootPath: rootPath}
	if _, err := m.wsStore.GetByID(ctx, wid); err != nil {
		if err := m.wsStore.Create(ctx, row); err != nil {
			return fmt.Errorf("create im workspace: %w", err)
		}
		m.bcast.Broadcast("agent.event", map[string]any{"type": "WorkspacesChanged"})
	}
	m.mu.Lock()
	m.imWorkspaces[instanceID] = wid
	m.mu.Unlock()
	m.bindIMSession(ctx, channel, accountID, peerKind, peerID, peerName, wid)
	return nil
}

// bindIMSession creates the IM session row and pins it to the channel
// workspace so the UI groups all sessions for the same channel together.
func (m *Manager) bindIMSession(ctx context.Context, channel, accountID, peerKind, peerID, peerName, workspaceID string) {
	imKey := SessionKey(channel, accountID, peerKind, peerID)
	sessionID := "im-" + imKey // matches gateway.IMSessionID so SubmitForIM finds it
	sess := session.NewSession(sessionID)
	if err := m.sessStor.Save(ctx, sess); err != nil {
		m.log.Warn("im session save failed", zap.String("session_id", sessionID), zap.Error(err))
		return
	}
	if err := m.sessStor.BindWorkspace(ctx, sessionID, workspaceID); err != nil {
		m.log.Warn("im session bind failed", zap.String("session_id", sessionID), zap.Error(err))
	}
	if updateTitle, ok := m.sessStor.(interface {
		UpdateTitle(ctx context.Context, id, title string) error
	}); ok {
		title := imSessionTitle(channel, peerID, peerName)
		if err := updateTitle.UpdateTitle(ctx, sessionID, title); err != nil {
			m.log.Warn("im session title failed", zap.String("session_id", sessionID), zap.Error(err))
		}
	}
	m.bcast.Broadcast("agent.event", map[string]any{"type": "SessionsChanged"})
}

// imWorkspaceName returns the human-readable name for an IM channel.
func imWorkspaceName(channel string) string {
	switch channel {
	case "weixin":
		return "微信消息"
	case "qq":
		return "QQ 消息"
	case "wecom":
		return "企业微信消息"
	default:
		return "IM 消息"
	}
}

func imSessionTitle(channel, peerID, peerName string) string {
	if peerName != "" {
		return channel + " · " + peerName
	}
	return channel + " · " + peerID
}

// sctxBounded guards the submit against a dead inbound context; the turn
// itself is background-rooted by the Loop, so a peer disconnect never
// cancels an in-flight reply.
func sctxBounded(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	if _, done := ctx.Deadline(); done {
		return ctx
	}
	// keep the caller's values but detach cancellation — the Loop owns the
	// turn lifetime.
	return context.WithoutCancel(ctx)
}

func (m *Manager) sendReply(ctx context.Context, to Target, reply string) {
	m.mu.Lock()
	inst := m.instances[to.InstanceID]
	m.mu.Unlock()
	if inst == nil {
		return
	}
	if err := inst.Send(ctx, to, Outbound{Text: reply}); err != nil {
		m.log.Warn("im send failed", zap.String("instance_id", to.InstanceID), zap.Error(err))
	}
}

// authorized applies the instance access policy for an inbound sender.
func (m *Manager) authorized(msg InboundMessage) bool {
	m.mu.Lock()
	ch := m.byID[msg.InstanceID]
	m.mu.Unlock()
	if ch == nil {
		return false
	}
	switch ch.AccessMode {
	case "disabled":
		return false
	case "allowlist":
		for _, id := range strings.Split(ch.AllowFrom, ",") {
			if strings.TrimSpace(id) == msg.SenderID {
				return true
			}
		}
		return false
	default: // open
		return true
	}
}

// SessionKey builds the stable session key for a conversation.
func SessionKey(channel, accountID, peerKind, peerID string) string {
	return fmt.Sprintf("im:%s:%s:%s:%s", channel, accountID, peerKind, peerID)
}

// bcastEvents pushes an ImChanged notification on any config mutation.
func (m *Manager) bcastEvents() {
	m.bcast.Broadcast("agent.event", map[string]any{
		"type": "ImChanged",
	})
}

// bcastStatus pushes an ImStatusChanged notification on connection changes.
func (m *Manager) bcastStatus() {
	m.bcast.Broadcast("agent.event", map[string]any{
		"type": "ImStatusChanged",
	})
}

func accessPolicy(ch *store.IMChannel) AccessPolicy {
	return AccessPolicy{Mode: ch.AccessMode, AllowFrom: splitList(ch.AllowFrom)}
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func decodeConfig(s string, out *map[string]any) error {
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return err
	}
	*out = v
	return nil
}
