package agent

import (
	"context"
	"errors"

	"darvin-cowork/backend/internal/agents/protocol"
)

// RunSkillSession drives one user-invoked skill turn through the same
// executor as a normal prompt, but with the skill's SKILL.md body as the
// system prompt and a registry scoped to the skill's allowed tools. The
// user message is the raw `/skill-name args` command, so the persisted
// message matches the renderer bubble.
//
// Errors from the underlying Run are already emitted as AgentErrorEvent by
// the dispatcher; only a rejected enqueue (e.g. session busy) surfaces here
// so the caller (agentloop.Loop) can emit an explicit error event.
func (a *Agent) RunSkillSession(ctx context.Context, systemPrompt, userContent string, skillTools []protocol.Tool) error {
	isRunning := a.controller.IsRunning
	if running := isRunning(); running {
		return errors.New("agent: Run already in progress")
	}

	a.runSkillPrompt = systemPrompt
	a.runSkillTools = a.tools.ScopedForSkill(skillToolNames(skillTools))
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

// skillToolNames extracts the names of the skill's allowed tools; nil
// entries are skipped so a partially-built list cannot panic.
func skillToolNames(tools []protocol.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t != nil {
			names = append(names, t.Name())
		}
	}
	return names
}
