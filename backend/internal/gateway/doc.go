// Package gateway 实现 OpenClaw Gateway WebSocket 客户端。
//
// v0.1 第 1 周 Day 3-5 通信地基填充：
//   - client.go  : 底层 WebSocket 连接（拉 OpenClaw gateway-client SDK 翻译）
//   - manager.go : 引擎生命周期（spawn / WaitReady / IsHealthy / HardRestart）
//   - events.go  : 8 个非功能机制（tick 看门狗、指数退避重连…）
//
// 详见 docs/research-openclaw.md §3.3、docs/lobsterai-borrowed-patterns.md §7。
package gateway
