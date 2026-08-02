---
name: web-search
description: 联网搜索 PoC 占位
version: 0.1.0
invocation:
  userInvocable: true
---

# Web Search Skill

PoC 占位。规范定义 search 工具的契约；v0 不执行真实网络请求。

设计参考：

- 启动本地 server（scripts/start-server.sh，仅文档说明）
- 提供 search 客户端（scripts/search.sh，仅文档说明）
- 由 agent loop 视具体上下文调用
