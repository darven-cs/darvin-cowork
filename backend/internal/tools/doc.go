// Package tools 实现基础工具调用能力。
//
// v0.1 首批内置工具：
//   - 文件读写（受限沙箱目录）
//   - 终端执行（白名单命令 + 超时）
//   - 文本/代码搜索（ripgrep wrapper）
//
// 工具调用协议走 OpenClaw Gateway 的 function call 通道，
// Agent 在 internal/agent 里编排调用顺序，本包只负责单工具执行 + 校验。
//
// v0.1 排期：
//   - 第 2 周：文件 + 终端工具骨架
//   - 第 3 周：搜索工具 + 权限审批回调（与 Electron 渲染层联动）
//
// 详见 docs/v0.1.md §2.3；docs/research-openclaw.md §4。
package tools
