package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/tools"
)

// SkillPlugin exposes enabled skills as KindSkill tools named skill__<id>.
// Register is meant to be re-run after the skill registry changes so the
// tool surface stays in sync with the enabled set.
type SkillPlugin struct {
	pluginID string
	registry *SkillRegistry
	runner   *SkillRunner
}

// NewSkillPlugin builds a plugin over the given skill registry. runner is
// optional; when nil SkillTool.Execute reports the skill as resolved
// without executing the runner.
func NewSkillPlugin(reg *SkillRegistry, runner *SkillRunner) *SkillPlugin {
	return &SkillPlugin{pluginID: "skill", registry: reg, runner: runner}
}

// PluginID returns the owning identifier used for UnregisterByPlugin.
func (p *SkillPlugin) PluginID() string { return p.pluginID }

// SetBootstrapResult swaps the registry + runner the plugin registers
// tools from. Workspace switches re-bootstrap project skills and update
// the plugin in place; RefreshAllTools re-runs Register afterwards.
func (p *SkillPlugin) SetBootstrapResult(res *BootstrapResult) {
	if res == nil {
		return
	}
	p.registry = res.Registry
	p.runner = res.Runner
}

// Register adds one SkillTool per enabled skill.
func (p *SkillPlugin) Register(reg tool.ToolRegistrar) error {
	for _, entry := range p.registry.ListEnabled() {
		t := &SkillTool{skillEntry: entry, runner: p.runner}
		if err := reg.RegisterTool(t, tool.KindSkill, map[string]any{
			"pluginID": p.pluginID,
			"skillID":  entry.ID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Unregister removes every skill tool this plugin owns.
func (p *SkillPlugin) Unregister(reg tool.ToolRegistrar) error {
	return reg.UnregisterByPlugin(p.pluginID)
}

// SkillTool adapts a skill entry to the Tool interface. Execute resolves
// the skill through the runner and returns a summary; driving the skill's
// own tools is the caller's responsibility.
type SkillTool struct {
	skillEntry *SkillEntry
	runner     *SkillRunner
}

// skillToolName maps a skill ID to its tool name; double-underscore keeps
// the name in [a-zA-Z0-9_-] so Anthropic accepts it.
func skillToolName(skillID string) string { return "skill__" + skillID }

func (t *SkillTool) Name() string        { return skillToolName(t.skillEntry.ID) }
func (t *SkillTool) Description() string { return t.skillEntry.Description }

func (t *SkillTool) Parameters() json.RawMessage {
	return tool.MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"args": {Type: "string", Description: "Free-form arguments passed to the skill"},
		},
	})
}

// Execute resolves the skill and reports the resolved context; errors
// surface as an IsError result.
func (t *SkillTool) Execute(ctx context.Context, args map[string]any) tool.Result {
	meta := map[string]any{"skillID": t.skillEntry.ID}
	if t.runner == nil {
		return tool.Result{Content: fmt.Sprintf("skill %s resolved (no runner attached)", t.skillEntry.ID), Metadata: meta}
	}
	argStr, _ := args["args"].(string)
	sec, err := t.runner.ExecuteByID(ctx, t.skillEntry.ID, argStr)
	if err != nil {
		return tool.Result{Content: err.Error(), IsError: true, Metadata: meta}
	}
	return tool.Result{
		Content:  fmt.Sprintf("skill %s resolved with %d tools", sec.Skill.ID, len(sec.Tools)),
		Metadata: meta,
	}
}
