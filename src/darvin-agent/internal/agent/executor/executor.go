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

	"darvin-cowork/backend/internal/agent/ctxengine"
	"darvin-cowork/backend/internal/agent/event"
	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/session"
	"darvin-cowork/backend/internal/agent/tool"
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
// before running (spec 12). RequestID is minted by the Agent.
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
	// TokenBudget is forwarded to ContextEngine.Assemble as the per-turn
	// tool-budget cap. When <= 0, Assemble uses the assembler's configured
	// fallback (or disables Compact if that is also <= 0).
	TokenBudget int
}

// Deps is the surface of agent.Agent the executor consumes. The agent root
// package satisfies this implicitly.
type Deps interface {
	Session() *session.Session
	Tools() *tool.Registry
	Provider() llm.ModelProvider
	ModelName() string
	Instructions() string
	Emit(event.Event)
	Config() Config
	// ContextEngine seam (spec §4.10 — agent-context-engine). May return
	// nil / false to opt out of assembler-driven prompt construction and
	// fall back to the legacy d.Session().Messages() path.
	Assembler() ctxengine.ContextEngine
	SystemSections() []ctxengine.SystemSection
	AssemblerEnabled() bool
	// Usage accounting: RecordUsage stores the API-reported Usage for the
	// just-finished turn; LastUsage returns the most recent Usage so the
	// ContextEngine can prefer API token counts over the local estimator.
	RecordUsage(u llm.Usage)
	LastUsage() llm.Usage
	// CurrentMessageID returns the messageID the ACP loop assigned to the
	// prompt that triggered the in-flight run. The executor embeds it on
	// every emitted event so downstream consumers (EventLedger, renderer)
	// can correlate events back to the originating prompt.
	CurrentMessageID() string
	// CurrentRunID returns the caller-minted runID the ACP loop assigned
	// to the prompt that triggered the in-flight run. The executor embeds
	// it on every emitted event so downstream consumers can abort a
	// specific turn and demultiplex events by turn id.
	CurrentRunID() string
	// EvaluatePermission decides whether a tool call needs user approval
	// (path escapes the authorized roots, or a destructive shell command).
	EvaluatePermission(toolName string, args map[string]any) tool.PermissionEval
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
	var totalUsage llm.Usage
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
		//    explicitly disabled — see spec §4.11).
		var messages []llm.Message
		assemblerEnabled := d.Assembler() != nil && d.AssemblerEnabled()
		if !assemblerEnabled {
			messages = d.Session().Messages()
		} else {
			assembled := d.Assembler().Assemble(ctx, ctxengine.AssembleParams{
				SessionID:      d.Session().ID,
				Messages:       d.Session().Messages(),
				ToolBudget:     d.Config().TokenBudget,
				LastUsage:      d.LastUsage(),
				SystemSections: d.SystemSections(),
			})
			messages = assembled.Messages
		}

		// 2. LLM call
		d.Emit(event.LLMStartEvent{
			EventBase: event.EventBase{EventCommon: ec()},
			Model:     d.ModelName(),
		})
		req := &llm.CompletionRequest{
			Model:      d.ModelName(),
			Messages:   messages,
			Tools:      d.Tools().Specs(),
			ToolChoice: llm.ToolChoice{Type: "auto"},
			System:     d.Instructions(),
			Stream:     true,
			MaxTokens:  4096,
		}
		stream, err := d.Provider().Stream(ctx, req)
		if err != nil {
			return fmt.Errorf("executor: stream: %w", err)
		}

		// 3. accumulate assistant message
		var text string
		var toolCalls []llm.ToolCall
		turnUsage, streamErr := drainStream(ctx, d, ec, stream, &text, &toolCalls)
		if streamErr != nil {
			if streamErr == context.Canceled || ctx.Err() != nil {
				assistant := llm.Message{Role: llm.RoleAssistant, Content: text, ToolCalls: toolCalls}
				d.Session().Append(assistant)
				d.Emit(event.TurnEndEvent{
					EventBase:  event.EventBase{EventCommon: ec()},
					TurnIndex:  turnIndex,
					StopReason: llm.FinishReasonAborted,
				})
				return context.Canceled
			}
			return streamErr
		}
		// publish the API-reported usage so subsequent assembles can prefer
		// it over the rune/4 estimator, and so the LLMEndEvent payload carries
		// the real cumulative cost (prior version always emitted zero).
		d.RecordUsage(turnUsage)
		totalUsage.PromptTokens += turnUsage.PromptTokens
		totalUsage.CompletionTokens += turnUsage.CompletionTokens
		totalUsage.TotalTokens += turnUsage.TotalTokens
		assistant := llm.Message{Role: llm.RoleAssistant, Content: text, ToolCalls: toolCalls}
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
				StopReason: llm.FinishReasonStop,
			})
			return nil
		}

		// 5. run tools in parallel
		results := e.runToolsParallel(ctx, d, ec, turnID, toolCalls)

		// 6. attach tool results to the assistant snapshot so persistence can
		// store them alongside the call (renderer rebuilds tool_result on reload)
		for i := range assistant.ToolCalls {
			if i < len(results) {
				assistant.ToolCalls[i].Result = &llm.ToolResult{Content: truncateForStore(results[i].Content), IsError: results[i].IsError}
			}
		}
		// 7. append assistant (with results) then tool result messages in original order
		d.Session().Append(assistant)
		for i, tc := range toolCalls {
			d.Session().Append(llm.Message{
				Role:       llm.RoleTool,
				Content:    results[i].Content,
				ToolCallID: tc.ID,
			})
		}
		d.Emit(event.TurnEndEvent{
			EventBase:  event.EventBase{EventCommon: ec()},
			TurnIndex:  turnIndex,
			StopReason: llm.FinishReasonToolCalls,
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
func drainStream(ctx context.Context, d Deps, ec func() event.EventCommon, stream *llm.StreamingResponse, textOut *string, callsOut *[]llm.ToolCall) (llm.Usage, error) {
	for ev := range stream.Events {
		switch e := ev.(type) {
		case llm.StartEvent:
			// nothing to do — already emitted LLMStartEvent
		case llm.TextDeltaEvent:
			*textOut += e.Delta
			d.Emit(event.TextDeltaEvent{
				EventBase: event.EventBase{EventCommon: ec()},
				Delta:     e.Delta,
			})
		case llm.ThinkingDeltaEvent:
			d.Emit(event.ThinkingDeltaEvent{
				EventBase: event.EventBase{EventCommon: ec()},
				Delta:     e.Delta,
			})
		case llm.ToolCallStartEvent:
			// we accumulate via End event; nothing here
		case llm.ToolCallDeltaEvent:
			// no-op: provider delivers parsed Arguments in End
		case llm.ToolCallEndEvent:
			*callsOut = append(*callsOut, llm.ToolCall{
				ID:        e.ID,
				Name:      e.Name,
				Arguments: e.Arguments,
			})
		case llm.DoneEvent:
			return e.Response.Usage, nil
		case llm.ErrorEvent:
			if ctx.Err() != nil {
				return llm.Usage{}, context.Canceled
			}
			if err := stream.Err(); err != nil {
				return llm.Usage{}, err
			}
			return llm.Usage{}, fmt.Errorf("executor: stream error event")
		}
	}
	// channel closed without DoneEvent — likely ctx cancel
	if ctx.Err() != nil {
		return llm.Usage{}, context.Canceled
	}
	if err := stream.Err(); err != nil {
		return llm.Usage{}, err
	}
	return llm.Usage{}, nil
}

func (e *defaultExecutor) runToolsParallel(ctx context.Context, d Deps, ec func() event.EventCommon, turnID string, calls []llm.ToolCall) []tool.Result {
	results := make([]tool.Result, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func(i int, c llm.ToolCall) {
			defer wg.Done()
			d.Emit(event.ToolStartEvent{
				EventBase: event.EventBase{EventCommon: ec()},
				TurnID:    turnID,
				CallID:    c.ID,
				Name:      c.Name,
				Arguments: c.Arguments,
			})
			tctx, cancel := context.WithTimeout(ctx, d.Config().ToolTimeout)
			defer cancel()
			start := time.Now()
			// ctx = parent run ctx (permission wait is NOT capped by the tool
			// timeout — the modal needs up to its own 60s), tctx = tool timeout.
			results[i] = executeOneTool(ctx, tctx, d, c)
			d.Emit(event.ToolEndEvent{
				EventBase: event.EventBase{EventCommon: ec()},
				CallID:    c.ID,
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

func executeOneTool(ctx context.Context, tctx context.Context, d Deps, c llm.ToolCall) (res tool.Result) {
	t := d.Tools().Get(c.Name)
	if t == nil {
		return tool.Result{IsError: true, Content: fmt.Sprintf("tool not found: %s", c.Name)}
	}
	defer func() {
		if r := recover(); r != nil {
			res = tool.Result{IsError: true, Content: fmt.Sprintf("tool %q panicked: %v", c.Name, r)}
		}
	}()
	// 权限门（spec 12）：越授权根 / 危险操作 → 请求用户审批。命中记住规则
	// 时直接放行（不弹窗）。ctx（非 tctx）作为等待上下文，避免被工具超时提前砍掉。
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
				return tool.Result{IsError: true, Content: "权限请求失败：" + err.Error()}
			}
			if pr.Behavior != "allow" {
				msg := "用户拒绝了该操作"
				if pr.Message != "" {
					msg = pr.Message
				}
				return tool.Result{IsError: true, Content: msg}
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
