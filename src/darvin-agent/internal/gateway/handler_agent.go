// Agent CRUD JSON-RPC handlers (list / get / create / update / delete).

package gateway

import (
	"context"
	"encoding/json"
	"strings"

	"darvin-cowork/backend/internal/agents/store"
)

// AgentWire is the renderer-facing agent shape, the store.Agent row
// projected onto the darvin-api contract.
type AgentWire struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	NameEn         string   `json:"nameEn"`
	DescriptionEn  string   `json:"descriptionEn"`
	Identity       string   `json:"identity"`
	IdentityEn     string   `json:"identityEn"`
	SystemPrompt   string   `json:"systemPrompt"`
	SystemPromptEn string   `json:"systemPromptEn"`
	Icon           string   `json:"icon"`
	Color          string   `json:"color"`
	SkillIDs       []string `json:"skillIds"`
	Source         string   `json:"source"`
	PresetID       string   `json:"presetId"`
	IsDefault      bool     `json:"isDefault"`
	SortOrder      int      `json:"sortOrder"`
	Enabled        bool     `json:"enabled"`
	WorkspaceID    string   `json:"workspaceId"`
}

func toAgentWire(a store.Agent) AgentWire {
	return AgentWire{
		ID:             a.ID,
		Name:           a.Name,
		Description:    a.Description,
		NameEn:         a.NameEn,
		DescriptionEn:  a.DescriptionEn,
		Identity:       a.Identity,
		IdentityEn:     a.IdentityEn,
		SystemPrompt:   a.SystemPrompt,
		SystemPromptEn: a.SystemPromptEn,
		Icon:           a.Icon,
		Color:          a.Color,
		SkillIDs:       store.DecodeSkillIDs(a.SkillIDs),
		Source:         a.Source,
		PresetID:       a.PresetID,
		IsDefault:      a.IsDefault,
		SortOrder:      a.SortOrder,
		Enabled:        a.Enabled,
		WorkspaceID:    a.WorkspaceID,
	}
}

// ListAgentsParams is the JSON-RPC params for agent.list_agents.
// workspaceId is required — agents are scoped per workspace.
type ListAgentsParams struct {
	WorkspaceID string `json:"workspaceId"`
}

// ListAgentsResult is the JSON-RPC result for agent.list_agents.
type ListAgentsResult struct {
	Agents []AgentWire `json:"agents"`
}

func handleListAgents(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p ListAgentsParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResp(id, CodeInvalidParams, "params must be an object", nil)
		}
	}
	if p.WorkspaceID == "" {
		return errorResp(id, CodeInvalidParams, "workspaceId is required", nil)
	}
	if h.AgentStore == nil {
		return successResp(id, ListAgentsResult{Agents: []AgentWire{}})
	}
	rows, err := h.AgentStore.ListByWorkspace(ctx, p.WorkspaceID)
	if err != nil {
		return errorResp(id, CodeInternalError, "agent list", err)
	}
	out := make([]AgentWire, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAgentWire(r))
	}
	return successResp(id, ListAgentsResult{Agents: out})
}

// GetAgentParams is the JSON-RPC params for agent.get_agent.
type GetAgentParams struct {
	AgentID string `json:"agentId"`
}

// GetAgentResult is the JSON-RPC result for agent.get_agent.
type GetAgentResult struct {
	Agent AgentWire `json:"agent"`
}

func handleGetAgent(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p GetAgentParams
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.AgentID == "" {
		return errorResp(id, CodeInvalidParams, "agentId is required", nil)
	}
	if h.AgentStore == nil {
		return errorResp(id, CodeInternalError, "agent store not configured", nil)
	}
	row, err := h.AgentStore.GetByID(ctx, p.AgentID)
	if err != nil {
		return errorResp(id, CodeInternalError, "agent get", err)
	}
	return successResp(id, GetAgentResult{Agent: toAgentWire(row)})
}

// CreateAgentParams is the JSON-RPC params for agent.create_agent.
// fromPresetId, when set, copies the preset's content fields into the new
// user agent instead of using the explicit field values.
type CreateAgentParams struct {
	WorkspaceID    string   `json:"workspaceId"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	NameEn         string   `json:"nameEn,omitempty"`
	DescriptionEn  string   `json:"descriptionEn,omitempty"`
	Identity       string   `json:"identity,omitempty"`
	IdentityEn     string   `json:"identityEn,omitempty"`
	SystemPrompt   string   `json:"systemPrompt,omitempty"`
	SystemPromptEn string   `json:"systemPromptEn,omitempty"`
	Icon           string   `json:"icon,omitempty"`
	Color          string   `json:"color,omitempty"`
	SkillIDs       []string `json:"skillIds,omitempty"`
	FromPresetID   string   `json:"fromPresetId,omitempty"`
}

// CreateAgentResult is the JSON-RPC result for agent.create_agent.
type CreateAgentResult struct {
	Agent AgentWire `json:"agent"`
}

func handleCreateAgent(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p CreateAgentParams
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.WorkspaceID == "" {
		return errorResp(id, CodeInvalidParams, "workspaceId is required", nil)
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return errorResp(id, CodeInvalidParams, "name is required", nil)
	}
	if h.AgentStore == nil {
		return errorResp(id, CodeInternalError, "agent store not configured", nil)
	}

	row := store.Agent{
		Name: name, Description: p.Description,
		NameEn: p.NameEn, DescriptionEn: p.DescriptionEn,
		Identity: p.Identity, IdentityEn: p.IdentityEn,
		SystemPrompt: p.SystemPrompt, SystemPromptEn: p.SystemPromptEn,
		Icon: p.Icon, Color: p.Color,
		WorkspaceID: p.WorkspaceID,
		Source:      "user",
		Enabled:     true,
	}
	if len(p.SkillIDs) > 0 {
		if b, err := json.Marshal(p.SkillIDs); err == nil {
			row.SkillIDs = string(b)
		}
	}
	if presetID := strings.TrimSpace(p.FromPresetID); presetID != "" {
		preset, ok := findPresetSeed(presetID)
		if !ok {
			return errorResp(id, CodeInvalidParams, "unknown presetId", nil)
		}
		row.NameEn = preset.NameEn
		row.DescriptionEn = preset.DescriptionEn
		row.Identity = preset.Identity
		row.IdentityEn = preset.IdentityEn
		row.SystemPrompt = preset.SystemPrompt
		row.SystemPromptEn = preset.SystemPromptEn
		row.Icon = preset.Icon
		row.Color = preset.Color
		row.SkillIDs = preset.SkillIDs
		row.PresetID = presetID
		if name == "" {
			row.Name = preset.Name
		}
	}

	created, err := h.AgentStore.Create(ctx, row)
	if err != nil {
		return errorResp(id, CodeInternalError, "agent create", err)
	}
	return successResp(id, CreateAgentResult{Agent: toAgentWire(created)})
}

// findPresetSeed looks a preset up by id across the 9 experts + Main Agent.
func findPresetSeed(presetID string) (store.Agent, bool) {
	if presetID == store.MainAgentSeed().ID {
		return store.MainAgentSeed(), true
	}
	for _, a := range store.PresetSeed() {
		if a.ID == presetID {
			return a, true
		}
	}
	return store.Agent{}, false
}

// UpdateAgentParams is the JSON-RPC params for agent.update_agent. The
// id / workspaceId / source / isDefault / presetId fields are ignored —
// they are immutable row identity.
type UpdateAgentParams struct {
	AgentID        string   `json:"agentId"`
	Name           *string  `json:"name,omitempty"`
	Description    *string  `json:"description,omitempty"`
	NameEn         *string  `json:"nameEn,omitempty"`
	DescriptionEn  *string  `json:"descriptionEn,omitempty"`
	Identity       *string  `json:"identity,omitempty"`
	IdentityEn     *string  `json:"identityEn,omitempty"`
	SystemPrompt   *string  `json:"systemPrompt,omitempty"`
	SystemPromptEn *string  `json:"systemPromptEn,omitempty"`
	Icon           *string  `json:"icon,omitempty"`
	Color          *string  `json:"color,omitempty"`
	SkillIDs       []string `json:"skillIds,omitempty"`
	Enabled        *bool    `json:"enabled,omitempty"`
	SortOrder      *int     `json:"sortOrder,omitempty"`
}

// UpdateAgentResult is the JSON-RPC result for agent.update_agent.
type UpdateAgentResult struct {
	Agent AgentWire `json:"agent"`
}

func handleUpdateAgent(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p UpdateAgentParams
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.AgentID == "" {
		return errorResp(id, CodeInvalidParams, "agentId is required", nil)
	}
	if h.AgentStore == nil {
		return errorResp(id, CodeInternalError, "agent store not configured", nil)
	}
	row, err := h.AgentStore.GetByID(ctx, p.AgentID)
	if err != nil {
		return errorResp(id, CodeInternalError, "agent get", err)
	}

	if p.Name != nil {
		if name := strings.TrimSpace(*p.Name); name != "" {
			row.Name = name
		}
	}
	if p.Description != nil {
		row.Description = *p.Description
	}
	if p.NameEn != nil {
		row.NameEn = *p.NameEn
	}
	if p.DescriptionEn != nil {
		row.DescriptionEn = *p.DescriptionEn
	}
	if p.Identity != nil {
		row.Identity = *p.Identity
	}
	if p.IdentityEn != nil {
		row.IdentityEn = *p.IdentityEn
	}
	if p.SystemPrompt != nil {
		row.SystemPrompt = *p.SystemPrompt
	}
	if p.SystemPromptEn != nil {
		row.SystemPromptEn = *p.SystemPromptEn
	}
	if p.Icon != nil {
		row.Icon = *p.Icon
	}
	if p.Color != nil {
		row.Color = *p.Color
	}
	if p.SkillIDs != nil {
		if b, err := json.Marshal(p.SkillIDs); err == nil {
			row.SkillIDs = string(b)
		}
	}
	if p.Enabled != nil {
		row.Enabled = *p.Enabled
	}
	if p.SortOrder != nil {
		row.SortOrder = *p.SortOrder
	}

	if err := h.AgentStore.Update(ctx, row); err != nil {
		return errorResp(id, CodeInternalError, "agent update", err)
	}
	return successResp(id, UpdateAgentResult{Agent: toAgentWire(row)})
}

// DeleteAgentParams is the JSON-RPC params for agent.delete_agent.
type DeleteAgentParams struct {
	AgentID string `json:"agentId"`
}

// DeleteAgentResult is the JSON-RPC result for agent.delete_agent.
type DeleteAgentResult struct {
	Deleted bool `json:"deleted"`
}

func handleDeleteAgent(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p DeleteAgentParams
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.AgentID == "" {
		return errorResp(id, CodeInvalidParams, "agentId is required", nil)
	}
	if h.AgentStore == nil {
		return errorResp(id, CodeInternalError, "agent store not configured", nil)
	}
	row, err := h.AgentStore.GetByID(ctx, p.AgentID)
	if err != nil {
		return errorResp(id, CodeInternalError, "agent get", err)
	}
	if row.Source == "preset" || row.IsDefault {
		return errorResp(id, CodeInvalidParams, "preset / default agents cannot be deleted", nil)
	}
	if err := h.AgentStore.Delete(ctx, p.AgentID); err != nil {
		return errorResp(id, CodeInternalError, "agent delete", err)
	}
	return successResp(id, DeleteAgentResult{Deleted: true})
}

// UpdateDefaultAgentParams is the JSON-RPC params for
// agent.update_default_agent. defaultAgentId must resolve to an agent in
// the same workspace; empty is refused (a workspace always keeps a
// default).
type UpdateDefaultAgentParams struct {
	WorkspaceID    string `json:"workspaceId"`
	DefaultAgentID string `json:"defaultAgentId"`
}

// UpdateDefaultAgentResult is the JSON-RPC result for
// agent.update_default_agent.
type UpdateDefaultAgentResult struct {
	Workspace WorkspaceWire `json:"workspace"`
}

func handleUpdateDefaultAgent(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p UpdateDefaultAgentParams
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.WorkspaceID == "" || p.DefaultAgentID == "" {
		return errorResp(id, CodeInvalidParams, "workspaceId and defaultAgentId are required", nil)
	}
	if h.WorkspaceStore == nil || h.AgentStore == nil {
		return errorResp(id, CodeInternalError, "stores not configured", nil)
	}
	if _, err := h.WorkspaceStore.GetByID(ctx, p.WorkspaceID); err != nil {
		return errorResp(id, CodeInternalError, "workspace lookup", err)
	}
	agent, err := h.AgentStore.GetByID(ctx, p.DefaultAgentID)
	if err != nil {
		return errorResp(id, CodeInternalError, "agent lookup", err)
	}
	if agent.WorkspaceID != p.WorkspaceID {
		return errorResp(id, CodeInvalidParams, "agent does not belong to this workspace", nil)
	}

	prev, prevErr := h.AgentStore.GetDefaultForWorkspace(ctx, p.WorkspaceID)
	if prevErr == nil && prev.ID == agent.ID {
		// already the default — idempotent no-op
	} else {
		if prevErr == nil {
			prev.IsDefault = false
			if err := h.AgentStore.Update(ctx, prev); err != nil {
				return errorResp(id, CodeInternalError, "agent unmark previous default", err)
			}
		}
		agent.IsDefault = true
		if err := h.AgentStore.Update(ctx, agent); err != nil {
			return errorResp(id, CodeInternalError, "agent mark default", err)
		}
		if err := h.WorkspaceStore.UpdateDefaultAgent(ctx, p.WorkspaceID, p.DefaultAgentID); err != nil {
			return errorResp(id, CodeInternalError, "workspace update default agent", err)
		}
	}

	row, err := h.WorkspaceStore.GetByID(ctx, p.WorkspaceID)
	if err != nil {
		return errorResp(id, CodeInternalError, "workspace reload", err)
	}
	n, err := h.WorkspaceStore.CountSessions(ctx, p.WorkspaceID)
	if err != nil {
		return errorResp(id, CodeInternalError, "workspace session count", err)
	}
	return successResp(id, UpdateDefaultAgentResult{Workspace: toWorkspaceWire(row, n)})
}
