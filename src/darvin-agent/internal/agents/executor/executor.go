// Package executor owns the single-turn LLM call + tool dispatch logic.
// The agent root package feeds it via the Deps interface; this package does
// not import the agent root (avoiding a cycle: agent -> executor -> agent).
package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/session"
)

// ErrMaxTurns is returned by RunConversation when the loop reaches the
// configured MaxTurns cap without reaching a natural stop reason. Callers
// should treat this as a normal termination path (no AgentErrorEvent), not
// a crash. Lives in the executor package (rather than the agent root) to
// avoid an agent <-> executor import cycle — dispatcher's Run forwards
// this error upward unchanged via errors.Is.
var ErrMaxTurns = errors.New("executor: max turns exceeded")

// maxToolResultStoreBytes caps the tool result persisted into ToolCall.Result.
// Bash / read 输出可能到 MB 级，落库只保留截断前缀 + 尾注；live 流式事件仍
// 推完整内容给 renderer，截断只影响 reload 时的工具结果展示。
const maxToolResultStoreBytes = 64 * 1024

const toolResultTruncatedSuffix = "\n…[已截断]"

// PermissionRequest is a tool call the executor wants user approval for
// before running. RequestID is minted by the Agent.
type PermissionRequest struct {
	ToolName    string
	ToolInput   map[string]any
	DangerLevel string // safe | caution | destructive
	Reason      string
}

// PermissionResult is the renderer's answer (via agent.permission_response).
type PermissionResult struct {
	Behavior     string // "allow" | "deny"
	UpdatedInput map[string]any
	Message      string
	Interrupt    bool
	Remember     bool
}

func truncateForStore(content string) string {
	if len(content) <= maxToolResultStoreBytes {
		return content
	}
	keep := maxToolResultStoreBytes - len(toolResultTruncatedSuffix)
	if keep < 0 {
		keep = 0
	}
	return content[:keep] + toolResultTruncatedSuffix
}

// Config is the subset of agent configuration the executor needs.
type Config struct {
	MaxTurns    int
	ToolTimeout time.Duration
	// ContextWindow is the LLM's hard context cap; forwarded to
	// ContextEngine.Assemble as part of the assembler's auto-compact
	// configuration. When <= 0 the assembler disables the entire
	// auto-compact pipeline.
	ContextWindow int
}

// compactBudget returns the post-compact token target for the
// current run. The 0.5 ratio keeps the cascade compressing into the
// lower half of the context window rather than barely under the
// trigger threshold.
func (c Config) compactBudget() int {
	if c.ContextWindow <= 0 {
		return 0
	}
	return c.ContextWindow / 2
}

// Deps is the surface of agent.Agent the executor consumes. The agent root
// package satisfies this implicitly.
type Deps interface {
	Session() *session.Session
	Tools() protocol.ToolRegistry
	Provider() protocol.ModelProvider
	ModelName() string
	Instructions() string
	Emit(event.Event)
	Config() Config
	// ContextEngine seam. May return
	// nil / false to opt out of assembler-driven prompt construction and
	// fall back to the legacy d.Session().Messages() path.
	Assembler() ctxengine.ContextEngine
	SystemSections() []ctxengine.SystemSection
	AssemblerEnabled() bool
	// Usage accounting: RecordUsage stores the API-reported Usage for the
	// just-finished turn (model tags the snapshot so the persistence layer
	// knows which context window to use on rehydrate); LastUsage returns
	// the most recent Usage so the ContextEngine can prefer API token
	// counts over the local estimator.
	RecordUsage(u protocol.Usage, model string)
	LastUsage() protocol.Usage
	// CurrentMessageID returns the messageID the agent loop assigned to the
	// prompt that triggered the in-flight run. The executor embeds it on
	// every emitted event so downstream consumers (EventLedger, renderer)
	// can correlate events back to the originating prompt.
	CurrentMessageID() string
	// CurrentRunID returns the caller-minted runID the agent loop assigned
	// to the prompt that triggered the in-flight run. The executor embeds
	// it on every emitted event so downstream consumers can abort a
	// specific turn and demultiplex events by turn id.
	CurrentRunID() string
	// EvaluatePermission decides whether a tool call needs user approval
	// (path escapes the authorized roots, or a destructive shell command).
	EvaluatePermission(toolName string, args map[string]any) protocol.PermissionEval
	// RequestPermission emits a permission_request event and blocks until
	// the renderer answers (or ctx fires / 60s timeout → deny).
	RequestPermission(ctx context.Context, req PermissionRequest) (PermissionResult, error)
	// HasPermissionRule / AddPermissionRule back the "remember this session"
	// auto-allow feature: identical (tool, level, reason) requests skip the
	// modal after the user allowed + remembered once.
	HasPermissionRule(toolName, level, reason string) bool
	AddPermissionRule(toolName, level, reason string)
	// ApprovePath grants the sandbox one-shot access to a path the user
	// allowed via the modal, so the tool can actually open it.
	ApprovePath(path string)
	// ResultTransformer normalises a tool result before the executor
	// forwards it to the LLM. nil means no transformation. The hook exists
	// for the harness's tooldridge middleware chain; it is intentionally
	// optional so the executor does not depend on harness.
	ResultTransformer() func(protocol.Result) protocol.Result
	// SkillSummaries / McpServers feed the assembler's system prompt
	// sections. nil / nil are valid — empty registries just omit the
	// <available_skills> / <available_mcp> blocks.
	SkillSummaries() []ctxengine.SkillSummary
	McpServers() []ctxengine.MCPServerInfo

	// PersistCompaction records a compaction digest. Called after
	// auto-compact persists the compacted slice to the live session.
	PersistCompaction(ctx context.Context, res ctxengine.CompactResult) error
}

// Executor runs one "user message -> possibly many turns -> natural stop"
// sequence. Implementations must respect ctx cancellation.
type Executor interface {
	RunConversation(ctx context.Context, d Deps) error
}

// New constructs the default in-process executor.
func New() Executor { return &defaultExecutor{} }

type defaultExecutor struct{}

func (e *defaultExecutor) RunConversation(ctx context.Context, d Deps) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg := d.Config()
	var totalUsage protocol.Usage
	// ec is the EventCommon payload every emitted event in this run carries.
	// SessionID is stable across the Run; MessageID is read at the moment of
	// emit so it always reflects the in-flight prompt's id (no stale read
	// across iterations).
	ec := func() event.EventCommon {
		return event.EventCommon{
			SessionID: d.Session().ID,
			RunID:     d.CurrentRunID(),
			MessageID: d.CurrentMessageID(),
		}
	}
	for turnIndex := 1; turnIndex <= cfg.MaxTurns; turnIndex++ {
		turnID := newTurnID()
		d.Emit(event.TurnStartEvent{
			EventBase: event.EventBase{EventCommon: ec()},
			TurnID:    turnID,
			TurnIndex: turnIndex,
		})

		// 1. assemble messages via the ContextEngine (fallback to the legacy
		//    d.Session().Messages() path when the assembler is not wired or
		//    explicitly disabled).
		var messages []protocol.Message
		var systemAddition string
		assemblerEnabled := d.Assembler() != nil && d.AssemblerEnabled()
		if !assemblerEnabled {
			messages = d.Session().Messages()
		} else {
			assembled := d.Assembler().Assemble(ctx, ctxengine.AssembleParams{
				SessionID:       d.Session().ID,
				Messages:        d.Session().Messages(),
				ToolBudget:      d.Config().compactBudget(),
				LastUsage:       d.LastUsage(),
				SystemSections:  d.SystemSections(),
				AvailableSkills: d.SkillSummaries(),
				MCPServers:      d.McpServers(),
			})
			messages = assembled.Messages
			systemAddition = assembled.SystemAddition
			if assembled.Stats.CompactionTriggered {
				d.Session().ReplaceAll(messages)
				// Persist failures are warn-and-continue — the live
				// slice is already in place, only the digest row
				// failed to land.
				_ = d.PersistCompaction(ctx, ctxengine.CompactResult{
					Success:            true,
					Summary:            assembled.CompactSummary,
					TokensAfter:        assembled.EstimatedTokens,
					FirstKeptID:        assembled.FirstKeptID,
					FirstKeptTimestamp: assembled.FirstKeptTimestamp,
					Reason:             "budget_exceeded",
					Checkpoint: &ctxengine.CheckPoint{
						ID:         "auto-" + assembled.FirstKeptID,
						CapturedAt: time.Now(),
						Snapshot:   messages,
					},
				})
			}
		}

		// 2. LLM call
		d.Emit(event.LLMStartEvent{
			EventBase: event.EventBase{EventCommon: ec()},
			Model:     d.ModelName(),
		})
		// System = Instructions() + SystemAddition. Both must reach
		// the LLM; sending Instructions() alone silently drops
		// bootstrap / skill / memory blocks.
		system := d.Instructions()
		if systemAddition != "" {
			system += "\n\n" + systemAddition
		}
		req := &protocol.CompletionRequest{
			Model:      d.ModelName(),
			Messages:   messages,
			Tools:      d.Tools().Specs(),
			ToolChoice: protocol.ToolChoice{Type: "auto"},
			System:     system,
			Stream:     true,
			MaxTokens:  4096,
		}
		stream, err := d.Provider().Stream(ctx, req)
		if err != nil {
			return fmt.Errorf("executor: stream: %w", err)
		}

		// 3. accumulate assistant message
		var text string
		var toolCalls []protocol.ToolCall
		turnUsage, streamErr := drainStream(ctx, d, ec, stream, &text, &toolCalls)
		if streamErr != nil {
			if streamErr == context.Canceled || ctx.Err() != nil {
				assistant := protocol.Message{
					Role:      protocol.RoleAssistant,
					Content:   text,
					ToolCalls: toolCalls,
					ID:        d.CurrentMessageID(),
					Timestamp: time.Now().UnixMilli(),
				}
				d.Session().Append(assistant)
				d.Emit(event.TurnEndEvent{
					EventBase:  event.EventBase{EventCommon: ec()},
					TurnIndex:  turnIndex,
					StopReason: protocol.FinishReasonAborted,
				})
				return context.Canceled
			}
			return streamErr
		}
		// publish the API-reported usage so subsequent assembles can prefer
		// it over the rune/4 estimator, and so the LLMEndEvent payload carries
		// the real cumulative cost (prior version always emitted zero). The
		// model tag is forwarded so the persisted snapshot knows which
		// context window to use when rehydrating on session switch.
		d.RecordUsage(turnUsage, d.ModelName())
		totalUsage.PromptTokens += turnUsage.PromptTokens
		totalUsage.CompletionTokens += turnUsage.CompletionTokens
		totalUsage.TotalTokens += turnUsage.TotalTokens
		assistant := protocol.Message{
			Role:      protocol.RoleAssistant,
			Content:   text,
			ToolCalls: toolCalls,
			ID:        d.CurrentMessageID(),
			Timestamp: time.Now().UnixMilli(),
		}
		d.Emit(event.LLMEndEvent{
			EventBase: event.EventBase{EventCommon: ec()},
			Assistant: assistant,
			Usage:     totalUsage,
		})

		// 4. decide next step
		if len(toolCalls) == 0 {
			d.Session().Append(assistant)
			d.Emit(event.TurnEndEvent{
				EventBase:  event.EventBase{EventCommon: ec()},
				TurnIndex:  turnIndex,
				StopReason: protocol.FinishReasonStop,
			})
			return nil
		}

		// 5. run tools in parallel
		results := e.runToolsParallel(ctx, d, ec, turnID, toolCalls)

		// 6. attach tool results to the assistant snapshot so persistence can
		// store them alongside the call (renderer rebuilds tool_result on reload)
		for i := range assistant.ToolCalls {
			if i < len(results) {
				assistant.ToolCalls[i].Result = &protocol.ToolResult{Content: truncateForStore(results[i].Content), IsError: results[i].IsError}
			}
		}
		// 7. append assistant (with results) then tool result messages in original order
		d.Session().Append(assistant)
		for i, tc := range toolCalls {
			d.Session().Append(protocol.Message{
				Role:       protocol.RoleTool,
				Content:    results[i].Content,
				ToolCallID: tc.ID,
				Timestamp:  time.Now().UnixMilli(),
			})
		}
		d.Emit(event.TurnEndEvent{
			EventBase:  event.EventBase{EventCommon: ec()},
			TurnIndex:  turnIndex,
			StopReason: protocol.FinishReasonToolCalls,
		})
	}
	return fmt.Errorf("%w (limit: %d)", ErrMaxTurns, cfg.MaxTurns)
}

// drainStream consumes ev from stream.Events, accumulating text / tool
// calls and emitting passthrough events. Returns the API-reported Usage
// from the terminal DoneEvent (zero value if the stream ended without a
// DoneEvent — e.g. context cancellation) and the stream's terminal error
// (nil on clean DoneEvent or context.Canceled if ctx fires).
//
// ec is the EventCommon payload for every passthrough event; the helper
// reads it at each emit so MessageID reflects the in-flight prompt.
func drainStream(ctx context.Context, d Deps, ec func() event.EventCommon, stream *protocol.StreamingResponse, textOut *string, callsOut *[]protocol.ToolCall) (protocol.Usage, error) {
	for ev := range stream.Events {
		switch e := ev.(type) {
		case protocol.StartEvent:
			// nothing to do — already emitted LLMStartEvent
		case protocol.TextDeltaEvent:
			*textOut += e.Delta
			d.Emit(event.TextDeltaEvent{
				EventBase: event.EventBase{EventCommon: ec()},
				Delta:     e.Delta,
			})
		case protocol.ThinkingDeltaEvent:
			d.Emit(event.ThinkingDeltaEvent{
				EventBase: event.EventBase{EventCommon: ec()},
				Delta:     e.Delta,
			})
		case protocol.ToolCallStartEvent:
			// we accumulate via End event; nothing here
		case protocol.ToolCallDeltaEvent:
			// no-op: provider delivers parsed Arguments in End
		case protocol.ToolCallEndEvent:
			*callsOut = append(*callsOut, protocol.ToolCall{
				ID:        e.ID,
				Name:      e.Name,
				Arguments: e.Arguments,
			})
		case protocol.DoneEvent:
			return e.Response.Usage, nil
		case protocol.ErrorEvent:
			if ctx.Err() != nil {
				return protocol.Usage{}, context.Canceled
			}
			if err := stream.Err(); err != nil {
				return protocol.Usage{}, err
			}
			return protocol.Usage{}, fmt.Errorf("executor: stream error event")
		}
	}
	// channel closed without DoneEvent — likely ctx cancel
	if ctx.Err() != nil {
		return protocol.Usage{}, context.Canceled
	}
	if err := stream.Err(); err != nil {
		return protocol.Usage{}, err
	}
	return protocol.Usage{}, nil
}

func (e *defaultExecutor) runToolsParallel(ctx context.Context, d Deps, ec func() event.EventCommon, turnID string, calls []protocol.ToolCall) []protocol.Result {
	results := make([]protocol.Result, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func(i int, c protocol.ToolCall) {
			defer wg.Done()
			kind, skillID, mcpServerID := entryAttrs(d.Tools(), c.Name)
			d.Emit(event.ToolStartEvent{
				EventBase:   event.EventBase{EventCommon: ec()},
				TurnID:      turnID,
				CallID:      c.ID,
				Name:        c.Name,
				ToolKind:    kind,
				SkillID:     skillID,
				McpServerID: mcpServerID,
				Arguments:   c.Arguments,
			})
			tctx, cancel := context.WithTimeout(ctx, d.Config().ToolTimeout)
			defer cancel()
			start := time.Now()
			// ctx = parent run ctx (permission wait is NOT capped by the tool
			// timeout — the modal needs up to its own 60s), tctx = tool timeout.
			results[i] = executeOneTool(ctx, tctx, d, c)
			if transform := d.ResultTransformer(); transform != nil {
				results[i] = transform(results[i])
			}
			d.Emit(event.ToolEndEvent{
				EventBase:   event.EventBase{EventCommon: ec()},
				CallID:      c.ID,
				ToolKind:    kind,
				SkillID:     skillID,
				McpServerID: mcpServerID,
				Result: event.ToolResult{
					Content:  results[i].Content,
					IsError:  results[i].IsError,
					Metadata: results[i].Metadata,
				},
				DurationMS: time.Since(start).Milliseconds(),
			})
		}(i, c)
	}
	wg.Wait()
	return results
}

// entryAttrs returns the kind + kind-specific identifiers for a tool from
// its registry entry. Empty strings for unknown tools keep the events
// backward-compatible with pre-kind registrations.
func entryAttrs(reg protocol.ToolRegistry, name string) (kind, skillID, mcpServerID string) {
	entry, ok := reg.GetEntry(name)
	if !ok {
		return "", "", ""
	}
	kind = string(entry.Kind)
	if v, ok := entry.Metadata["skillID"].(string); ok {
		skillID = v
	}
	if v, ok := entry.Metadata["mcpServerID"].(string); ok {
		mcpServerID = v
	}
	return kind, skillID, mcpServerID
}

func executeOneTool(ctx context.Context, tctx context.Context, d Deps, c protocol.ToolCall) (res protocol.Result) {
	t := d.Tools().Get(c.Name)
	if t == nil {
		return protocol.Result{IsError: true, Content: fmt.Sprintf("tool not found: %s", c.Name)}
	}
	defer func() {
		if r := recover(); r != nil {
			res = protocol.Result{IsError: true, Content: fmt.Sprintf("tool %q panicked: %v", c.Name, r)}
		}
	}()
	// 越授权根 / 危险操作 → 请求用户审批。命中记住规则时直接放行（不弹
	// 窗）。ctx（非 tctx）作为等待上下文，避免被工具超时提前砍掉。
	if eval := d.EvaluatePermission(c.Name, c.Arguments); eval.Need {
		allowed := d.HasPermissionRule(c.Name, eval.Level, eval.Reason)
		if !allowed {
			pr, err := d.RequestPermission(ctx, PermissionRequest{
				ToolName:    c.Name,
				ToolInput:   c.Arguments,
				DangerLevel: eval.Level,
				Reason:      eval.Reason,
			})
			if err != nil {
				return protocol.Result{IsError: true, Content: "权限请求失败：" + err.Error()}
			}
			if pr.Behavior != "allow" {
				msg := "用户拒绝了该操作"
				if pr.Message != "" {
					msg = pr.Message
				}
				return protocol.Result{IsError: true, Content: msg}
			}
			if pr.Remember {
				d.AddPermissionRule(c.Name, eval.Level, eval.Reason)
			}
			if pr.UpdatedInput != nil {
				c.Arguments = pr.UpdatedInput
			}
			allowed = true
		}
		// 越授权根路径：放行后把该路径加入沙箱一次性授权，工具才能真正打开。
		if allowed && eval.EscapedPath != "" {
			d.ApprovePath(eval.EscapedPath)
		}
	}
	// per-tool timeout lives in the goroutine; here we just respect tctx.
	return t.Execute(tctx, c.Arguments)
}

func newTurnID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("turn-%d", time.Now().UnixNano())
	}
	return "turn-" + hex.EncodeToString(b[:])
}
