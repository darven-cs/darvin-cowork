// Package mcp 实现 Model Context Protocol 客户端。
//
// 支持两种 transport：
//   - stdio  ：本地 MCP server 子进程
//   - HTTP/SSE：远程 MCP server（含流式事件）
//
// 职责是发现并加载用户配置的 MCP server，把它们的工具/资源/prompt
// 聚合后暴露给 Agent（internal/agent）作为可调用能力。
//
// v0.1 排期：
//   - 第 4 周：stdio transport 骨架 + 至少一个内置 MCP server 联通
//
// 详见 docs/v0.1.md §2.5；docs/research-openclaw.md §5。
package mcp
