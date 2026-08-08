// Runs a skill through the model with its system prompt and tool set.

package skills

import (
	"context"
	"time"

	tool "darvin-cowork/backend/internal/tools"
)

type SkillExecutionContext struct {
	Skill        *SkillEntry
	SystemPrompt string
	Args         string
	Tools        []tool.Tool
	StartedAt    time.Time
}

type SkillRunner struct {
	reg     *SkillRegistry
	toolReg *tool.Registry
}

func NewSkillRunner(reg *SkillRegistry, toolReg *tool.Registry) *SkillRunner {
	return &SkillRunner{reg: reg, toolReg: toolReg}
}

func (r *SkillRunner) ExecuteByID(ctx context.Context, id string, args string) (*SkillExecutionContext, error) {
	return r.executeByIDWithRegistry(ctx, id, args, r.reg)
}

func (r *SkillRunner) ExecuteByUserInvocation(ctx context.Context, id string, args string) (*SkillExecutionContext, error) {
	return r.executeByUserInvocationWithRegistry(ctx, id, args, r.reg)
}

func (r *SkillRunner) executeByIDWithRegistry(ctx context.Context, id, args string, reg *SkillRegistry) (*SkillExecutionContext, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, ok := reg.Get(id)
	if !ok {
		return nil, ErrSkillNotFound
	}
	if !entry.Enabled {
		return nil, ErrSkillDisabled
	}
	tools := r.toolsForSkill(entry)
	return &SkillExecutionContext{
		Skill:        entry,
		SystemPrompt: entry.Prompt,
		Args:         args,
		Tools:        tools,
		StartedAt:    time.Now(),
	}, nil
}

func (r *SkillRunner) executeByUserInvocationWithRegistry(ctx context.Context, id, args string, reg *SkillRegistry) (*SkillExecutionContext, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, ok := reg.Get(id)
	if !ok {
		return nil, ErrSkillNotFound
	}
	if !entry.Enabled {
		return nil, ErrSkillDisabled
	}
	if !entry.UserInvocable {
		return nil, ErrSkillNotUserInvocable
	}
	tools := r.toolsForSkill(entry)
	return &SkillExecutionContext{
		Skill:        entry,
		SystemPrompt: entry.Prompt,
		Args:         args,
		Tools:        tools,
		StartedAt:    time.Now(),
	}, nil
}

func (r *SkillRunner) toolsForSkill(entry *SkillEntry) []tool.Tool {
	if r.toolReg == nil {
		return nil
	}
	if entry.DisableModelInvocation {
		return nil
	}
	// Skill-tool filtering will be added later; for now we surface all
	// currently registered tools to the execution context.
	return r.toolReg.ToolsForSkill(entry.ID)
}
