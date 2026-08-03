package agent

import (
	"context"
	"errors"

	"darvin-cowork/backend/internal/tools"
)

// RunSkillSession drives one user-invoked skill turn through the same
// executor as a normal prompt, but with the skill's SKILL.md body as the
// system prompt and a registry scoped to the skill's allowed tools. The
// user message is the raw `/skill-name args` command, so the persisted
// message matches the renderer bubble.
//
// Errors from the underlying Run are already emitted as AgentErrorEvent by
// the dispatcher; only a rejected enqueue (e.g. session busy) surfaces here
// so the caller (acp.Loop) can emit an explicit error event.
func (a *Agent) RunSkillSession(ctx context.Context, systemPrompt, userContent string, skillTools []tool.Tool) error {
	a.runMu.Lock()
	running := a.state == stateRunning
	a.runMu.Unlock()
	if running {
		return errors.New("agent: Run already in progress")
	}

	a.runSkillPrompt = systemPrompt
	a.runSkillTools = buildSkillTools(a.tools, skillTools)
	defer func() {
		a.runSkillPrompt = ""
		a.runSkillTools = nil
	}()

	if err := a.Prompt(ctx, userContent, nil, nil); err != nil {
		return err
	}
	_ = a.Run(ctx)
	return nil
}

// buildSkillTools projects the skill's allowed tools into a fresh registry,
// preserving each entry's kind/metadata so the executor's event attribution
// (toolKind / skillId / mcpServerId) stays intact. An empty allowed set
// yields an empty registry — the skill is not allowed to call tools, so the
// LLM answers from the skill prompt alone.
func buildSkillTools(full *tool.Registry, allowed []tool.Tool) *tool.Registry {
	if full == nil {
		return tool.NewRegistry()
	}
	names := make(map[string]struct{}, len(allowed))
	for _, t := range allowed {
		if t != nil {
			names[t.Name()] = struct{}{}
		}
	}
	reg := tool.NewRegistry()
	for _, e := range full.List() {
		if _, ok := names[e.Tool.Name()]; ok {
			// RegisterTool on an empty registry never collides; ignore the
			// returned ErrAlreadyRegistered defensively.
			_ = reg.RegisterTool(e.Tool, e.Kind, e.Metadata)
		}
	}
	return reg
}
