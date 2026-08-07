package agentloop

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/tools"
)

// AgentFactory 携带构建一个 *agent.Agent 所需的共享依赖;main.go 构造
// 一次并注入 SessionManager,SessionManager 在懒建路径里调
// NewAgentLoopSession。Provider / Store / Logger / Tools / Assembler 在 Agent
// 看来都是只读,只有 conversation history 走 session.Session 自己的锁。
type AgentFactory struct {
	Name         string
	Instructions string
	Model        agent.ModelRef
	Provider     llm.ModelProvider
	Store        store.SessionStore
	MessageStore store.MessageStore
	UsageStore   store.UsageStore
	Logger       *zap.Logger
	Config       agent.Config
	Tools        *tool.Registry
	Assembler    ctxengine.ContextEngine

	// Plugins 在每次 Build 后应用到该 agent 的 tool registry(skill /
	// mcp 插件)。SessionManager.RefreshAllTools 复用同一组插件做全量
	// Unregister + Register,让工具面跟随 skill / mcp 状态变化。
	Plugins []tool.Plugin

	// AssemblerEnabled 与 Config.AssemblerEnabled 是两条独立开关:后者
	// 决定是否构造默认 Assembler,前者决定 executor 是否走 assembler
	// 路径。factory 不替调用方合并这两条,各自由调用方决定。
	AssemblerEnabled bool

	// HarnessID pins a specific harness by id. An empty
	// value defers to harness.SelectHarness at NewAgentLoopSession time.
	HarnessID string

	// Selector is the factory's harness selector. nil falls back to a
	// built-in helper that uses harness.SelectHarness with the factory's
	// provider / model / explicit-id state. Production wiring uses the
	// default; tests inject a fake to keep the registry deterministic.
	Selector HarnessSelector
}

// HarnessSelector chooses a harness for a given session. The default
// implementation goes through harness.SelectHarness.
type HarnessSelector func(a *agent.Agent, f *AgentFactory) (harness.Harness, error)

// NewAgentLoopSession 一次构造 Agent + Harness + Loop,并把 Loop 的 CurrentMessageID /
// CurrentRunID 挂到 Agent 上,确保事件带的 messageID / runID 与 Loop 当前
// 状态一致。顺序必须先建 Loop 再 AttachMessageIDSrc,否则 executor
// Deps.Current* 解析时会拿到空字符串。
//
// MessageStore 注入时同时挂 TextDeltaHook(streaming 落库);
// hook 的订阅由 AgentLoopSession.Close 在 evict 时清理。
func (f *AgentFactory) NewAgentLoopSession(sessionID string) (*AgentLoopSession, error) {
	a, err := f.Build(sessionID)
	if err != nil {
		return nil, err
	}
	// 从持久化 MessageStore 恢复该 session 的历史消息进内存 Session，
	// 让重启 / entry 重建后的 agent 仍记得之前的对话。失败 warn-and-continue，
	// 不阻塞 AgentLoopSession 构造。
	hydrateSession(context.Background(), f, a.Session())
	h, err := f.resolveHarnessFor(a)
	if err != nil {
		return nil, err
	}
	l := NewLoop(a, h)
	a.AttachMessageIDSrc(l.CurrentMessageID)
	a.AttachRunIDSrc(l.CurrentRunID)
	a.AttachUserMessageIDSrc(l.CurrentUserMessageID)
	deltaHook := agent.NewTextDeltaHook(f.MessageStore, f.Logger)
	deltaHook.Attach(a)
	return &AgentLoopSession{
		SessionID: sessionID,
		Agent:     a,
		Harness:   h,
		Loop:      l,
		DeltaHook: deltaHook,
	}, nil
}

// resolveHarnessFor picks a harness. If the Selector accepts the just-built
// agent, it can wire its closures to drive that exact instance. The default
// Selector path does not need an agent — it goes through harness.SelectHarness
// which only looks at provider / model facts.
func (f *AgentFactory) resolveHarnessFor(a *agent.Agent) (harness.Harness, error) {
	if id := f.HarnessID; id != "" {
		h, ok := harness.Get(id)
		if !ok {
			return nil, fmt.Errorf("acp: harness %q not registered", id)
		}
		return h, nil
	}
	if f.Selector != nil {
		return f.Selector(a, f)
	}
	decision, err := harness.SelectHarness(harness.SelectionParams{
		Provider: extractProviderName(a),
		Model:    f.Model.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("acp: harness selection: %w", err)
	}
	return decision.Harness, nil
}

// Build 用传入的 sessionID 构造一个全新的 *agent.Agent。Agent 自己的
// session.ID 是 dispatcher 给事件打 sessionId 字段的唯一来源 —— 错了
// EventLedger.publishLocked 就会路由到错的桶。每个新 agent 建好后依次
// 应用 Plugins,插件注册失败只记 warn,不阻塞 agent 可用。
func (f *AgentFactory) Build(sessionID string) (*agent.Agent, error) {
	a, err := agent.New(agent.NewAgentConfig{
		Name:             f.Name,
		Instructions:     f.Instructions,
		Model:            f.Model,
		Provider:         f.Provider,
		Session:          session.NewSession(sessionID),
		Store:            f.Store,
		MessageStore:     f.MessageStore,
		UsageStore:       f.UsageStore,
		Logger:           f.Logger,
		Config:           f.Config,
		Tools:            f.Tools,
		Assembler:        f.Assembler,
		AssemblerEnabled: f.AssemblerEnabled,
	})
	if err != nil {
		return nil, err
	}
	for _, p := range f.Plugins {
		reg, ok := a.Tools().(protocol.ToolRegistrar)
		if !ok {
			if f.Logger != nil {
				f.Logger.Warn("agent tools are not a registrar", zap.String("plugin", p.PluginID()))
			}
			continue
		}
		if err := p.Register(reg); err != nil {
			if f.Logger != nil {
				f.Logger.Warn("plugin register failed", zap.String("plugin", p.PluginID()), zap.Error(err))
			}
		}
	}
	return a, nil
}
