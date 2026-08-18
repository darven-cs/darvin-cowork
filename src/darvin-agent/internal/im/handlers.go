// JSON-RPC handler layer for the IM-channel subsystem. The gateway router
// dispatches agent.im.<op> here; each handler unpacks params, delegates to
// im.Manager, and wraps the result in a JSON-RPC Response.

package im

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"darvin-cowork/backend/internal/agents/store"
)

// Exported entry points the gateway dispatch layer calls. Each maps
// 1:1 to an agent.im.<op> method and returns the wire payload or an error.

// HandleIMList implements agent.im.list.
func (h *Handlers) HandleIMList(ctx context.Context, params json.RawMessage) (any, error) {
	return h.handleList(ctx, params)
}

// HandleIMGet implements agent.im.get.
func (h *Handlers) HandleIMGet(ctx context.Context, params json.RawMessage) (any, error) {
	return h.handleGet(ctx, params)
}

// HandleIMCreate implements agent.im.create.
func (h *Handlers) HandleIMCreate(ctx context.Context, params json.RawMessage) (any, error) {
	return h.handleCreate(ctx, params)
}

// HandleIMUpdate implements agent.im.update.
func (h *Handlers) HandleIMUpdate(ctx context.Context, params json.RawMessage) (any, error) {
	return h.handleUpdate(ctx, params)
}

// HandleIMDelete implements agent.im.delete.
func (h *Handlers) HandleIMDelete(ctx context.Context, params json.RawMessage) (any, error) {
	return h.handleDelete(ctx, params)
}

// HandleIMSetEnabled implements agent.im.set_enabled.
func (h *Handlers) HandleIMSetEnabled(ctx context.Context, params json.RawMessage) (any, error) {
	return h.handleSetEnabled(ctx, params)
}

// HandleIMTest implements agent.im.test.
func (h *Handlers) HandleIMTest(ctx context.Context, params json.RawMessage) (any, error) {
	return h.handleTest(ctx, params)
}

// HandleIMLoginStart implements agent.im.login_start.
func (h *Handlers) HandleIMLoginStart(ctx context.Context, params json.RawMessage) (any, error) {
	return h.handleLoginStart(ctx, params)
}

// HandleIMLoginPoll implements agent.im.login_poll.
func (h *Handlers) HandleIMLoginPoll(ctx context.Context, params json.RawMessage) (any, error) {
	return h.handleLoginPoll(ctx, params)
}

// Wire types mirror the persisted / live shape exposed over IPC. They use
// the darvin-api wire naming (camelCase) so the renderer consumes them
// without reshaping.

// InstanceWire is one channel instance as the renderer sees it.
type InstanceWire struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspaceId"`
	Channel     string          `json:"channel"`
	Name        string          `json:"name"`
	Enabled     bool            `json:"enabled"`
	Config      json.RawMessage `json:"config"`
	AccessMode  string          `json:"accessMode"`
	AllowFrom   []string        `json:"allowFrom,omitempty"`
	Status      Status          `json:"status"`
}

// ListResult is the agent.im.list payload.
type ListResult struct {
	Instances []InstanceWire `json:"instances"`
}

// CreateParams is the agent.im.create inbound shape.
type CreateParams struct {
	WorkspaceID string          `json:"workspaceId"`
	Channel     string          `json:"channel"`
	Name        string          `json:"name"`
	Enabled     *bool           `json:"enabled,omitempty"`
	Config      json.RawMessage `json:"config"`
	AccessMode  string          `json:"accessMode"`
	AllowFrom   []string        `json:"allowFrom,omitempty"`
}

// UpdateParams is the agent.im.update inbound shape (patch).
type UpdateParams struct {
	WorkspaceID string          `json:"workspaceId"`
	InstanceID  string          `json:"instanceId"`
	Patch       json.RawMessage `json:"patch"`
}

// SetEnabledParams is the agent.im.set_enabled inbound shape.
type SetEnabledParams struct {
	WorkspaceID string `json:"workspaceId"`
	InstanceID  string `json:"instanceId"`
	Enabled     bool   `json:"enabled"`
}

// TestParams is the agent.im.test inbound shape.
type TestParams struct {
	WorkspaceID string          `json:"workspaceId"`
	Channel     string          `json:"channel"`
	Config      json.RawMessage `json:"config"`
}

// TestResult reports connectivity for a candidate config.
type TestResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// LoginStartParams / LoginPollParams drive the QR login state machine.
type LoginStartParams struct {
	WorkspaceID string `json:"workspaceId"`
	Channel     string `json:"channel"`
	InstanceID  string `json:"instanceId"`
}

// Handlers wraps Manager + store for the JSON-RPC dispatch layer.
type Handlers struct {
	mgr   *Manager
	store *store.IMChannelStore
	qr    *QRManager
	seed  InstanceSeed
	ctx   context.Context
}

// NewHandlers builds a Handlers bundle. mgr may be nil; the handlers then
// return explicit not-wired errors (matches the gateway's internal-error
// convention for absent subsystems).
func NewHandlers(ctx context.Context, mgr *Manager, s *store.IMChannelStore, qr *QRManager, seed InstanceSeed) *Handlers {
	return &Handlers{mgr: mgr, store: s, qr: qr, seed: seed, ctx: ctx}
}

// WireResult maps a stored channel + its live status to the wire shape.
func WireResult(ch *store.IMChannel, st Status) InstanceWire {
	return InstanceWire{
		ID:          ch.ID,
		WorkspaceID: ch.WorkspaceID,
		Channel:     ch.Channel,
		Name:        ch.Name,
		Enabled:     ch.Enabled,
		Config:      json.RawMessage(ch.ConfigJSON),
		AccessMode:  ch.AccessMode,
		AllowFrom:   splitList(ch.AllowFrom),
		Status:      st,
	}
}

// handleList returns every instance plus its status. IM instances are
// app-global (not scoped to a chat workspace), so the workspaceId param is
// accepted but ignored.
func (h *Handlers) handleList(ctx context.Context, params json.RawMessage) (any, error) {
	if h.mgr == nil {
		return nil, errors.New("im handlers not wired")
	}
	rows, err := h.store.List(ctx, "")
	if err != nil {
		return nil, err
	}
	statuses := statusMap(h.mgr.ListStatuses())
	out := make([]InstanceWire, 0, len(rows))
	for i := range rows {
		out = append(out, WireResult(&rows[i], statuses[rows[i].ID]))
	}
	return ListResult{Instances: out}, nil
}

// handleGet returns one instance.
func (h *Handlers) handleGet(ctx context.Context, params json.RawMessage) (any, error) {
	if h.mgr == nil {
		return nil, errors.New("im handlers not wired")
	}
	var p struct {
		InstanceID string `json:"instanceId"`
	}
	if err := decodeRaw(params, &p); err != nil {
		return nil, err
	}
	ch, err := h.store.Get(ctx, p.InstanceID)
	if err != nil {
		return nil, err
	}
	statuses := statusMap(h.mgr.ListStatuses())
	return WireResult(&ch, statuses[ch.ID]), nil
}

// handleCreate persists a new instance. IM instances are app-global, so
// workspaceId is optional; the passed value (if any) is no longer used to
// scope the instance to a chat workspace.
func (h *Handlers) handleCreate(ctx context.Context, params json.RawMessage) (any, error) {
	if h.mgr == nil {
		return nil, errors.New("im handlers not wired")
	}
	var p CreateParams
	if err := decodeRaw(params, &p); err != nil {
		return nil, err
	}
	if p.Channel == "" {
		return nil, errors.New("channel is required")
	}
	ch := &store.IMChannel{
		ID:         newID(p.Channel),
		Channel:    p.Channel,
		Name:       p.Name,
		ConfigJSON: string(p.Config),
		AccessMode: p.AccessMode,
		AllowFrom:  joinList(p.AllowFrom),
	}
	if p.Enabled != nil {
		ch.Enabled = *p.Enabled
	}
	if err := h.mgr.Create(ctx, ch); err != nil {
		return nil, err
	}
	statuses := statusMap(h.mgr.ListStatuses())
	return WireResult(ch, statuses[ch.ID]), nil
}

// handleUpdate applies a patch and hot-reloads.
func (h *Handlers) handleUpdate(ctx context.Context, params json.RawMessage) (any, error) {
	if h.mgr == nil {
		return nil, errors.New("im handlers not wired")
	}
	var p UpdateParams
	if err := decodeRaw(params, &p); err != nil {
		return nil, err
	}
	var patch map[string]any
	if err := decodeRaw(p.Patch, &patch); err != nil {
		return nil, err
	}
	if err := h.mgr.Update(ctx, p.InstanceID, patch); err != nil {
		return nil, err
	}
	ch, err := h.store.Get(ctx, p.InstanceID)
	if err != nil {
		return nil, err
	}
	statuses := statusMap(h.mgr.ListStatuses())
	return WireResult(&ch, statuses[ch.ID]), nil
}

// handleDelete removes an instance.
func (h *Handlers) handleDelete(ctx context.Context, params json.RawMessage) (any, error) {
	if h.mgr == nil {
		return nil, errors.New("im handlers not wired")
	}
	var p struct {
		InstanceID string `json:"instanceId"`
	}
	if err := decodeRaw(params, &p); err != nil {
		return nil, err
	}
	if err := h.mgr.Delete(ctx, p.InstanceID); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

// handleSetEnabled flips the enabled flag.
func (h *Handlers) handleSetEnabled(ctx context.Context, params json.RawMessage) (any, error) {
	if h.mgr == nil {
		return nil, errors.New("im handlers not wired")
	}
	var p SetEnabledParams
	if err := decodeRaw(params, &p); err != nil {
		return nil, err
	}
	if err := h.mgr.SetEnabled(ctx, p.InstanceID, p.Enabled); err != nil {
		return nil, err
	}
	ch, err := h.store.Get(ctx, p.InstanceID)
	if err != nil {
		return nil, err
	}
	statuses := statusMap(h.mgr.ListStatuses())
	return WireResult(&ch, statuses[ch.ID]), nil
}

// handleTest probes connectivity for a candidate config without persisting.
func (h *Handlers) handleTest(ctx context.Context, params json.RawMessage) (any, error) {
	if h.mgr == nil {
		return nil, errors.New("im handlers not wired")
	}
	var p TestParams
	if err := decodeRaw(params, &p); err != nil {
		return nil, err
	}
	build, ok := h.mgr.builders[p.Channel]
	if !ok {
		return TestResult{}, fmt.Errorf("no connector for channel %q", p.Channel)
	}
	var cfg map[string]any
	if p.Config != nil {
		_ = decodeRaw(p.Config, &cfg)
	}
	seed := InstanceSeed{
		Broadcaster: h.mgr.bcast,
		Channel:     p.Channel,
		InstanceID:  "test",
		Config:      cfg,
	}
	inst, err := build(h.ctx, seed)
	if err != nil {
		return TestResult{OK: false, Error: err.Error()}, nil
	}
	_ = inst.Stop(h.ctx)
	return TestResult{OK: true}, nil
}

// handleLoginStart begins a QR login session (weixin).
func (h *Handlers) handleLoginStart(ctx context.Context, params json.RawMessage) (any, error) {
	if h.qr == nil {
		return nil, errors.New("qr login not supported")
	}
	var p LoginStartParams
	if err := decodeRaw(params, &p); err != nil {
		return nil, err
	}
	return h.qr.Start(ctx, p)
}

// handleLoginPoll polls a QR login session.
func (h *Handlers) handleLoginPoll(ctx context.Context, params json.RawMessage) (any, error) {
	if h.qr == nil {
		return nil, errors.New("qr login not supported")
	}
	var p struct {
		WorkspaceID string `json:"workspaceId"`
		SessionID   string `json:"sessionId"`
	}
	if err := decodeRaw(params, &p); err != nil {
		return nil, err
	}
	return h.qr.Poll(ctx, p.WorkspaceID, p.SessionID)
}

func statusMap(statuses []Status) map[string]Status {
	out := make(map[string]Status, len(statuses))
	for _, s := range statuses {
		out[s.InstanceID] = s
	}
	return out
}

func decodeRaw(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
