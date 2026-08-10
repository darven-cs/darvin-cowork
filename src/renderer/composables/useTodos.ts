/**
 * useTodos — Todo artifact tab 的 renderer 状态。
 *
 * 数据来源：当前会话消息桶里的 tool_use 消息，纯派生、不新增 IPC。
 * todo 工具是 stateless（args 即状态）：最后一次 todo_write 的 input.todos
 * 是当前清单，complete_step 的 input 按 content 去重成签收记录。live 事件与
 * 历史回放走同一条消息桶，切 session 时随 currentMessages 自动切换。
 */

import { computed } from 'vue';
import { useMessages } from './useMessages';
import { isTodoWriteToolName, normalizeTodoStatus, normalizeToolName } from '../services/toolDisplay';

export interface TodoItem {
  content: string;
  status: 'pending' | 'in_progress' | 'completed';
  activeForm?: string;
  level: 0 | 1;
}

export interface TodoSignOff {
  stepId: number;
  content: string;
  evidenceCount: number;
  createdAt: number;
}

export interface TodoState {
  items: TodoItem[];
  /** content 去重后的签收记录（Map 累加，后者覆盖）。 */
  signOffs: TodoSignOff[];
  /** 是否存在至少一次 todo_write（todos: [] 也算 true）。 */
  hasList: boolean;
  /** 最后一次 todo_write 的 createdAt。 */
  updatedAt: number;
}

interface TodoWriteInput {
  todos?: TodoItem[];
}

interface CompleteStepInput {
  step_id?: number;
  content?: string;
  evidence?: unknown[];
}

const messages = useMessages();

function isCompleteStepTool(tool: string | undefined): boolean {
  return Boolean(tool && normalizeToolName(tool) === 'completestep');
}

function parseTodoWriteInput(input: unknown): TodoItem[] | null {
  const raw = input as TodoWriteInput | null;
  if (!raw || !Array.isArray(raw.todos)) return null;
  return raw.todos.map((item) => {
    const status = normalizeTodoStatus(item.status);
    return {
      content: typeof item.content === 'string' ? item.content : '',
      status: status === 'in_progress' || status === 'completed' ? status : 'pending',
      activeForm: typeof item.activeForm === 'string' ? item.activeForm : undefined,
      level: item.level === 1 ? 1 : 0,
    };
  });
}

function parseCompleteStepInput(input: unknown, createdAt: number): TodoSignOff | null {
  const raw = input as CompleteStepInput | null;
  if (!raw || typeof raw.content !== 'string' || raw.content.trim() === '') return null;
  return {
    stepId: typeof raw.step_id === 'number' ? raw.step_id : -1,
    content: raw.content,
    evidenceCount: Array.isArray(raw.evidence) ? raw.evidence.length : 0,
    createdAt,
  };
}

const state = computed<TodoState>(() => {
  let items: TodoItem[] | null = null;
  let updatedAt = 0;
  const signOffMap = new Map<string, TodoSignOff>();

  for (const msg of messages.currentMessages.value) {
    if (msg.kind !== 'tool_use') continue;
    if (isTodoWriteToolName(msg.tool)) {
      const parsed = parseTodoWriteInput(msg.input);
      if (parsed) {
        items = parsed;
        updatedAt = msg.createdAt;
      }
    } else if (isCompleteStepTool(msg.tool)) {
      const so = parseCompleteStepInput(msg.input, msg.createdAt);
      if (so) signOffMap.set(so.content, so);
    }
  }

  return {
    items: items ?? [],
    signOffs: [...signOffMap.values()],
    hasList: items !== null,
    updatedAt,
  };
});

export function useTodos() {
  return {
    items: computed(() => state.value.items),
    signOffs: computed(() => state.value.signOffs),
    hasList: computed(() => state.value.hasList),
    updatedAt: computed(() => state.value.updatedAt),
  };
}
