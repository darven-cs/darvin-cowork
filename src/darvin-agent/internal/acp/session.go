package acp

import (
	"darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/harness"
)

// AcpSession 捆绑一个 session id 下的 Agent + Harness + Loop。
// SessionManager 在首次 prompt 懒建,在 evict 时拆掉。
//
// Harness 是 spec 04 引入的新字段:Loop.executeTurn 通过它走
// harness.RunAttemptWithLifecycle 驱动 prompt 路径,而不是直接调
// Agent.Prompt + Agent.Run。skill 路径仍然直接调 Agent,因为 skill
// 需要 Agent 持有的 transient state(RunSkillPrompt / RunSkillTools)。
type AcpSession struct {
	SessionID string
	Agent     *agent.Agent
	Harness   harness.Harness
	Loop      *Loop
	// DeltaHook 订阅 Agent bus 的 text_delta 做 streaming 落库
	// (spec FR-4)。可能为 nil(测试 / 未注入 MessageStore 的 factory)。
	DeltaHook *agent.TextDeltaHook
}

// Close 关闭 DeltaHook 订阅 + Loop 并等 run goroutine 退出。幂等。
//
// 注意:Loop.Close 会阻塞到 in-flight turn 退出,SessionManager 因此
// 把 Close 放在后台 goroutine 跑 —— entry.cancel 触发的是 ctx.Done(),
// 真正关 Loop 的是监听 ctx 的 goroutine,不是 cancel 路径本身。
func (s *AcpSession) Close() {
	if s == nil {
		return
	}
	if s.DeltaHook != nil {
		s.DeltaHook.Close()
	}
	if s.Loop != nil {
		s.Loop.Close()
	}
}
