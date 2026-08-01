package acp

import (
	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agent"
	"darvin-cowork/backend/internal/agent/ctxengine"
	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/session"
	"darvin-cowork/backend/internal/agent/store"
	"darvin-cowork/backend/internal/agent/tool"
)

// AgentFactory 携带构建一个 *agent.Agent 所需的共享依赖;main.go 构造
// 一次并注入 SessionManager,SessionManager 在懒建路径里调
// NewAcpSession。Provider / Store / Logger / Tools / Assembler 在 Agent
// 看来都是只读,只有 conversation history 走 session.Session 自己的锁。
type AgentFactory struct {
	Name         string
	Instructions string
	Model        agent.ModelRef
	Provider     llm.ModelProvider
	Store        store.SessionStore
	MessageStore store.MessageStore
	Logger       *zap.Logger
	Config       agent.Config
	Tools        *tool.Registry
	Assembler    ctxengine.ContextEngine

	// AssemblerEnabled 与 Config.AssemblerEnabled 是两条独立开关:后者
	// 决定是否构造默认 Assembler,前者决定 executor 是否走 assembler
	// 路径。factory 不替调用方合并这两条,各自由调用方决定。
	AssemblerEnabled bool
}

// Build 用传入的 sessionID 构造一个全新的 *agent.Agent。Agent 自己的
// session.ID 是 dispatcher 给事件打 sessionId 字段的唯一来源 —— 错了
// EventLedger.publishLocked 就会路由到错的桶。
func (f *AgentFactory) Build(sessionID string) (*agent.Agent, error) {
	return agent.New(agent.NewAgentConfig{
		Name:             f.Name,
		Instructions:     f.Instructions,
		Model:            f.Model,
		Provider:         f.Provider,
		Session:          session.NewSession(sessionID),
		Store:            f.Store,
		MessageStore:     f.MessageStore,
		Logger:           f.Logger,
		Config:           f.Config,
		Tools:            f.Tools,
		Assembler:        f.Assembler,
		AssemblerEnabled: f.AssemblerEnabled,
	})
}

// NewAcpSession 一次构造 Agent + Loop,并把 Loop 的 CurrentMessageID /
// CurrentRunID 挂到 Agent 上,确保事件带的 messageID / runID 与 Loop 当前
// 状态一致。顺序必须先建 Loop 再 AttachMessageIDSrc,否则 executor
// Deps.Current* 解析时会拿到空字符串。
//
// MessageStore 注入时同时挂 TextDeltaHook(streaming 落库,spec FR-4);
// hook 的订阅由 AcpSession.Close 在 evict 时清理。
func (f *AgentFactory) NewAcpSession(sessionID string) (*AcpSession, error) {
	a, err := f.Build(sessionID)
	if err != nil {
		return nil, err
	}
	l := NewLoop(a)
	a.AttachMessageIDSrc(l.CurrentMessageID)
	a.AttachRunIDSrc(l.CurrentRunID)
	a.AttachUserMessageIDSrc(l.CurrentUserMessageID)
	deltaHook := agent.NewTextDeltaHook(f.MessageStore, f.Logger)
	deltaHook.Attach(a)
	return &AcpSession{
		SessionID: sessionID,
		Agent:     a,
		Loop:      l,
		DeltaHook: deltaHook,
	}, nil
}
