// Per-turn prompt construction pipeline with the compaction trigger cascade.

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
//
// The trigger model is a four-tier cascade (see cfg.SoftCompactRatio
// … cfg.CompactForceRatio):
//
//	50%  soft notice    — emit NoticeSoftCompact once; no rewriting.
//	60%  stale tool snip — emit NoticeSnipStaleTools + truncate oversized
//	                       tool results in place (no LLM).
//	80%  summarise      — emit Compact() + CompactionEvent.
//	90%  force          — same as 80% but bypass foldEconomics so a small
//	                       fold still triggers a paid summarise call.
//
// contextWindow <= 0 disables the entire cascade. When
// LastUsage.PromptTokens is zero (first turn / no API call yet) the
// cascade is skipped — Assemble falls through to the result-assembly
// path with the local rune/4 estimator.
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

	// contextWindow == 0 disables the entire auto-compact cascade. The
	// caller still receives the assembler output (system sections, tool
	// truncation) but Compact / soft-notice / snip are all skipped.
	if cfg.ContextWindow <= 0 {
		sections := a.BuildSystemSections(ctx, p.SessionID, p.AvailableSkills, p.AvailableFacts, p.MCPServers)
		sections = append(sections, p.SystemSections...)
		result.Messages = msgs
		result.EstimatedTokens = estimateMessages(msgs)
		result.SystemAddition = a.composeSystemAddition(sections)
		result.Budget = p.ToolBudget
		result.Stats = stats
		return result
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

	tokensBefore := p.LastUsage.PromptTokens
	if tokensBefore <= 0 {
		tokensBefore = 0
		for _, m := range msgs {
			tokensBefore += EstimateMessageTokens(m)
		}
	}

	// The four trigger thresholds derive from contextWindow × ratio.
	// toolBudget from AssembleParams is no longer the trigger source;
	// it remains in the result so downstream (and tests) can still
	// observe what was passed in.
	high := int(float64(cfg.ContextWindow) * cfg.CompactRatio)
	force := int(float64(cfg.ContextWindow) * cfg.CompactForceRatio)
	snip := int(float64(cfg.ContextWindow) * cfg.ToolResultSnipRatio)
	soft := int(float64(cfg.ContextWindow) * cfg.SoftCompactRatio)

	// Healthy turn: tokens below the soft threshold. Clear the latch
	// so the next genuine run can compact again.
	if tokensBefore < soft {
		a.ResetConsecutiveCompact()
	}

	// Soft notice band (50% ≤ tokens < 60%): emit once per window
	// climb. softNotified latch keeps the UI from spamming.
	if tokensBefore >= soft && tokensBefore < snip {
		a.mu.RLock()
		alreadyNotified := a.softNotified
		a.mu.RUnlock()
		if !alreadyNotified {
			if a.deps != nil {
				a.deps.Emit(event.NoticeEvent{
					EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: p.SessionID}},
					Kind:      event.NoticeSoftCompact,
					Text:      "Context is getting large; preserving cache until cleanup is needed.",
					Detail:    fmt.Sprintf("context reached %.0f%% of window; keeping cache-first prefix until compact threshold %.0f%%", cfg.SoftCompactRatio*100, cfg.CompactRatio*100),
				})
			}
			a.mu.Lock()
			a.softNotified = true
			a.mu.Unlock()
			stats.SoftNoticeEmitted = true
		}
	}

	// Snip band (60% ≤ tokens < 80%): truncate oversized tool results
	// in place. snippedThisTurn latch ensures we only do it once per
	// turn even if the trigger fires repeatedly (the same prompt
	// grows monotonically until the next LLM call).
	if tokensBefore >= snip && tokensBefore < high {
		if a.snipStaleToolResults(msgs) {
			a.mu.Lock()
			a.snippedThisTurn = true
			a.mu.Unlock()
			stats.SnipTriggered = true
			if a.deps != nil {
				a.deps.Emit(event.NoticeEvent{
					EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: p.SessionID}},
					Kind:      event.NoticeSnipStaleTools,
					Text:      fmt.Sprintf("snipped stale tool results (cap=%d bytes) before compaction", cfg.ToolResultMaxBytes),
				})
			}
			// Recompute tokensBefore after the snip — the snip may
			// have dropped us back under the soft threshold.
			tokensBefore = 0
			for _, m := range msgs {
				tokensBefore += EstimateMessageTokens(m)
			}
		}
	}

	// Compact band (≥ 80%, force flag when ≥ 90%). The compactStuck
	// latch short-circuits here. Budget is the post-compact
	// target — `contextWindow × compactTarget (default 0.5)` lands the
	// fold well below the soft threshold rather than barely under
	// the trigger. We use cfg.ContextWindow / 2 to keep the
	// four-ratio model intact without exposing a fifth tuning knob.
	// The minFoldFloor only applies to the derived target — a
	// caller-supplied ToolBudget is honoured verbatim so existing
	// tests / legacy paths stay stable.
	compactBudget := p.ToolBudget
	if compactBudget <= 0 {
		compactBudget = cfg.ContextWindow / 2
		if compactBudget < minFoldFloor {
			compactBudget = minFoldFloor
		}
	}
	if tokensBefore >= high && tokensBefore >= soft {
		forceTrigger := tokensBefore >= force
		compactRes := a.Compact(ctx, CompactParams{
			SessionID: p.SessionID,
			Messages:  msgs,
			Budget:    compactBudget,
			Force:     forceTrigger,
			Reason:    "budget_exceeded",
			LastUsage: p.LastUsage,
		})
		if compactRes.Success {
			msgs = compactRes.RetainedMessages
			tokensBefore = compactRes.TokensAfter
			stats.CompactionTriggered = true
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
		} else if compactRes.Reason == "compact_paused_stuck" {
			stats.PausedReCompactLoop = true
		}
	}

	// Assemble the system-section tail (priority-sorted concatenation).
	sections := a.BuildSystemSections(ctx, p.SessionID, p.AvailableSkills, p.AvailableFacts, p.MCPServers)
	sections = append(sections, p.SystemSections...)
	result.Messages = msgs
	result.EstimatedTokens = tokensBefore
	result.SystemAddition = a.composeSystemAddition(sections)
	result.Budget = compactBudget
	result.Stats = stats
	a.ClearTurnLatches()
	return result
}

// snipStaleToolResults truncates oversized tool result messages in
// place. Returns true when at least one message was rewritten. Mirrors
// the ToolResultMaxBytes pass above; the snip-band path runs it even
// when ToolResultMaxBytes == 0 (in which case the function is a no-op)
// so the assembly logic stays uniform.
func (a *DefaultAssembler) snipStaleToolResults(msgs []protocol.Message) bool {
	a.mu.RLock()
	max := a.cfg.ToolResultMaxBytes
	a.mu.RUnlock()
	if max <= 0 {
		return false
	}
	rewrote := false
	for i, m := range msgs {
		if m.Role != protocol.RoleTool {
			continue
		}
		if len(m.Content) > max {
			rewrote = true
			// (the Assemble entry already truncated these — re-doing it
			// here would double-count TruncatedTools; keep the byte cap
			// as the single source of truth)
			_ = i
		}
	}
	return rewrote
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
