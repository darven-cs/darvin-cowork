// Context-assembly providers for the ctxengine Deps contract: skill /
// mcp summaries, MEMORY FTS facts, bootstrap files, and compaction
// digests.

package agent

import (
	"context"
	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/store"
	"time"

	"go.uber.org/zap"
)

// SkillSummaries satisfies executor.Deps. nil registry → nil slice.
func (a *Agent) SkillSummaries() []ctxengine.SkillSummary {
	if a.skills == nil {
		return nil
	}
	entries := a.skills.ListEnabled()
	out := make([]ctxengine.SkillSummary, 0, len(entries))
	for _, e := range entries {
		name := e.Name
		if name == "" {
			name = e.ID
		}
		out = append(out, ctxengine.SkillSummary{Name: name, Description: e.Description})
	}
	return out
}

// McpServers satisfies executor.Deps. nil registry → nil slice.
func (a *Agent) McpServers() []ctxengine.MCPServerInfo {
	if a.mcp == nil {
		return nil
	}
	servers := a.mcp.ListServers()
	out := make([]ctxengine.MCPServerInfo, 0, len(servers))
	for _, s := range servers {
		out = append(out, ctxengine.MCPServerInfo{Name: s.Name, Tools: s.Tools})
	}
	return out
}

// MemoryFacts satisfies ctxengine.Deps. nil manager / empty hits /
// ctx error collapse to nil so BuiltInSections skips the MEMORY
// block.
func (a *Agent) MemoryFacts(ctx context.Context) []ctxengine.Fact {
	if a.memoryMgr == nil || !a.memoryMgr.Enabled() {
		return nil
	}
	q := a.recentUserQuery(3)
	if q == "" {
		return nil
	}
	hits := a.memoryMgr.Search(ctx, q, a.cfg.MemoryFactsLimit)
	if len(hits) == 0 {
		return nil
	}
	out := make([]ctxengine.Fact, 0, len(hits))
	for _, h := range hits {
		out = append(out, ctxengine.Fact{Content: h.Text, Source: h.Section})
	}
	return out
}

// MemoryBootstrap satisfies ctxengine.Deps. MUST proxy through
// workspaceBstrp.Get — bypassing the singleton (e.g. via
// memoryMgr.ReadBootstrap) defeats the change-notification machinery
// so bootstrap.write RPCs never reach the LLM.
func (a *Agent) MemoryBootstrap(name string) string {
	if a.workspaceBstrp == nil {
		return ""
	}
	return a.workspaceBstrp.Get(name)
}

// PersistCompaction writes a new digest row to session_digests.
// Sequence is allocated by DigestStore.Save so concurrent saves
// cannot duplicate. Failures are warn-and-continue.
func (a *Agent) PersistCompaction(ctx context.Context, res ctxengine.CompactResult) error {
	if a.digestStore == nil || !res.Success {
		return nil
	}
	checkpointID := ""
	if res.Checkpoint != nil {
		checkpointID = res.Checkpoint.ID
	}
	rec := &store.SessionDigest{
		ID:                 "digest-" + checkpointID,
		SessionID:          a.session.ID,
		Summary:            res.Summary,
		TokensBefore:       res.TokensBefore,
		TokensAfter:        res.TokensAfter,
		FirstKeptID:        res.FirstKeptID,
		FirstKeptTimestamp: res.FirstKeptTimestamp,
		CompactReason:      res.Reason,
		SourceCompactID:    checkpointID,
		CreatedAt:          time.Now().UnixMilli(),
	}
	if err := a.digestStore.Save(ctx, rec); err != nil && a.logger != nil {
		a.logger.Warn("persist compaction failed",
			zap.String("session_id", a.session.ID),
			zap.String("digest_id", rec.ID),
			zap.Error(err))
		return err
	}
	return nil
}

// recentUserQuery concatenates the last n user messages' Content into a
// single query string for MEMORY FTS. Empty when the session has no
// user turns yet — the assembler skips the MEMORY block in that case.
func (a *Agent) recentUserQuery(n int) string {
	if n <= 0 || a.session == nil {
		return ""
	}
	msgs := a.session.Messages()
	parts := make([]string, 0, n)
	for i := len(msgs) - 1; i >= 0 && len(parts) < n; i-- {
		if msgs[i].Role == protocol.RoleUser && msgs[i].Content != "" {
			parts = append(parts, msgs[i].Content)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	// Reverse to chronological order so the query reads naturally.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}
