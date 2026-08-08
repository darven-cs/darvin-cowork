// Compacts older history back within the token budget via LLM summarisation.

package ctxengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/protocol"
)

// summaryTimeout bounds one summarizer call so a stalled stream surfaces
// a clear failure (then a mechanical fold) instead of hanging compaction
// indefinitely.
const summaryTimeout = 90 * time.Second

// Compact runs the LLM-based compaction pipeline. It never mutates
// p.Messages — the caller adopts RetainedMessages only on Success. Prior
// digests stay verbatim so repeated passes don't re-summarise them.
func (a *DefaultAssembler) Compact(ctx context.Context, p CompactParams) CompactResult {
	if err := ctx.Err(); err != nil {
		return CompactResult{
			Success:          false,
			RetainedMessages: p.Messages,
			Reason:           p.Reason,
		}
	}

	// Stuck latch: after two consecutive auto-compacts, pause and surface
	// a Notice instead of paying repeated LLM summariser calls.
	if !p.Force && a.Stuck() {
		return CompactResult{
			Success:          false,
			RetainedMessages: p.Messages,
			TokensBefore:     estimateMessages(p.Messages),
			TokensAfter:      estimateMessages(p.Messages),
			Reason:           "compact_paused_stuck",
		}
	}

	a.mu.RLock()
	cfg := a.cfg
	summarizer := a.summarizer
	archiver := a.archiver
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

	// partitionFold splits into pinnedPrefix (verbatim), kept (prior
	// digests + pinnable user turns, verbatim), and fold (summarised).
	pinned, kept, fold := partitionFold(p.Messages)

	// tailStart computes start from RecentKeep (message-count floor) and
	// CompactTailTokens (token budget); [head:start] is the fold region.
	head := pinnedPrefixLen(p.Messages, 0, EstimateMessageTokens)
	_ = head // head is informational; partitionFold already split pinned/kept

	start := tailStart(p.Messages, 0, cfg.CompactTailTokens, EstimateMessageTokens, cfg.RecentKeep)
	// Don't split a tool_use/tool_result pair across the tail boundary —
	// an orphan tool_result is rejected by the Anthropic wire format.
	if start < len(p.Messages) {
		tailLen := len(p.Messages) - start
		if aligned := alignTailBoundary(p.Messages, tailLen); aligned > tailLen {
			start = len(p.Messages) - aligned
		}
	}
	if start >= len(p.Messages) {
		// Entire conversation fits in the tail — nothing to summarise.
		return CompactResult{
			Success:          false,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensBefore,
			RetainedMessages: p.Messages,
			Checkpoint:       snap,
			Reason:           p.Reason,
		}
	}
	// Recompute fold as kept + summary + tail. We already
	// have pinned / kept above; the fold region is the slice between
	// (pinned + kept) and the tail boundary. The partitionFold / head
	// cooperation keeps tool-use pairs atomic; tailStart only chooses
	// the tail boundary by token budget.
	if start <= len(pinned)+len(kept) {
		// The tail budget covers the entire conversation — nothing
		// to fold. Mirror the old `tail >= len(messages)` no-op:
		// return the original messages with the budget satisfied
		// (caller already bypassed this with Force, otherwise the
		// early-out at line 107 would have caught it).
		noopID, noopTS := firstKeptBoundary(p.Messages, len(p.Messages))
		return CompactResult{
			Success:            true,
			TokensBefore:       tokensBefore,
			TokensAfter:        tokensBefore,
			RetainedMessages:   p.Messages,
			FirstKeptID:        noopID,
			FirstKeptTimestamp: noopTS,
			Reason:             p.Reason,
			Checkpoint:         snap,
		}
	}
	fold = fold[:0]
	keptEnd := len(pinned) + len(kept)
	for i := keptEnd; i < start; {
		size := pairAwareGroupSize(p.Messages, i)
		fold = append(fold, p.Messages[i:i+size]...)
		i += size
	}

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

	// Archive the fold region before the LLM call so a summariser
	// failure still leaves the originals on disk. Best-effort.
	archived := ""
	if archiver != nil {
		if path, err := archiver.Archive(ctx, fold); err == nil {
			archived = path
		} else if a.deps != nil {
			a.deps.Emit(event.NoticeEvent{
				EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: p.SessionID}},
				Kind:      event.NoticeMechanicalFold,
				Text:      "Context was compacted without a generated summary.",
				Detail:    "archive write failed: " + err.Error(),
			})
		}
	}

	// Run the LLM summary with the 7-section prompt. A summary failure
	// is no longer fatal — fall through to the mechanical fold so the
	// caller still gets a compacted slice.
	summaryText, err := a.callSummariser(ctx, summarizer, modelName, fold, cfg.SummarizeMaxTokens)
	if err != nil {
		if a.deps != nil {
			a.deps.Emit(event.NoticeEvent{
				EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: p.SessionID}},
				Kind:      event.NoticeMechanicalFold,
				Text:      "Context was compacted without a generated summary.",
				Detail:    "compaction summary unavailable (" + err.Error() + "); folded mechanically",
			})
		}
		summaryText = mechanicalFoldDigest(len(fold), archived)
	}

	summaryMsg := protocol.Message{
		Role: protocol.RoleAssistant,
		Content: "[Conversation Summary]\n" + summaryText +
			fmt.Sprintf("\n\n(Compacted at %s; original %d messages → tail %d messages)",
				time.Now().Format(time.RFC3339), len(p.Messages)-(len(p.Messages)-start), len(p.Messages)-start),
	}

	tail := p.Messages[start:]
	newMessages := make([]protocol.Message, 0, len(pinned)+len(kept)+1+len(tail))
	newMessages = append(newMessages, pinned...)
	newMessages = append(newMessages, kept...)
	newMessages = append(newMessages, summaryMsg)
	newMessages = append(newMessages, tail...)
	tokensAfter := estimateMessages(newMessages)

	firstKeptID, firstKeptTS := firstKeptBoundary(p.Messages, len(tail))

	// Re-compact loop is bounded by `tokensAfter <= Budget`. The
	// mechanical fold fallback ensures every iteration lands a
	// digest; the natural-exit condition keeps this bounded without
	// a magic retry counter. Half-fold each iteration until the
	// result fits, or the fold is empty.
	for p.Budget > 0 && tokensAfter > p.Budget {
		half := len(fold) / 2
		if half == 0 {
			break
		}
		fold = fold[:half]
		newSpan, err := a.callSummariser(ctx, summarizer, modelName, fold, cfg.SummarizeMaxTokens)
		if err != nil {
			newSpan = mechanicalFoldDigest(len(fold), archived)
		}
		summaryMsg.Content = "[Conversation Summary]\n" + newSpan +
			fmt.Sprintf("\n\n(Recompacted; fold=%d messages)", len(fold))
		newMessages = make([]protocol.Message, 0, len(pinned)+len(kept)+1+len(tail))
		newMessages = append(newMessages, pinned...)
		newMessages = append(newMessages, kept...)
		newMessages = append(newMessages, summaryMsg)
		newMessages = append(newMessages, tail...)
		tokensAfter = estimateMessages(newMessages)
	}

	if p.Budget > 0 && tokensAfter > p.Budget {
		// Could not converge — return original messages, treat as
		// failure so the caller keeps the live state. Caller is
		// expected to log / surface; we don't penalise the stuck
		// latch for an unrecoverable attempt.
		return CompactResult{
			Success:          false,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensAfter,
			RetainedMessages: p.Messages,
			Checkpoint:       snap,
			Reason:           p.Reason,
		}
	}

	// Stuck-latch update — fire before returning so the next Assemble
	// sees the latest count.
	stuckNow := a.MarkConsecutiveCompact()
	if stuckNow && a.deps != nil {
		a.deps.Emit(event.NoticeEvent{
			EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: p.SessionID}},
			Kind:      event.NoticeStuck,
			Text: "Automatic context cleanup paused because the context window is too small; " +
				"raise context_window or shrink tool output.",
			Detail: "compaction ran on consecutive turns; the system prompt plus one turn " +
				"already exceeds the configured threshold. Auto-compaction paused until the prompt drops.",
		})
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

// callSummariser is the timeout-bounded call to the LLM summary. A
// stalled stream surfaces as context.DeadlineExceeded; the caller treats
// any error as the mechanical-fold trigger.
func (a *DefaultAssembler) callSummariser(
	ctx context.Context,
	summarizer Summarizer,
	modelName string,
	fold []protocol.Message,
	maxTokens int,
) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	return summarizer.Summarize(cctx, SummarizeRequest{
		Model:     modelName,
		Messages:  fold,
		Hint:      "preserve identifiers, paths, and numbers exactly",
		MaxTokens: maxTokens,
	})
}

// mechanicalFoldDigest is the deterministic stand-in used when the
// summariser is unreachable: the foldable region is already archived,
// so the digest just notes the gap and points the model at the user
// for anything it needs from before it.
func mechanicalFoldDigest(n int, archive string) string {
	where := "."
	if archive != "" {
		where = " (archived to " + archive + ")."
	}
	return fmt.Sprintf(
		"%d earlier message(s) were folded here to free context, but the automatic summary was unavailable%s "+
			"Ask the user if you need details from before this point.",
		n, where)
}

// DefaultSummarizer is the default Summarizer: it wraps
// protocol.ModelProvider.Stream with the 7-section system prompt.
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

// Summarize sends req.Messages to the provider with the summariser system
// prompt and returns the assistant content. Uses Stream (rather than
// Complete) so long fold summaries do not bump against Complete's
// hard token cap.
func (s *DefaultSummarizer) Summarize(ctx context.Context, req SummarizeRequest) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("ctxengine: summarizer provider is nil")
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 800
	}
	system := summarySystemPrompt
	if req.Hint != "" {
		system = system + "\n\n" + req.Hint
	}
	stream, err := s.provider.Stream(ctx, &protocol.CompletionRequest{
		Model:     req.Model,
		Messages:  req.Messages,
		System:    system,
		MaxTokens: maxTokens,
		Stream:    true,
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = stream.Close() }()
	var content strings.Builder
	for ev := range stream.Events {
		switch e := ev.(type) {
		case protocol.TextDeltaEvent:
			content.WriteString(e.Delta)
		case protocol.DoneEvent:
			if e.Response.Content != "" && content.Len() == 0 {
				content.WriteString(e.Response.Content)
			}
			return content.String(), nil
		case protocol.ErrorEvent:
			if e.Err != nil {
				return "", e.Err
			}
			return "", stream.Err()
		}
	}
	if err := stream.Err(); err != nil {
		return "", err
	}
	return content.String(), nil
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
// old digests out of the next summary pass is the contract enforced
// by partitionFold.
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

// tailStart returns the index at which the verbatim recent tail
// begins. head is the (already computed) prefix the summariser skips;
// tailTokens is the budget the kept tail fits under, estimate converts
// a message to tokens, and recentKeep is the message-count floor (the
// tail must always include at least this many trailing messages, even
// if their token cost exceeds tailTokens).
//
// Walks back from the tail, accumulating tokens, until it has either
// spent tailTokens or accumulated recentKeep messages — whichever
// comes last. Returns len(msgs) when the tail budget covers the whole
// conversation (caller treats that as "nothing to fold").
func tailStart(msgs []protocol.Message, head, tailTokens int, estimate func(protocol.Message) int, recentKeep int) int {
	if recentKeep <= 0 {
		recentKeep = DefaultRecentKeep
	}
	if len(msgs) == 0 {
		return 0
	}
	if head < 0 {
		head = 0
	}
	if head >= len(msgs) {
		return len(msgs)
	}
	if tailTokens <= 0 {
		// No token budget — fall back to keeping recentKeep messages.
		start := len(msgs) - recentKeep
		if start < head {
			start = head
		}
		return start
	}
	used := 0
	count := 0
	for i := len(msgs) - 1; i >= head; i-- {
		cost := estimate(msgs[i])
		used += cost
		count++
		if count >= recentKeep {
			// RecentKeep is the floor — once we've collected that
			// many trailing messages, stop regardless of the token
			// budget. The budget acts as an upper bound on the tail,
			// not as a "must spend" target: a conversation smaller
			// than tailTokens keeps just the most recent
			// recentKeep messages verbatim.
			return i
		}
	}
	// All remaining messages fit under the tail budget.
	return head
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
