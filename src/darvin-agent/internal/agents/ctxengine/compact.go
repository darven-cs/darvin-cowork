package ctxengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"darvin-cowork/backend/internal/agents/protocol"
)

// Compact runs the LLM-based compaction pipeline. It never mutates
// p.Messages — the caller adopts RetainedMessages only on Success.
//
// Prior digests (matched by isCompactionSummary) stay verbatim in the
// retained slice so repeated passes don't recursively re-summarise the
// original summary text (FR-4).
func (a *DefaultAssembler) Compact(ctx context.Context, p CompactParams) CompactResult {
	if err := ctx.Err(); err != nil {
		return CompactResult{
			Success:          false,
			RetainedMessages: p.Messages,
			Reason:           p.Reason,
		}
	}

	a.mu.RLock()
	cfg := a.cfg
	summarizer := a.summarizer
	var modelName string
	if a.deps != nil {
		modelName = a.deps.ModelName()
	}
	a.mu.RUnlock()

	snap := p.Checkpoint
	if snap == nil {
		snap = &CheckPoint{
			ID:         newCheckpointID(),
			CapturedAt: time.Now(),
			Snapshot:   cloneMessages(p.Messages),
		}
	} else {
		snap.Snapshot = cloneMessages(p.Messages)
	}

	tokensBefore := p.LastUsage.PromptTokens
	if tokensBefore <= 0 {
		tokensBefore = estimateMessages(p.Messages)
	}

	if !p.Force && tokensBefore <= p.Budget {
		firstKeptID, firstKeptTS := firstKeptBoundary(p.Messages, len(p.Messages))
		return CompactResult{
			Success:            true,
			TokensBefore:       tokensBefore,
			TokensAfter:        tokensBefore,
			RetainedMessages:   p.Messages,
			FirstKeptID:        firstKeptID,
			FirstKeptTimestamp: firstKeptTS,
			Reason:             p.Reason,
			Checkpoint:         snap,
		}
	}

	if summarizer == nil {
		return CompactResult{
			Success:          false,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensBefore,
			RetainedMessages: p.Messages,
			Checkpoint:       snap,
			Reason:           p.Reason,
		}
	}

	// partitionFold splits messages into (pinned, kept, fold):
	//   pinnedPrefix — leading pinned run (always kept verbatim)
	//   kept        — prior digests + pinnable user turns (verbatim)
	//   fold        — the rest, fed to the summariser
	// Old digests stay out of the LLM summary, so repeated Compact
	// passes don't re-summarise the original summary text (FR-4).
	pinned, kept, fold := partitionFold(p.Messages)

	tail := cfg.CompactTailKeep
	if tail <= 0 {
		tail = 6
	}
	// Honour an explicit CompactTailTokens budget (FR-3): pin as many
	// trailing messages as fit under the budget.
	if cfg.CompactTailTokens > 0 {
		if n := pinnedPrefixLen(fold, cfg.CompactTailTokens, EstimateMessageTokens); n > 0 {
			tail = n
		}
	}
	if tail >= len(p.Messages) {
		tail = len(p.Messages) - 1
		if tail < 0 {
			tail = 0
		}
	}
	// Adjust tail so the slice msgs[len-tail:] does not split a
	// tool_use/tool_result pair — see alignTailBoundary. The pair-aware
	// partitionFold keeps pairs atomic inside fold/kept, but the tail
	// boundary itself can still cut a pair between the summarised span
	// and the verbatim tail. Without this adjustment, an assistant with
	// tool_use may end up in fold while its tool_result lands in tail
	// (or vice versa), producing a 400 in anthropic/convert.go.
	tail = alignTailBoundary(p.Messages, tail)

	if len(fold) == 0 {
		return CompactResult{
			Success:          false,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensBefore,
			RetainedMessages: p.Messages,
			Checkpoint:       snap,
			Reason:           p.Reason,
		}
	}

	summaryText, err := summarizer.Summarize(ctx, SummarizeRequest{
		Model:     modelName,
		Messages:  fold,
		Hint:      "conversational summary; preserve tool input/output facts and decisions",
		MaxTokens: cfg.SummarizeMaxTokens,
	})
	if err != nil {
		return CompactResult{
			Success:          false,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensBefore,
			RetainedMessages: p.Messages,
			Checkpoint:       snap,
			Reason:           p.Reason,
		}
	}

	summaryMsg := protocol.Message{
		Role: protocol.RoleAssistant,
		Content: "[Conversation Summary]\n" + summaryText +
			fmt.Sprintf("\n\n(Compacted at %s; original %d messages → tail %d messages)",
				time.Now().Format(time.RFC3339), len(p.Messages)-tail, tail),
	}

	// retained = pinnedPrefix + kept (old digests / pinnable) + summaryMsg + tail
	newMessages := make([]protocol.Message, 0, len(pinned)+len(kept)+1+tail)
	newMessages = append(newMessages, pinned...)
	newMessages = append(newMessages, kept...)
	newMessages = append(newMessages, summaryMsg)
	newMessages = append(newMessages, p.Messages[len(p.Messages)-tail:]...)
	tokensAfter := estimateMessages(newMessages)

	firstKeptID, firstKeptTS := firstKeptBoundary(p.Messages, tail)

	retries := 0
	for p.Budget > 0 && tokensAfter > p.Budget && retries < cfg.CompactMaxRetries {
		half := len(fold) / 2
		if half == 0 {
			break
		}
		fold = fold[:half]
		newSpan, err := summarizer.Summarize(ctx, SummarizeRequest{
			Model:     modelName,
			Messages:  fold,
			Hint:      "compress further",
			MaxTokens: cfg.SummarizeMaxTokens,
		})
		if err != nil {
			break
		}
		summaryMsg.Content = "[Conversation Summary]\n" + newSpan +
			fmt.Sprintf("\n\n(Recompacted %d times)", retries+1)
		newMessages = make([]protocol.Message, 0, len(pinned)+len(kept)+1+tail)
		newMessages = append(newMessages, pinned...)
		newMessages = append(newMessages, kept...)
		newMessages = append(newMessages, summaryMsg)
		newMessages = append(newMessages, p.Messages[len(p.Messages)-tail:]...)
		tokensAfter = estimateMessages(newMessages)
		retries++
	}

	if p.Budget > 0 && tokensAfter > p.Budget {
		return CompactResult{
			Success:          false,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensAfter,
			RetainedMessages: p.Messages,
			Checkpoint:       snap,
			Reason:           p.Reason,
		}
	}

	return CompactResult{
		Success:            true,
		TokensBefore:       tokensBefore,
		TokensAfter:        tokensAfter,
		RetainedMessages:   newMessages,
		Summary:            summaryMsg.Content,
		FirstKeptID:        firstKeptID,
		FirstKeptTimestamp: firstKeptTS,
		Reason:             p.Reason,
		Checkpoint:         snap,
	}
}

// DefaultSummarizer is the default Summarizer: it wraps
// protocol.ModelProvider.Complete with a summarisation-specific system prompt.
// It deliberately does not reuse Agent.Session or Agent.EventBus so the
// summary call does not pollute the agent's own conversation state.
type DefaultSummarizer struct {
	provider protocol.ModelProvider
}

// NewDefaultSummarizer constructs the default Summarizer. Provider may be
// nil — Summarize returns an error in that case.
func NewDefaultSummarizer(provider protocol.ModelProvider) *DefaultSummarizer {
	return &DefaultSummarizer{provider: provider}
}

// Summarize sends req.Messages to the provider with a summariser system
// prompt and returns the assistant content.
func (s *DefaultSummarizer) Summarize(ctx context.Context, req SummarizeRequest) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("ctxengine: summarizer provider is nil")
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 800
	}
	system := "You are a conversation summarizer. Output a concise summary preserving tool inputs/outputs, decisions, and context that future turns might need. " + req.Hint
	resp, err := s.provider.Complete(ctx, &protocol.CompletionRequest{
		Model:     req.Model,
		Messages:  req.Messages,
		System:    system,
		MaxTokens: maxTokens,
		Stream:    false,
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// estimateMessages sums EstimateMessageTokens over a slice.
func estimateMessages(msgs []protocol.Message) int {
	n := 0
	for _, m := range msgs {
		n += EstimateMessageTokens(m)
	}
	return n
}

func newCheckpointID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("cp-%d", time.Now().UnixNano())
	}
	return "cp-" + hex.EncodeToString(b[:])
}

// partitionFold splits msgs into (pinnedPrefix, kept, fold).
// `kept` contains prior digests — messages that MUST survive verbatim
// — and `fold` is everything else, fed to the summariser.
//
// Tool-use / tool-result pairs are kept atomic: an assistant with
// ToolCalls and the immediately following tool{ToolCallID ∈
// ToolCalls.ID} messages always share a bucket so the summariser
// never eats a tool_use without its results (or vice versa).
// Anthropic's wire format requires every tool_result block to have a
// corresponding tool_use in the immediately preceding assistant
// message; splitting a pair produces an orphan that fails the 400
// check in anthropic/convert.go.
func partitionFold(msgs []protocol.Message) (pinnedPrefix, kept, fold []protocol.Message) {
	// Pinned prefix is intentionally empty today; the hook exists for
	// future expansion (system-pinned messages, etc.).
	pinnedPrefix = nil
	kept = make([]protocol.Message, 0, len(msgs))
	fold = make([]protocol.Message, 0, len(msgs))
	for i := 0; i < len(msgs); {
		size := pairAwareGroupSize(msgs, i)
		m := msgs[i]
		if isCompactionSummary(m) || isPinnableUserTurn(m) {
			kept = append(kept, msgs[i:i+size]...)
		} else {
			fold = append(fold, msgs[i:i+size]...)
		}
		i += size
	}
	return pinnedPrefix, kept, fold
}

// pairAwareGroupSize returns the number of consecutive messages
// starting at index i that must stay together as a tool_use/tool_result
// pair. When msgs[i] is an assistant with ToolCalls, the group extends
// forward to include every immediately following
// tool{ToolCallID ∈ ToolCalls.ID} message; otherwise the group is just
// msgs[i] (size 1).
func pairAwareGroupSize(msgs []protocol.Message, i int) int {
	if i < 0 || i >= len(msgs) {
		return 0
	}
	m := msgs[i]
	if m.Role != protocol.RoleAssistant || len(m.ToolCalls) == 0 {
		return 1
	}
	ids := make(map[string]struct{}, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		ids[tc.ID] = struct{}{}
	}
	end := i + 1
	for end < len(msgs) {
		n := msgs[end]
		if n.Role != protocol.RoleTool {
			break
		}
		if _, ok := ids[n.ToolCallID]; !ok {
			break
		}
		end++
	}
	return end - i
}

// isCompactionSummary matches the "[Conversation Summary]\n..." prefix
// both the assembler and the manual compact handler emit. Keeping
// old digests out of the next summary pass is the FR-4 contract.
func isCompactionSummary(m protocol.Message) bool {
	if m.Role != protocol.RoleAssistant {
		return false
	}
	return strings.HasPrefix(m.Content, "[Conversation Summary]\n")
}

// isPinnableUserTurn is a future hook. Today the heuristic is
// disabled — every user turn goes through the normal summarise path
// — so partitionFold's "kept" set is exactly the prior digests.
func isPinnableUserTurn(m protocol.Message) bool {
	return false
}

// pinnedPrefixLen returns the largest prefix length of msgs whose
// summed estimate does not exceed budgetTokens. Returns 0 when the
// first message already blows the budget.
func pinnedPrefixLen(msgs []protocol.Message, budgetTokens int, estimate func(protocol.Message) int) int {
	if budgetTokens <= 0 || len(msgs) == 0 {
		return 0
	}
	used := 0
	for i, m := range msgs {
		cost := estimate(m)
		if used+cost > budgetTokens && i > 0 {
			return i
		}
		used += cost
	}
	return len(msgs)
}

// firstKeptBoundary returns the (ID, Timestamp) of the first message
// in the trailing tail. Persisted on CompactResult so hydrate.go can
// slice the messages table at the same boundary on the next launch.
func firstKeptBoundary(msgs []protocol.Message, tail int) (string, int64) {
	if tail <= 0 || len(msgs) == 0 {
		return "", 0
	}
	idx := len(msgs) - tail
	if idx < 0 {
		idx = 0
	}
	m := msgs[idx]
	return m.ID, m.Timestamp
}

// alignTailBoundary returns the smallest tail length ≥ requestedTail
// such that the slice msgs[len-tail:] does not split a
// tool_use/tool_result pair:
//
//   - Start cut (proposed tail[0] is a tool message): extend backward
//     to include the matching tool_use assistant so the tool_result's
//     tool_use_id is present in the immediately preceding assistant.
//   - End cut (proposed tail[-1] is an assistant with unpaired
//     ToolCalls whose results are not all in tail): shrink forward so
//     no orphan tool_use lands at the tail end.
//
// The returned length may exceed requestedTail when the start must be
// extended; the caller's retry loop will recompress if that pushes the
// result over budget.
func alignTailBoundary(msgs []protocol.Message, requestedTail int) int {
	if requestedTail <= 0 || len(msgs) == 0 {
		return 0
	}
	start := len(msgs) - requestedTail
	if start < 0 {
		start = 0
	}
	end := len(msgs)

	// Start-cut: walk backward over orphan tool messages, each time
	// pulling its tool_use assistant into the tail.
	for start > 0 && msgs[start].Role == protocol.RoleTool {
		tcID := msgs[start].ToolCallID
		matched := -1
		for j := start - 1; j >= 0; j-- {
			if msgs[j].Role != protocol.RoleAssistant {
				continue
			}
			for _, tc := range msgs[j].ToolCalls {
				if tc.ID == tcID {
					matched = j
					break
				}
			}
			if matched >= 0 {
				break
			}
		}
		if matched < 0 {
			// No matching tool_use in msgs; orphan tool we cannot
			// resolve by extending. Leave it alone — partitionFold's
			// pair-aware grouping will keep it atomic with any matching
			// pair inside fold, and the convert-side check rejects only
			// tool_result without preceding tool_use in the same wire
			// frame, which we cannot satisfy without dropping the tool.
			break
		}
		start = matched
	}

	// End-cut: shrink forward while the last assistant has ToolCalls
	// that are not all paired with tool results inside the tail span.
	for end > start {
		last := msgs[end-1]
		if last.Role != protocol.RoleAssistant || len(last.ToolCalls) == 0 {
			break
		}
		if allToolCallsHaveResults(msgs[start:end], last.ToolCalls) {
			break
		}
		end--
	}

	return end - start
}

// allToolCallsHaveResults reports whether every ToolCall.ID in calls
// has a matching tool{ToolCallID=id} somewhere within span.
func allToolCallsHaveResults(span []protocol.Message, calls []protocol.ToolCall) bool {
	if len(calls) == 0 {
		return true
	}
	seen := make(map[string]struct{}, len(calls))
	for _, m := range span {
		if m.Role == protocol.RoleTool && m.ToolCallID != "" {
			seen[m.ToolCallID] = struct{}{}
		}
	}
	for _, tc := range calls {
		if _, ok := seen[tc.ID]; !ok {
			return false
		}
	}
	return true
}
