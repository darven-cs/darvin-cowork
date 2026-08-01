/**
 * useMessages — renderer 端消息状态按 session 分桶。
 *
 * 数据所有权归 main：本 composable 只持有 in-memory 缓存。
 * - `messagesBySessionId` 每个 session 一条独立数组，事件按 ev.sessionId
 *   分桶写入，不混。
 * - `streamingSessionIds` 来自 `text_delta` / `thinking_delta` 事件 + 由
 *   `done` / `error` / `agent_end` 清除；sidebar 用它显示 "running" 状态。
 * - `unreadSessionIds`：session 不在 active 时收到了非 agent_end 事件 →
 *   标记 unread；切回时清掉。
 *
 * 视图层用 `currentMessages`（基于 activeSessionId 派生）只画当前 session
 * 的气泡；切 session 由 useSession 推 activeSessionId 过来，再 watch
 * 它拉一次 getMessages 历史 + 清 unread。
 */

import { computed, ref, watch } from 'vue';
import type { DarvinAttachment, DarvinContextUsage, DarvinEvent, DarvinMessage, DarvinToolKind, DarvinUsage } from '../../shared/darvin-api';
import { assertNever } from '../../shared/darvin-api';
import { useSession } from './useSession';
import { useArtifacts } from './useArtifacts';
import type { Artifact } from './useArtifacts';
import { getToolKind } from '../services/toolDisplay';
import { t } from '../services/i18n';
import { showToast } from '../services/toast';
import { formatTokenCount } from '../services/tokenFormat';

export interface Message {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant';
  content: string;
  thinking?: string;
  done: boolean;
  error?: string;
  toolLabel?: string;
  attachments?: DarvinAttachment[];
  model?: string;
  usage?: DarvinUsage;
  createdAt: number;
  // spec 02 — 工具调用条目（tool_use / tool_result）
  kind?: 'tool_use' | 'tool_result';
  toolUseId?: string;
  tool?: string;
  toolKind?: DarvinToolKind;
  input?: unknown;
  output?: unknown;
  isError?: boolean;
  /** spec 11 — 本 assistant 消息产出的 artifact（按 artifact 事件 messageId 挂载）。 */
  artifacts?: Artifact[];
}

/** AssistantTurnBlock 的条目：普通 assistant 消息或一组配对的工具调用。 */
export type AssistantTurnItem =
  | { type: 'assistant'; message: Message }
  | { type: 'tool_group'; toolUse: Message; toolResult: Message | null };

/** 会话项状态（spec 06）：idle / running / completed / error。 */
export type SessionActivityStatus = 'idle' | 'running' | 'completed' | 'error';

/** 一次上下文压缩的渲染标记（divider / toast 数据源）。 */
export interface CompactionMarker {
  checkpointId: string;
  sessionId: string;
  reason: 'auto' | 'manual';
  createdAt: number;
  beforeTokens?: number;
  afterTokens?: number;
}

/** 一个 turn = user 消息 + 其后所有 assistant 消息；无 user 的 assistant 归入 orphan turn。 */
export interface ConversationTurn {
  id: string;
  userMessage: Message | null;
  assistantItems: AssistantTurnItem[];
  /** 该 turn 之前发生过的压缩（divider 渲染在其上方）。 */
  precedingCompactions?: CompactionMarker[];
}

export function buildConversationTurns(
  messages: Message[],
  markers: CompactionMarker[] = [],
): ConversationTurn[] {
  const turns: ConversationTurn[] = [];
  let current: ConversationTurn | null = null;
  let orphanIndex = 0;
  const groupsByToolUseId = new Map<string, Extract<AssistantTurnItem, { type: 'tool_group' }>>();
  let pendingAdjacentGroup: AssistantTurnItem | null = null;
  const sortedMarkers = [...markers].sort((a, b) => a.createdAt - b.createdAt);
  let mi = 0;

  const pushAssistant = (item: AssistantTurnItem): void => {
    if (!current) {
      current = { id: `orphan-${orphanIndex++}`, userMessage: null, assistantItems: [] };
      turns.push(current);
    }
    current.assistantItems.push(item);
  };

  const attachMarkers = (turn: ConversationTurn, anchor: number): void => {
    while (mi < sortedMarkers.length && sortedMarkers[mi].createdAt <= anchor) {
      turn.precedingCompactions = [...(turn.precedingCompactions ?? []), sortedMarkers[mi]];
      mi++;
    }
  };

  for (const msg of messages) {
    if (msg.role === 'user') {
      current = { id: msg.id, userMessage: msg, assistantItems: [] };
      attachMarkers(current, msg.createdAt);
      turns.push(current);
      pendingAdjacentGroup = null;
      continue;
    }

    // 工具调用：tool_use 开新 group，tool_result 按 toolUseId 配对回来
    if (msg.kind === 'tool_use') {
      const group: AssistantTurnItem = { type: 'tool_group', toolUse: msg, toolResult: null };
      pushAssistant(group);
      if (msg.toolUseId) groupsByToolUseId.set(msg.toolUseId, group as Extract<AssistantTurnItem, { type: 'tool_group' }>);
      pendingAdjacentGroup = group;
      continue;
    }

    if (msg.kind === 'tool_result') {
      let matched = false;
      const id = msg.toolUseId;
      const group = id ? groupsByToolUseId.get(id) : undefined;
      if (group) {
        group.toolResult = msg;
        matched = true;
      } else if (pendingAdjacentGroup && pendingAdjacentGroup.type === 'tool_group' && !pendingAdjacentGroup.toolResult) {
        pendingAdjacentGroup.toolResult = msg;
        matched = true;
      }
      pendingAdjacentGroup = null;
      if (!matched) {
        // 孤立 tool_result（缺 tool_use）：让 ToolCallGroup 直接消费自身 output
        pushAssistant({ type: 'tool_group', toolUse: msg, toolResult: null });
      }
      continue;
    }

    pendingAdjacentGroup = null;
    pushAssistant({ type: 'assistant', message: msg });
  }
  return turns;
}

const messagesBySessionId = ref<Record<string, Message[]>>({});
const streamingSessionIds = ref<Set<string>>(new Set());
const unreadSessionIds = ref<Set<string>>(new Set());
/** 每 session 上下文用量快照，来自 Go 的 `context_usage` 事件。 */
const contextUsageBySessionId = ref<Record<string, DarvinContextUsage>>({});
/** 每 session 已发生的压缩标记，来自 Go 的 `compaction` 事件。 */
const compactionsBySessionId = ref<Record<string, CompactionMarker[]>>({});
/** 会话项活动状态（spec 06），从事件流 + 历史消息派生。 */
const sessionStatusBySessionId = ref<Record<string, SessionActivityStatus>>({});

const session = useSession();
const artifacts = useArtifacts();

const currentMessages = computed<Message[]>(
  () => messagesBySessionId.value[session.activeSessionId.value ?? ''] ?? [],
);

const currentCompactions = computed<CompactionMarker[]>(
  () => compactionsBySessionId.value[session.activeSessionId.value ?? ''] ?? [],
);

/**
 * 从已加载消息派生会话活动状态：有 error → error；有已完成 assistant 消息
 * → completed；否则 idle。running 不在此判断（事件流时 streamingSessionIds 覆盖）。
 */
export function deriveSessionStatusFromMessages(msgs: Message[]): SessionActivityStatus {
  if (msgs.some((m) => m.error)) return 'error';
  if (msgs.some((m) => m.role === 'assistant' && m.done)) return 'completed';
  return 'idle';
}

/**
 * 从 main 拉指定 session 的历史消息。
 */
async function loadMessages(sessionId: string): Promise<void> {
  if (typeof window === 'undefined' || !window.darvin) return;
  try {
    const r = await window.darvin.getMessages(sessionId);
    const msgs = r.messages.map(toMessage);
    messagesBySessionId.value = {
      ...messagesBySessionId.value,
      [sessionId]: msgs,
    };
    sessionStatusBySessionId.value = {
      ...sessionStatusBySessionId.value,
      [sessionId]: deriveSessionStatusFromMessages(msgs),
    };
  } catch {
    messagesBySessionId.value = {
      ...messagesBySessionId.value,
      [sessionId]: [],
    };
  }
}

// 切 active session 时清 unread + 拉历史。watch 只在此处建一次，避免每个
// useMessages() 调用点都注册一个 immediate watch、在组件挂载时重复触发
// loadMessages 覆盖正在流式的 bucket。
watch(
  () => session.activeSessionId.value,
  (newId, oldId) => {
    if (newId !== null && unreadSessionIds.value.has(newId)) {
      const next = new Set(unreadSessionIds.value);
      next.delete(newId);
      unreadSessionIds.value = next;
    }
    if (newId !== null && newId !== oldId) {
      void loadMessages(newId);
    }
  },
  { immediate: true },
);

/** 老 Go 的扁平 wire shape（role 在顶层，无 type 判别）。 */
interface LegacyFlatMessage {
  id: string;
  sessionId: string;
  role: string;
  content: string;
  done: boolean;
  error?: string;
  toolLabel?: string;
  createdAt: number;
}

function toMessage(m: DarvinMessage): Message {
  const legacy = m as unknown as LegacyFlatMessage;
  if (legacy.role !== undefined) {
    return {
      id: legacy.id,
      sessionId: legacy.sessionId,
      role: legacy.role === 'user' ? 'user' : 'assistant',
      content: legacy.content,
      done: legacy.done,
      error: legacy.error,
      toolLabel: legacy.toolLabel,
      createdAt: legacy.createdAt,
    };
  }
  switch (m.type) {
    case 'user':
      return {
        id: m.id,
        sessionId: m.sessionId,
        role: 'user',
        content: m.content,
        done: m.done,
        error: m.error,
        attachments: m.attachments,
        createdAt: m.createdAt,
      };
    case 'assistant':
      return {
        id: m.id,
        sessionId: m.sessionId,
        role: 'assistant',
        content: m.content,
        done: m.done,
        error: m.error,
        toolLabel: m.toolLabel,
        model: m.model,
        usage: m.usage,
        createdAt: m.createdAt,
      };
    case 'tool_use':
      return {
        id: m.id,
        sessionId: m.sessionId,
        role: 'assistant',
        content: '',
        done: true,
        kind: 'tool_use',
        toolUseId: m.toolUseId,
        tool: m.tool,
        toolKind: m.toolKind,
        input: m.input,
        createdAt: m.createdAt,
      };
    case 'tool_result':
      return {
        id: m.id,
        sessionId: m.sessionId,
        role: 'assistant',
        content: '',
        done: true,
        kind: 'tool_result',
        toolUseId: m.toolUseId,
        tool: m.tool,
        output: m.output,
        isError: m.isError,
        createdAt: m.createdAt,
      };
    case 'system':
      return {
        id: m.id,
        sessionId: m.sessionId,
        role: 'assistant',
        content: m.content,
        done: true,
        createdAt: m.createdAt,
      };
    default:
      return assertNever(m);
  }
}

// Go 的 ToolEndEvent.Result.IsError 存在但 mapEventToTS 没序列化（spec 规定不改
// Go），渲染层只能从 output 内容推断。命中这些 darvin-agent 实际错误文案视为失败。
const TOOL_ERROR_PATTERNS: RegExp[] = [
  /^<tool_use_error>[\s\S]*<\/tool_use_error>$/i,
  /command not allowed/i,
  /must be one of/i,
  /command not found/i,
  /no such file or directory/i,
  /permission denied/i,
  /not found/i,
  /(^|[\r\n])error\s*:/i,
  /(^|[\r\n])failed\s*:/i,
];

function inferToolEndError(output: unknown): boolean {
  if (output && typeof output === 'object') {
    const rec = output as Record<string, unknown>;
    if (rec.error !== undefined && rec.error !== null) return true;
    if (typeof rec.exitCode === 'number' && rec.exitCode !== 0) return true;
    if (typeof rec.stderr === 'string' && rec.stderr.trim().length > 0) return true;
  }
  if (typeof output === 'string') {
    const trimmed = output.trim();
    if (trimmed && TOOL_ERROR_PATTERNS.some((re) => re.test(trimmed))) return true;
  }
  return false;
}

function appendToBucket(list: Message[], sid: string, ev: DarvinEvent): void {
  if (ev.type === 'text_delta') {
    const msg = list.find((m) => m.id === ev.messageId);
    if (msg) msg.content += ev.delta;
  } else if (ev.type === 'thinking_delta') {
    const msg = list.find((m) => m.id === ev.messageId);
    if (msg) msg.thinking = (msg.thinking ?? '') + ev.delta;
  } else if (ev.type === 'done') {
    const msg = list.find((m) => m.id === ev.messageId);
    if (msg) {
      msg.done = true;
      // spec 03 — done 事件带 usage（in/out/cache），TurnMeta hover 消费
      if (ev.usage) msg.usage = ev.usage;
    }
  } else if (ev.type === 'error') {
    const msg = list.find((m) => m.id === ev.messageId);
    if (msg) {
      msg.done = true;
      msg.error = ev.message;
    }
  } else if (ev.type === 'tool_start') {
    const toolUseId = ev.toolUseId ?? ev.messageId;
    if (list.some((m) => m.kind === 'tool_use' && m.toolUseId === toolUseId)) return; // 幂等
    list.push({
      id: toolUseId,
      sessionId: sid,
      role: 'assistant',
      content: '',
      done: false,
      kind: 'tool_use',
      toolUseId,
      tool: ev.tool,
      toolKind: getToolKind(ev.tool),
      input: ev.input,
      createdAt: Date.now(),
    });
  } else if (ev.type === 'tool_end') {
    const toolUseId = ev.toolUseId ?? ev.messageId;
    const use = list.find((m) => m.kind === 'tool_use' && m.toolUseId === toolUseId);
    list.push({
      id: `${toolUseId}-result`,
      sessionId: sid,
      role: 'assistant',
      content: '',
      done: true,
      kind: 'tool_result',
      toolUseId,
      tool: use?.tool ?? ev.tool,
      output: ev.output,
      isError: inferToolEndError(ev.output),
      createdAt: Date.now(),
    });
  }
}

export function useMessages() {
  function appendUserMessage(sessionId: string, content: string, id?: string, attachments?: DarvinAttachment[]): string {
    const mid = id ?? `m-${Math.random().toString(36).slice(2, 10)}`;
    const bucket = messagesBySessionId.value[sessionId] ?? [];
    bucket.push({
      id: mid, sessionId, role: 'user', content, done: true, attachments, createdAt: Date.now(),
    });
    messagesBySessionId.value = { ...messagesBySessionId.value, [sessionId]: bucket };
    return mid;
  }

  function startAssistantMessage(sessionId: string, id?: string): string {
    const mid = id ?? `m-${Math.random().toString(36).slice(2, 10)}`;
    const bucket = messagesBySessionId.value[sessionId] ?? [];
    bucket.push({
      id: mid, sessionId, role: 'assistant', content: '', done: false, createdAt: Date.now(),
    });
    messagesBySessionId.value = { ...messagesBySessionId.value, [sessionId]: bucket };
    return mid;
  }

  /**
   * 推一条 backend event 进正确的 session bucket，副作用是更新
   * streamingSessionIds / unreadSessionIds。event.sessionId 必填：
   * main 已经注入到 payload 上。
   */
  function appendEvent(ev: DarvinEvent): void {
    const sid = ev.sessionId;
    if (!sid) {
      // 老 backend / 缺字段：当作 active session 处理，保持向后兼容
      const active = session.activeSessionId.value;
      if (active === null) return;
      appendEventFor(active, ev);
      return;
    }
    appendEventFor(sid, ev);
  }

  function appendEventFor(sid: string, ev: DarvinEvent): void {
    // context_usage 是 session 级快照，不落消息 bucket，单独维护
    // contextUsageBySessionId 给 chat header 圆环消费。
    if (ev.type === 'context_usage') {
      const key = ev.usage.sessionId || sid;
      contextUsageBySessionId.value = { ...contextUsageBySessionId.value, [key]: ev.usage };
    }

    if (ev.type === 'artifact') {
      // artifact 进 useArtifacts 面板状态机；带 messageId 时同时挂到对应
      // assistant 消息，聊天流里渲染卡片组（老事件缺 messageId 只进面板，兼容）。
      const artifact: Artifact = {
        id: ev.artifactId,
        kind: ev.kind,
        name: ev.name,
        content: ev.content,
        filePath: ev.filePath,
        messageId: ev.messageId,
        createdAt: ev.createdAt,
      };
      artifacts.addArtifact(sid, artifact);
      if (ev.messageId) {
        const bucket = messagesBySessionId.value[sid] ?? [];
        const msg = bucket.find((m) => m.id === ev.messageId && m.role === 'assistant');
        if (msg) {
          msg.artifacts = [...(msg.artifacts ?? []), artifact];
          messagesBySessionId.value = { ...messagesBySessionId.value, [sid]: bucket };
        }
      }
    }

    if (ev.type === 'compaction') {
      const markers = compactionsBySessionId.value[sid] ?? [];
      if (!markers.some((m) => m.checkpointId === ev.checkpointId)) {
        compactionsBySessionId.value = {
          ...compactionsBySessionId.value,
          [sid]: [...markers, {
            checkpointId: ev.checkpointId,
            sessionId: sid,
            reason: ev.reason,
            createdAt: ev.createdAt,
            beforeTokens: ev.beforeTokens,
            afterTokens: ev.afterTokens,
          }],
        };
      }
      const prev = contextUsageBySessionId.value[sid];
      contextUsageBySessionId.value = {
        ...contextUsageBySessionId.value,
        [sid]: {
          sessionId: sid,
          status: 'normal',
          usedTokens: prev?.usedTokens,
          contextTokens: prev?.contextTokens,
          percent: prev?.percent,
          compactionCount: (prev?.compactionCount ?? 0) + 1,
          latestCompactionAt: ev.createdAt,
          latestCompactionReason: ev.reason,
          model: prev?.model,
          updatedAt: Date.now(),
        },
      };
      showToast(
        t('chat.context.compacted')
          .replace('{before}', formatTokenCount(ev.beforeTokens ?? 0))
          .replace('{after}', formatTokenCount(ev.afterTokens ?? 0)),
        'success',
      );
    }

    const bucket = messagesBySessionId.value[sid] ?? [];
    appendToBucket(bucket, sid, ev);
    messagesBySessionId.value = { ...messagesBySessionId.value, [sid]: bucket };

    const active = session.activeSessionId.value;
    const isCurrent = sid === active;

    // 会话活动状态（spec 06）：流式/工具执行 → running，done → completed，
    // error → error，agent_end → 收尾（若非 error 则 completed）。
    const setStatus = (s: SessionActivityStatus) => {
      sessionStatusBySessionId.value = { ...sessionStatusBySessionId.value, [sid]: s };
    };
    // tool_start / tool_end 也属于"正在跑"（工具执行中 session 持续运行）
    if (ev.type === 'text_delta' || ev.type === 'thinking_delta' || ev.type === 'tool_start' || ev.type === 'tool_end') {
      streamingSessionIds.value = new Set([...streamingSessionIds.value, sid]);
      setStatus('running');
    } else if (ev.type === 'done' || ev.type === 'error' || ev.type === 'agent_end') {
      const next = new Set(streamingSessionIds.value);
      next.delete(sid);
      streamingSessionIds.value = next;
      if (ev.type === 'error') setStatus('error');
      else if (ev.type === 'agent_end') {
        const prev = sessionStatusBySessionId.value[sid];
        if (prev !== 'error') setStatus('completed');
      } else setStatus('completed');
    }

    // 后台 session 的非 lifecycle 事件 → unread 红点。agent_end / context_usage /
    // compaction 不触发：前两者避免 stream 收尾闪烁与用量快照跳动，compaction
    // 是纯压缩边界信号，不该点亮红点。
    if (
      !isCurrent
      && ev.type !== 'agent_end'
      && ev.type !== 'context_usage'
      && ev.type !== 'compaction'
    ) {
      unreadSessionIds.value = new Set([...unreadSessionIds.value, sid]);
    }
  }

  /** 手动压缩点击后把圆环切到 compacting 旋转态。 */
  function beginCompact(sessionId: string): void {
    const prev = contextUsageBySessionId.value[sessionId];
    contextUsageBySessionId.value = {
      ...contextUsageBySessionId.value,
      [sessionId]: {
        sessionId,
        status: 'compacting',
        compactionReason: 'manual',
        usedTokens: prev?.usedTokens,
        contextTokens: prev?.contextTokens,
        percent: prev?.percent,
        compactionCount: prev?.compactionCount,
        latestCompactionAt: prev?.latestCompactionAt,
        model: prev?.model,
        updatedAt: Date.now(),
      },
    };
  }

  /** 压缩未被受理（Go 离线 / 会话不可压）时把圆环还原，不 toast。 */
  function endCompact(sessionId: string): void {
    const prev = contextUsageBySessionId.value[sessionId];
    if (!prev) return;
    contextUsageBySessionId.value = {
      ...contextUsageBySessionId.value,
      [sessionId]: { ...prev, status: 'normal', updatedAt: Date.now() },
    };
  }

  /** 压缩失败：圆环转红 + toast「压缩失败，可重试」。 */
  function failCompact(sessionId: string): void {
    const prev = contextUsageBySessionId.value[sessionId];
    contextUsageBySessionId.value = {
      ...contextUsageBySessionId.value,
      [sessionId]: {
        ...(prev ?? { sessionId, status: 'unknown' as const }),
        status: 'danger',
        updatedAt: Date.now(),
      },
    };
    showToast(t('chat.context.compactionFailed'), 'error');
  }

  /** 会话删除后清掉其消息缓存与 streaming/unread/context/compaction 标记。 */
  function removeSession(sessionId: string): void {
    const bucket = { ...messagesBySessionId.value };
    delete bucket[sessionId];
    messagesBySessionId.value = bucket;
    streamingSessionIds.value = new Set([...streamingSessionIds.value].filter((s) => s !== sessionId));
    unreadSessionIds.value = new Set([...unreadSessionIds.value].filter((s) => s !== sessionId));
    const cu = { ...contextUsageBySessionId.value };
    delete cu[sessionId];
    contextUsageBySessionId.value = cu;
    const comp = { ...compactionsBySessionId.value };
    delete comp[sessionId];
    compactionsBySessionId.value = comp;
    const st = { ...sessionStatusBySessionId.value };
    delete st[sessionId];
    sessionStatusBySessionId.value = st;
    artifacts.clearSessionArtifacts(sessionId);
  }

  function reset(): void {
    messagesBySessionId.value = {};
    streamingSessionIds.value = new Set();
    unreadSessionIds.value = new Set();
    contextUsageBySessionId.value = {};
    compactionsBySessionId.value = {};
    sessionStatusBySessionId.value = {};
  }

  return {
    messagesBySessionId,
    streamingSessionIds,
    unreadSessionIds,
    contextUsageBySessionId,
    compactionsBySessionId,
    sessionStatusBySessionId,
    currentMessages,
    currentCompactions,
    loadMessages,
    appendUserMessage,
    startAssistantMessage,
    appendEvent,
    beginCompact,
    endCompact,
    failCompact,
    removeSession,
    reset,
  };
}
