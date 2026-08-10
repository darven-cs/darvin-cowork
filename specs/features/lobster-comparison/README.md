# Lobster 对位分析 SPEC

> 对照参考项目 `~/桌面/github-project/LobsterAI`（全功能桌面 AI 助手），盘点 darvin-agent 在「**日常生活 + 办公 + 编码**」三个场景下缺什么、优先级如何、怎么补。

## 1. 背景

darvin-cowork 当前定位是「个人桌面智能助手」(见 `CLAUDE.md`)。架构已经具备：Electron 壳 + Go agent runtime + WebSocket JSON-RPC + 工具注册中心 + Skills + MCP + 上下文压缩 + Persona。但工具面窄（6 个 built-in）、场景入口窄（单一主 Agent）、无 IM 接入、无 Office/Email/Calendar 工具。

LobsterAI 是个成熟的全场景桌面 AI 助手，约 28 个 shipped skills + 11 个 IM 平台 + 6 个 preset agent + 20 个 LLM provider。**对位它的能力面，darvin 在多个维度都缺；本 spec 把这些缺口列出来，按 Tier 排优先级，作为下一阶段路线图。**

## 2. 对位维度

参考 darvin-agent 自己的 `internal/` 子包结构和 LobsterAI 的 `src/main/` + `SKILLs/` + `openclaw-extensions/`，按下面 10 个维度对位（详见 `CHECKLIST.md`）：

1. **Tools** — 内置工具 / skills
2. **Skills / Plugins** — 第三方能力挂载
3. **Memory & Context** — 长期记忆 / RAG / 压缩
4. **Agent Loop** — 单 agent / sub-agent / planning / todo
5. **IM Gateway** — 消息平台接入
6. **MCP** — 外部工具协议
7. **LLM Providers** — 模型供应
8. **Persona** — 角色 / 身份 / 系统 prompt
9. **Scheduling** — 定时 / 触发
10. **「日常 + 办公 + 编码」场景入口** — PPT / 数据 / 文档 / 邮件 / 日历 / 编码等具体能力

## 3. 一句话结论

**darvin 的「核心引擎 + 协议 + UI 壳」已经成形**；缺的是**场景化能力**（Office / Email / Calendar / Web search / Media gen）和**入口扩展**（IM / Preset agent / Sub-agent / Scheduling）。按用户基群（中文桌面办公 + 编码）排优先级，**Tier 1（Office 三件套 + Web search + Todo/Goal）和 Tier 2（Email + Calendar + 多 LLM provider + Preset agent）是必补**；Tier 3/4 看产品方向再启动。

## 4. 范围

| 范围 | 在本 spec |
|---|---|
| **横向对位（10 维度打分）** | ✅ `CHECKLIST.md` § 1-2 节 |
| **Tier 1-4 缺口清单** | ✅ `CHECKLIST.md` § 3 节 |
| **SUBAGENT 设计稿** | ✅ `subagent.md`（Tier 3 之一，先把设计落到 paper level）→ 2026-08 已抽到 `specs/features/subagent/2026-08-09-subagent-design.md` 进入实现阶段 |
| **其他 Tier 3/4 项的子 spec** | ❌ 不在本 spec；后续按需新建目录 |
| **代码实现** | ❌ 不在本 spec；本 spec 是路线图 + 设计稿，不是任务清单 |

## 5. 实施方式

本 spec 是**路线图**性质，不直接产生代码：

1. **路线图作用**：`CHECKLIST.md` 每项是 `- [ ]` 状态，PR 完成后改成 `- [x]`。
2. **每项 Tier 1-2 的实现** 走现有 SDD 流程：先建子 spec（如 `specs/features/builtin-tools-g-office-suite/...`），再按 spec 实现。
3. **SUBAGENT** 是 Tier 3 的大件，2026-08-09 已从本 spec 抽到独立子 spec `specs/features/subagent/2026-08-09-subagent-design.md` 进入实现阶段；`subagent.md` 作为 paper-level 起点保留。

## 6. 涉及文件

| 文件 | 性质 |
|---|---|
| `README.md` | 本文件：背景 + 范围 + 路线图入口 |
| `CHECKLIST.md` | 横向对位 + Tier 1-4 可勾选清单 |
| `subagent.md` | SUBAGENT 设计稿（Tier 3 之一） |

## 7. 后续

- 每完成 Tier 1/2 一项，把对应 `- [ ]` 改成 `- [x]` 并加 commit hash。
- 任何 Tier 3/4 项启动时，从本 spec 抽成独立子 spec（避免本 spec 越长越乱）。
- 季度对位一次 LobsterAI / 其他参考项目（如后续引入新的对比对象），更新本 CHECKLIST。

---

**更新策略**：本 spec 是「活文档」，跟代码改动解耦 —— 它描述「为什么缺」「优先级如何」，不描述「代码怎么写」。具体代码细节在子 spec 里。
