// Package agent 实现 Agent 推理调度引擎。
//
// 负责把用户消息编排成 LLM 调用 + 工具调用 + 记忆查询的组合流程，
// 管理 token 预算、上下文窗口、并发任务和中断恢复。
//
// v0.1 排期：
//   - 第 2 周：单轮 LLM 调用骨架（直连 OpenClaw Gateway 推理通道）
//   - 第 3 周：多轮编排、工具循环（与 internal/tools 联动）
//
// 详见 docs/v0.1.md §2、§4.2；docs/lobsterai-borrowed-patterns.md §3。
package agent
