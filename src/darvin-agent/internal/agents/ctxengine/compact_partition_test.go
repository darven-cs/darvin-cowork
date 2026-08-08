package ctxengine

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/protocol"
)

func zapNopForTest() *zap.Logger { return zap.NewNop() }

func idStr(i int) string {
	const digits = "0123456789abcdef"
	if i == 0 {
		return "0"
	}
	out := ""
	for i > 0 {
		out = string(digits[i&0xf]) + out
		i >>= 4
	}
	return out
}

func TestPartitionFoldBasic(t *testing.T) {
	msgs := []protocol.Message{
		{Role: protocol.RoleUser, Content: "u1", ID: "u1"},
		{Role: protocol.RoleAssistant, Content: "a1", ID: "a1"},
		{Role: protocol.RoleUser, Content: "u2", ID: "u2"},
		{Role: protocol.RoleTool, Content: "tool", ToolCallID: "tc", ID: "tool"},
	}
	pinned, kept, fold := partitionFold(msgs)
	if len(pinned) != 0 {
		t.Errorf("pinned = %d, want 0 (no pin flag today)", len(pinned))
	}
	// isPinnableUserTurn is disabled today, so user turns go to fold.
	if len(kept) != 0 {
		t.Errorf("kept = %d, want 0 (no pinnable heuristic today)", len(kept))
	}
	if len(fold) != 4 {
		t.Errorf("fold = %d, want 4", len(fold))
	}
}

func TestPartitionFoldKeepsPriorDigests(t *testing.T) {
	digest := protocol.Message{
		Role:    protocol.RoleAssistant,
		Content: "[Conversation Summary]\nold summary",
		ID:      "d1",
	}
	user := protocol.Message{Role: protocol.RoleUser, Content: "u", ID: "u"}
	assistant := protocol.Message{Role: protocol.RoleAssistant, Content: "a", ID: "a"}

	_, kept, fold := partitionFold([]protocol.Message{digest, user, assistant})
	if len(kept) != 1 {
		t.Fatalf("kept = %d, want 1 (digest)", len(kept))
	}
	if kept[0].ID != "d1" {
		t.Errorf("kept[0].ID = %q, want d1", kept[0].ID)
	}
	if len(fold) != 2 {
		t.Fatalf("fold = %d, want 2 (user + assistant)", len(fold))
	}
}

func TestIsCompactionSummary(t *testing.T) {
	cases := []struct {
		name string
		m    protocol.Message
		want bool
	}{
		{"digest-with-prefix", protocol.Message{Role: protocol.RoleAssistant, Content: "[Conversation Summary]\n..."}, true},
		{"digest-no-prefix", protocol.Message{Role: protocol.RoleAssistant, Content: "summary"}, false},
		{"user-message", protocol.Message{Role: protocol.RoleUser, Content: "[Conversation Summary]\n..."}, false},
		{"tool-message", protocol.Message{Role: protocol.RoleTool, Content: "[Conversation Summary]\n..."}, false},
		{"empty-content", protocol.Message{Role: protocol.RoleAssistant, Content: ""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCompactionSummary(tc.m); got != tc.want {
				t.Errorf("isCompactionSummary = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPinnedPrefixLen(t *testing.T) {
	msgs := []protocol.Message{
		{Role: protocol.RoleUser, Content: "a"},   // (1+3)/4 = 1 token
		{Role: protocol.RoleUser, Content: "bb"},  // (2+3)/4 = 1 token
		{Role: protocol.RoleUser, Content: "ccc"}, // (3+3)/4 = 1 token
	}
	estimate := func(m protocol.Message) int { return (len(m.Content) + 3) / 4 }
	if got := pinnedPrefixLen(msgs, 0, estimate); got != 0 {
		t.Errorf("zero budget = %d, want 0", got)
	}
	if got := pinnedPrefixLen(nil, 100, estimate); got != 0 {
		t.Errorf("nil msgs = %d, want 0", got)
	}
	// 1 token each, total 3. budget=1 → only first fits.
	if got := pinnedPrefixLen(msgs, 1, estimate); got != 1 {
		t.Errorf("budget 1 = %d, want 1", got)
	}
	// budget=3 → all three fit (3 × 1 ≤ 3).
	if got := pinnedPrefixLen(msgs, 3, estimate); got != 3 {
		t.Errorf("budget 3 = %d, want 3", got)
	}
	if got := pinnedPrefixLen(msgs, 100, estimate); got != 3 {
		t.Errorf("budget 100 = %d, want 3", got)
	}
}

func TestFirstKeptBoundary(t *testing.T) {
	msgs := []protocol.Message{
		{ID: "m1", Timestamp: 100},
		{ID: "m2", Timestamp: 200},
		{ID: "m3", Timestamp: 300},
		{ID: "m4", Timestamp: 400},
	}
	id, ts := firstKeptBoundary(msgs, 2)
	if id != "m3" {
		t.Errorf("id = %q, want m3", id)
	}
	if ts != 300 {
		t.Errorf("ts = %d, want 300", ts)
	}

	id, ts = firstKeptBoundary(msgs, 0)
	if id != "" || ts != 0 {
		t.Errorf("zero tail = (%q, %d), want zero", id, ts)
	}

	id, ts = firstKeptBoundary(nil, 3)
	if id != "" || ts != 0 {
		t.Errorf("nil msgs = (%q, %d), want zero", id, ts)
	}

	id, ts = firstKeptBoundary(msgs, 100)
	if id != "m1" {
		t.Errorf("oversized tail = %q, want m1 (clamped)", id)
	}
}

func TestCompactPreservesPriorDigestInRetained(t *testing.T) {
	s := &fakeSummarizer{output: "fresh"}
	a := newAssemblerWithSummarizer(s)

	prior := protocol.Message{
		Role:    protocol.RoleAssistant,
		Content: "[Conversation Summary]\nold",
		ID:      "d1",
		Timestamp: 100,
	}
	// Enough user turns to push total tokens over budget=20 so compact
	// actually runs (digest ≈ 7 tokens, 18 × 2 = 36, total ≈ 43 > 20).
	msgs := []protocol.Message{
		prior,
	}
	for i := 1; i <= 18; i++ {
		msgs = append(msgs, protocol.Message{
			Role: protocol.RoleUser, Content: "msg x",
			ID:        "u" + idStr(i),
			Timestamp: int64(100 + i*100),
		})
	}

	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  msgs,
		Budget:    35,
	})
	if !res.Success {
		t.Fatalf("Success = false; result=%+v", res)
	}

	// Retained must contain the prior digest verbatim AND the new
	// summary AND the trailing tail.
	foundOld := false
	foundNew := false
	for _, m := range res.RetainedMessages {
		if m.Content == "[Conversation Summary]\nold" {
			foundOld = true
		}
		if strings.Contains(m.Content, "[Conversation Summary]") && strings.Contains(m.Content, "fresh") {
			foundNew = true
		}
	}
	if !foundOld {
		t.Errorf("prior digest missing from retained slice:\n%+v", res.RetainedMessages)
	}
	if !foundNew {
		t.Errorf("new digest missing from retained slice:\n%+v", res.RetainedMessages)
	}
	if res.FirstKeptID == "" {
		t.Errorf("FirstKeptID is empty (boundary not surfaced)")
	}
	if res.Summary == "" {
		t.Errorf("Summary empty; expected non-empty digest text")
	}
}

func TestBuildSystemSectionsRegistersAndBootstrap(t *testing.T) {
	a := NewDefaultAssembler(Config{
		MemoryFactsLimit: 5,
	}, fakeDeps{
		logger:          zapNopForTest(),
		memoryBootstrap: map[string]string{
			"IDENTITY.md": "i-am-the-agent",
			"SOUL.md":     "be-wise",
			"USER.md":     "user-prefs",
		},
		memoryFacts: []Fact{{Content: "preference: oat milk", Source: "user"}},
	})

	got := a.BuildSystemSections(context.Background(), "s1",
		[]SkillSummary{{Name: "docx", Description: "x"}},
		nil, // caller override empty → fall through to deps
		[]MCPServerInfo{{Name: "fs", Tools: []string{"r"}}},
	)

	wantPrefixes := map[string]int{
		"identity":         PriorityIdentity,
		"soul":             PrioritySoul,
		"user":             PriorityUser,
		"available_skills": 100,
		"available_facts":  PriorityMemory,
		"available_mcp":    120,
	}
	for name, prio := range wantPrefixes {
		found := false
		for _, s := range got {
			if s.Name == name && s.Priority == prio {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing section %q at priority %d", name, prio)
		}
	}
}

func TestBuildSystemSectionsCallerOverrideSkipsDeps(t *testing.T) {
	depsCalled := false
	a := NewDefaultAssembler(Config{
		MemoryFactsLimit: 5,
	}, fakeDeps{
		logger: zapNopForTest(),
		memoryFactsFn: func(_ context.Context) []Fact {
			depsCalled = true
			return nil
		},
	})

	override := []Fact{{Content: "override fact"}}
	got := a.BuildSystemSections(context.Background(), "s1", nil, override, nil)

	if depsCalled {
		t.Errorf("Deps.MemoryFacts called even though caller override was non-empty")
	}
	found := false
	for _, s := range got {
		if s.Name == "available_facts" && strings.Contains(s.Content, "override fact") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("override facts did not surface in built-in sections:\n%+v", got)
	}
}

func TestBuildSystemSectionsEmptyBootstrapDoesNotEmit(t *testing.T) {
	a := NewDefaultAssembler(Config{}, fakeDeps{
		logger:          zapNopForTest(),
		memoryBootstrap: map[string]string{"IDENTITY.md": "  "}, // whitespace only
	})

	got := a.BuildSystemSections(context.Background(), "s1", nil, nil, nil)
	for _, s := range got {
		if s.Name == "identity" {
			t.Errorf("identity section leaked despite whitespace-only content:\n%s", s.Content)
		}
	}
}

// toolPair builds a tool_use/tool_result pair (assistant with one
// ToolCall + matching tool message) for partition / tail tests.
func toolPair(callID, text, result string) []protocol.Message {
	return []protocol.Message{
		{
			Role:    protocol.RoleAssistant,
			Content: text,
			ToolCalls: []protocol.ToolCall{
				{ID: callID, Name: "fake"},
			},
		},
		{
			Role:       protocol.RoleTool,
			Content:    result,
			ToolCallID: callID,
		},
	}
}

// TestPartitionFoldKeepsToolPairsAtomic verifies Reasonix-style pair
// atomicity: when an assistant with ToolCalls is followed by its
// matching tool message, partitionFold must keep them in the same
// bucket so the summariser never eats a tool_use without its result
// (or vice versa) — Anthropic rejects orphan tool_result blocks with
// a 400 (see llm/anthropic/convert.go).
func TestPartitionFoldKeepsToolPairsAtomic(t *testing.T) {
	msgs := []protocol.Message{
		{Role: protocol.RoleUser, Content: "u1", ID: "u1"},
	}
	msgs = append(msgs, toolPair("tc1", "a1", "result-1")...)
	msgs = append(msgs, protocol.Message{Role: protocol.RoleUser, Content: "u2", ID: "u2"})
	msgs = append(msgs, toolPair("tc2", "a2", "result-2")...)

	_, kept, fold := partitionFold(msgs)
	if len(kept) != 0 {
		t.Errorf("kept = %d, want 0", len(kept))
	}
	if len(fold) != 6 {
		t.Fatalf("fold = %d, want 6 (u1 + pair + u2 + pair)", len(fold))
	}

	// Walk fold and confirm no orphan tool_result ever appears without
	// the matching tool_use directly preceding it.
	pendingIDs := map[string]struct{}{}
	for i, m := range fold {
		switch m.Role {
		case protocol.RoleAssistant:
			for _, tc := range m.ToolCalls {
				pendingIDs[tc.ID] = struct{}{}
			}
		case protocol.RoleTool:
			if _, ok := pendingIDs[m.ToolCallID]; !ok {
				t.Errorf("fold[%d] tool_result %q has no preceding tool_use", i, m.ToolCallID)
			}
			delete(pendingIDs, m.ToolCallID)
		}
	}
}

// TestPartitionFoldKeepsToolPairWithDigestKeepsPairAtomic covers the
// edge case where a tool pair abuts a prior digest: the digest is
// kept verbatim, but the immediately following tool pair must stay
// together (still in fold since neither side is pinnable).
func TestPartitionFoldKeepsToolPairWithDigestKeepsPairAtomic(t *testing.T) {
	msgs := []protocol.Message{
		{
			Role:    protocol.RoleAssistant,
			Content: "[Conversation Summary]\nold",
			ID:      "d1",
		},
		{Role: protocol.RoleUser, Content: "u1", ID: "u1"},
	}
	msgs = append(msgs, toolPair("tc1", "a1", "result-1")...)

	_, kept, fold := partitionFold(msgs)
	if len(kept) != 1 || kept[0].ID != "d1" {
		t.Errorf("kept = %+v, want exactly the prior digest", kept)
	}
	if len(fold) != 3 {
		t.Fatalf("fold = %d, want 3 (u1 + pair)", len(fold))
	}
	if fold[1].Role != protocol.RoleAssistant || len(fold[1].ToolCalls) == 0 {
		t.Errorf("fold[1] should be assistant with ToolCalls, got %+v", fold[1])
	}
	if fold[2].Role != protocol.RoleTool || fold[2].ToolCallID == "" {
		t.Errorf("fold[2] should be tool message, got %+v", fold[2])
	}
}

// TestAlignTailBoundaryExtendsOverOrphanTool is the direct regression
// for the bug the user reported: when the proposed tail would start
// mid-pair on a tool_result, alignTailBoundary extends backward to
// pull in the matching tool_use assistant so the wire format stays
// valid (anthropic/convert.go).
func TestAlignTailBoundaryExtendsOverOrphanTool(t *testing.T) {
	msgs := []protocol.Message{
		{Role: protocol.RoleUser, Content: "u1", ID: "u1"},
	}
	msgs = append(msgs, toolPair("tc1", "a1", "result-1")...)
	msgs = append(msgs, []protocol.Message{
		{Role: protocol.RoleUser, Content: "u2", ID: "u2"},
	}...)
	msgs = append(msgs, toolPair("tc2", "a2", "result-2")...)

	// Indices: 0=u1 1=a1{tc1} 2=t1 3=u2 4=a2{tc2} 5=t2

	// tail=1 → start=5 (msgs[5]=t2, tool). Look back → a2 at 4.
	// Extend: start=4. New tail length = 6-4 = 2.
	if got := alignTailBoundary(msgs, 1); got != 2 {
		t.Errorf("alignTailBoundary(msgs, 1) = %d, want 2 (extended by 1 to include a2)", got)
	}

	// tail=2 → start=4 (msgs[4]=a2, not tool). No extension.
	// msgs[4..6] = [a2, t2] is already pair-atomic.
	if got := alignTailBoundary(msgs, 2); got != 2 {
		t.Errorf("alignTailBoundary(msgs, 2) = %d, want 2 (already pair-atomic)", got)
	}

	// tail=3 → start=3 (msgs[3]=u2, user). No extension.
	if got := alignTailBoundary(msgs, 3); got != 3 {
		t.Errorf("alignTailBoundary(msgs, 3) = %d, want 3", got)
	}

	// tail=4 → start=2 (msgs[2]=t1, tool). Look back → a1 at 1.
	// Extend: start=1. New tail length = 6-1 = 5.
	if got := alignTailBoundary(msgs, 4); got != 5 {
		t.Errorf("alignTailBoundary(msgs, 4) = %d, want 5 (extended by 1 to include a1)", got)
	}

	// tail=5 → start=1 (msgs[1]=a1, assistant). No extension.
	if got := alignTailBoundary(msgs, 5); got != 5 {
		t.Errorf("alignTailBoundary(msgs, 5) = %d, want 5", got)
	}
}

// TestAlignTailBoundaryShrinksOverOrphanToolUse covers the symmetric
// end-cut case: a tail that ends on an assistant with unpaired
// ToolCalls must shrink forward so the LLM never sees a tool_use
// without its tool_result.
func TestAlignTailBoundaryShrinksOverOrphanToolUse(t *testing.T) {
	msgs := []protocol.Message{
		{Role: protocol.RoleUser, Content: "u1", ID: "u1"},
	}
	msgs = append(msgs, toolPair("tc1", "a1", "result-1")...)
	msgs = append(msgs, []protocol.Message{
		{Role: protocol.RoleUser, Content: "u2", ID: "u2"},
	}...)
	msgs = append(msgs, protocol.Message{
		Role:    protocol.RoleAssistant,
		Content: "a2",
		ToolCalls: []protocol.ToolCall{
			{ID: "tc2", Name: "fake"},
		},
	}) // note: no tool{tc2} follows — orphan tool_use at tail end

	// requested tail=2 → [u2, a2{tc2}] — orphan tool_use, shrink to 1.
	if got := alignTailBoundary(msgs, 2); got != 1 {
		t.Errorf("alignTailBoundary(msgs, 2) = %d, want 1 (shrunk over orphan tool_use)", got)
	}

	// requested tail=4 → [pair(u1/tc1), u2, a2{tc2}] — orphan tool_use,
	// shrink to 3 (drop a2).
	if got := alignTailBoundary(msgs, 4); got != 3 {
		t.Errorf("alignTailBoundary(msgs, 4) = %d, want 3 (shrunk over orphan tool_use)", got)
	}
}

// TestAlignTailBoundaryNoOpWhenAligned is the happy path: a tail that
// neither starts nor ends mid-pair must be returned unchanged.
func TestAlignTailBoundaryNoOpWhenAligned(t *testing.T) {
	msgs := []protocol.Message{
		{Role: protocol.RoleUser, Content: "u1", ID: "u1"},
	}
	msgs = append(msgs, toolPair("tc1", "a1", "result-1")...)
	msgs = append(msgs, []protocol.Message{
		{Role: protocol.RoleUser, Content: "u2", ID: "u2"},
	}...)
	msgs = append(msgs, toolPair("tc2", "a2", "result-2")...)

	// Indices: 0=u1 1=a1{tc1} 2=t1 3=u2 4=a2{tc2} 5=t2
	// Aligned tails: 2 ([a2,t2]), 3 ([u2,a2,t2]), 5 ([a1,t1,u2,a2,t2]), 6 (all).
	cases := []struct{ in, want int }{
		{2, 2},
		{3, 3},
		{4, 5}, // start=2 (tool), extend to 5
		{5, 5},
		{6, 6},
	}
	for _, tc := range cases {
		got := alignTailBoundary(msgs, tc.in)
		if got != tc.want {
			t.Errorf("alignTailBoundary(msgs, %d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestCompactRetainsToolPairInTail is the end-to-end regression for
// the user's reported 400 error: after Compact, the resulting
// RetainedMessages must never contain a tool message whose tool_use
// is not in the immediately preceding assistant message of the
// retained slice.
func TestCompactRetainsToolPairInTail(t *testing.T) {
	s := &fakeSummarizer{output: "compressed"}
	// RecentKeep=2 with a 7-message conversation makes the proposed
	// tail land on [tool{tc2}, u3] — orphan tool at tail[0]. After
	// alignTailBoundary the tail must extend to [a2{tc2}, t2{tc2}, u3].
	a := NewDefaultAssembler(Config{
		ContextWindow:      1000,
		RecentKeep:         2,
		SummarizeMaxTokens: 100,
	}, fakeDeps{model: "m", logger: zapNopForTest()})
	a.SetSummarizer(s)

	msgs := []protocol.Message{
		{Role: protocol.RoleUser, Content: "u1", ID: "u1"},
	}
	msgs = append(msgs, toolPair("tc1", "a1", "result-1")...)
	msgs = append(msgs, protocol.Message{Role: protocol.RoleUser, Content: "u2", ID: "u2"})
	msgs = append(msgs, toolPair("tc2", "a2", "result-2")...)
	msgs = append(msgs, protocol.Message{Role: protocol.RoleUser, Content: "u3", ID: "u3"})

	// Force=true bypasses the budget no-op short-circuit; Budget=0
	// disables the post-compact budget re-check (fakeSummarizer does
	// not shrink on retry, so we focus on the wire-format invariant
	// rather than the budget arithmetic).
	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  msgs,
		Force:     true,
	})
	if !res.Success {
		t.Fatalf("Compact Success=false; result=%+v", res)
	}

	// Walk retained: every tool message's tool_use_id must be present
	// in the ToolCalls of the immediately preceding assistant message.
	for i := 1; i < len(res.RetainedMessages); i++ {
		prev := res.RetainedMessages[i-1]
		cur := res.RetainedMessages[i]
		if cur.Role != protocol.RoleTool {
			continue
		}
		if prev.Role != protocol.RoleAssistant {
			t.Errorf("Retained[%d] is tool but Retained[%d] is %s (no preceding assistant); would 400", i, i-1, prev.Role)
			continue
		}
		matched := false
		for _, tc := range prev.ToolCalls {
			if tc.ID == cur.ToolCallID {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("Retained[%d] tool_result %q has no matching tool_use in Retained[%d] assistant %+v", i, cur.ToolCallID, i-1, prev.ToolCalls)
		}
	}
}
