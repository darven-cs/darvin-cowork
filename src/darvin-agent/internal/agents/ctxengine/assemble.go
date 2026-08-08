package ctxengine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/protocol"
)

// Assemble runs the per-turn prompt construction pipeline.
func (a *DefaultAssembler) Assemble(ctx context.Context, p AssembleParams) AssembleResult {
	if err := ctx.Err(); err != nil {
		return AssembleResult{Messages: p.Messages, Budget: p.ToolBudget}
	}

	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()

	msgs := cloneMessages(p.Messages)
	stats := AssembleStats{}
	result := AssembleResult{}

	budget := p.ToolBudget
	if budget <= 0 {
		budget = cfg.TokenBudget
	}

	if cfg.ToolResultMaxBytes > 0 {
		for i, m := range msgs {
			if m.Role != protocol.RoleTool {
				continue
			}
			if len(m.Content) > cfg.ToolResultMaxBytes {
				originalLen := len(m.Content)
				m.Content = m.Content[:cfg.ToolResultMaxBytes] +
					fmt.Sprintf("\n[truncated %d bytes, total %d bytes]",
						originalLen-cfg.ToolResultMaxBytes, originalLen)
				stats.TruncatedTools++
				stats.TruncatedBytes += int64(originalLen - cfg.ToolResultMaxBytes)
				msgs[i] = m
			}
		}
	}

	// Token estimation prefers API-reported promptTokens from previous turn; falls back to local estimator when zero.
	tokensBefore := p.LastUsage.PromptTokens
	if tokensBefore <= 0 {
		tokensBefore = 0
		for _, m := range msgs {
			tokensBefore += EstimateMessageTokens(m)
		}
	}

	if tokensBefore > budget {
		compactRes := a.Compact(ctx, CompactParams{
			SessionID: p.SessionID,
			Messages:  msgs,
			Budget:    budget,
			Reason:    "budget_exceeded",
			LastUsage: p.LastUsage,
		})
		if compactRes.Success {
			msgs = compactRes.RetainedMessages
			tokensBefore = compactRes.TokensAfter
			stats.CompactionTriggered = true
			// Forward the boundary so executor.RunConversation can
			// (a) ReplaceAll the session with compacted messages and
			// (b) hand it to Agent.PersistCompaction for the
			// session_digests write.
			result.CompactSummary = compactRes.Summary
			result.FirstKeptID = compactRes.FirstKeptID
			result.FirstKeptTimestamp = compactRes.FirstKeptTimestamp
			if a.deps != nil {
				a.deps.Emit(event.CompactionEvent{
					EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: p.SessionID}},
					Before:    compactRes.TokensBefore,
					After:     compactRes.TokensAfter,
					Note:      "auto",
				})
			}
		}
	}

	// BuildSystemSections merges identity / soul / user / memory
// bootstrap blocks (sourced from Deps) at the spec priority slots
// (30/40/60/110). Caller-supplied AvailableFacts is honoured verbatim
// when non-empty (test fakes / gateway overrides). p.SystemSections
// merge on top so existing callers / tests see their per-turn
// additions in the final concatenation.
	sections := a.BuildSystemSections(ctx, p.SessionID, p.AvailableSkills, p.AvailableFacts, p.MCPServers)
	sections = append(sections, p.SystemSections...)
	result.Messages = msgs
	result.EstimatedTokens = tokensBefore
	result.SystemAddition = a.composeSystemAddition(sections)
	result.Budget = budget
	result.Stats = stats
	return result
}

// composeSystemAddition merges registered + caller-supplied system
// sections, sorted by Priority ascending, with Config.SystemPromptAddition
// appended at priority 1000. Empty Content is skipped.
func (a *DefaultAssembler) composeSystemAddition(extra []SystemSection) string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	sections := append([]SystemSection{}, a.sections...)
	sections = append(sections, extra...)

	if a.cfg.SystemPromptAddition != "" {
		sections = append(sections, SystemSection{
			Name:     "addition",
			Content:  a.cfg.SystemPromptAddition,
			Priority: 1000,
		})
	}

	sort.SliceStable(sections, func(i, j int) bool {
		return sections[i].Priority < sections[j].Priority
	})

	var sb strings.Builder
	for _, s := range sections {
		if s.Content == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(s.Content)
	}
	return sb.String()
}

// cloneMessages returns a copy with ToolCalls slices re-allocated so caller
// mutations to the returned ToolCalls do not leak back. Argument maps are
// shared by reference (Assemble does not mutate them).
func cloneMessages(msgs []protocol.Message) []protocol.Message {
	out := make([]protocol.Message, len(msgs))
	for i := range msgs {
		m := msgs[i]
		if len(m.ToolCalls) > 0 {
			tcs := make([]protocol.ToolCall, len(m.ToolCalls))
			copy(tcs, m.ToolCalls)
			m.ToolCalls = tcs
		}
		out[i] = m
	}
	return out
}
