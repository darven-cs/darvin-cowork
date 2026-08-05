/**
 * 工具结果展示的纯函数集。
 *
 * 只做字符串 / 结构变换，不碰 DOM 与 i18n，保证可单测。组件层负责把
 * 返回的 preview / sizeLabel 渲染成 UI，并用 t() 补文案。
 */
import type { DarvinToolKind } from '../../shared/darvin-api';

// ── 工具名归一化 ─────────────────────────────────────────────────────────────

/** 归一化：小写 + 去掉空白 / 下划线，与 LobsterAI 的 normalizeToolName 对齐。 */
export function normalizeToolName(tool: string): string {
  return tool.toLowerCase().replace(/[\s_]+/g, '');
}

const DISPLAY_NAMES: Record<string, string> = {
  bash: 'Bash', exec: 'Bash', shell: 'Bash', run: 'Bash',
  read: 'Read', readfile: 'Read', listdir: 'Read', glob: 'Read', grep: 'Read',
  write: 'Write', writefile: 'Write',
  edit: 'Edit', editfile: 'Edit', multiedit: 'Edit',
  todowrite: 'TodoWrite',
  web_search: 'Web Search', websearch: 'Web Search',
  web_fetch: 'Web Fetch', webfetch: 'Web Fetch',
  image_gen: 'Image', video_gen: 'Video',
};

/** `Read/ReadFile → Read`、`Bash/Exec/Shell → Bash` 等；未知工具原样返回。 */
export function getToolDisplayName(tool: string | undefined): string {
  if (!tool) return 'Tool';
  return DISPLAY_NAMES[normalizeToolName(tool)] ?? tool;
}

const TOOL_KIND_ALIASES: Record<string, DarvinToolKind> = {
  read: 'read', readfile: 'read', listdir: 'read', glob: 'read', grep: 'read',
  write: 'write', writefile: 'write',
  edit: 'edit', editfile: 'edit', multiedit: 'edit',
  bash: 'bash', exec: 'bash', shell: 'bash', run: 'bash',
  todowrite: 'todowrite',
  web_search: 'web_search', websearch: 'web_search',
  web_fetch: 'web_fetch', webfetch: 'web_fetch',
  image_gen: 'image_gen', video_gen: 'video_gen',
};

/** 把工具名归一化成 `DarvinToolKind`；未知工具按原样字符串兜底。 */
export function getToolKind(tool: string): DarvinToolKind {
  return TOOL_KIND_ALIASES[normalizeToolName(tool)] ?? (tool as DarvinToolKind);
}

export const isBashLikeToolName = (tool: string | undefined): boolean =>
  Boolean(tool && ['bash', 'exec', 'shell', 'run'].includes(normalizeToolName(tool)));

export const isTodoWriteToolName = (tool: string | undefined): boolean =>
  Boolean(tool && normalizeToolName(tool) === 'todowrite');

// ── Tool input 摘要 ──────────────────────────────────────────────────────────

function getToolInputString(input: Record<string, unknown>, keys: string[]): string | null {
  for (const key of keys) {
    const value = input[key];
    if (typeof value === 'string' && value.trim()) return value;
  }
  return null;
}

const truncatePreview = (value: string, maxLength = 120): string =>
  value.length <= maxLength ? value : `${value.slice(0, maxLength - 3)}...`;

/**
 * 生成工具输入的一行摘要：bash 显示命令、read/write/edit 显示文件路径、
 * webfetch 显示 url；匹配不到返回 null（由调用方回退到 JSON 全文）。
 */
export function getToolInputSummary(
  toolName: string | undefined,
  toolInput: Record<string, unknown> | undefined,
): string | null {
  if (!toolName || !toolInput) return null;
  const input = toolInput;

  if (isBashLikeToolName(toolName)) {
    return getToolInputString(input, ['command', 'cmd', 'script'])
      ?? (Array.isArray(input.commands)
        ? (input.commands as unknown[]).filter((c): c is string => typeof c === 'string').join('\n')
        : null);
  }

  if (isTodoWriteToolName(toolName)) {
    const items = parseTodoWriteItems(input);
    return items ? getTodoWriteSummary(items) : null;
  }

  const normalized = normalizeToolName(toolName);
  switch (normalized) {
    case 'read':
    case 'readfile':
    case 'write':
    case 'writefile':
    case 'edit':
    case 'editfile':
    case 'multiedit':
      return getToolInputString(input, ['file_path', 'path', 'filePath', 'target_file', 'targetFile'])
        ?? (typeof input.content === 'string' && input.content.trim()
          ? truncatePreview(input.content.split('\n')[0].trim())
          : null);
    case 'webfetch':
    case 'web_fetch':
      return getToolInputString(input, ['url', 'query']);
    case 'glob':
    case 'grep':
      return getToolInputString(input, ['pattern', 'query']);
    default:
      return null;
  }
}

export function formatToolInput(
  toolName: string | undefined,
  toolInput: unknown,
): string {
  if (toolInput && typeof toolInput === 'object') {
    const summary = getToolInputSummary(toolName, toolInput as Record<string, unknown>);
    if (summary && summary.trim()) return summary;
  }
  return getToolResultText(toolInput);
}

// ── Tool result 文本化 + 截断 ────────────────────────────────────────────────

export const TOOL_COLLAPSE_THRESHOLD = 4 * 1024;
export const TOOL_PREVIEW_LINES = 12;

export function getToolResultText(output: unknown): string {
  if (typeof output === 'string') return output;
  if (output === undefined || output === null) return '';
  try {
    return JSON.stringify(output, null, 2);
  } catch {
    return String(output);
  }
}

function formatToolResultSize(charCount: number): string {
  if (charCount < 1024) return `${charCount} B`;
  if (charCount < 1024 * 1024) return `${Math.ceil(charCount / 1024)} KB`;
  return `${(charCount / (1024 * 1024)).toFixed(1)} MB`;
}

export interface ToolResultCollapsedDisplay {
  preview: string;
  sizeLabel: string;
  lineCount: number;
  isTruncated: boolean;
}

/**
 * 结果折叠态：>4KB 标记截断，始终给前 12 行 preview + KB/MB 大小摘要 +
 * 总行数。组件据此决定是否显示「展开」按钮。
 */
export function getToolResultCollapsedDisplay(output: unknown): ToolResultCollapsedDisplay {
  const text = getToolResultText(output);
  const lines = text.split('\n');
  return {
    preview: lines.slice(0, TOOL_PREVIEW_LINES).join('\n'),
    sizeLabel: formatToolResultSize(text.length),
    lineCount: lines.length,
    isTruncated: text.length > TOOL_COLLAPSE_THRESHOLD,
  };
}

// ── TodoWrite ────────────────────────────────────────────────────────────────

export type TodoStatus = 'completed' | 'in_progress' | 'pending' | 'unknown';

export interface ParsedTodoItem {
  primaryText: string;
  secondaryText: string | null;
  status: TodoStatus;
}

export function normalizeTodoStatus(value: unknown): TodoStatus {
  const normalized = typeof value === 'string'
    ? value.trim().toLowerCase().replace(/-/g, '_')
    : '';
  if (normalized === 'completed') return 'completed';
  if (normalized === 'in_progress' || normalized === 'running') return 'in_progress';
  // 无 status 字段 / 空串视为待办（spec 三态：completed / in_progress / pending）
  if (normalized === 'pending' || normalized === 'todo' || normalized === '') return 'pending';
  return 'unknown';
}

/** 从 todowrite input 解析三态 checkbox 列表；无 todos 数组返回 null。 */
export function parseTodoWriteItems(input: unknown): ParsedTodoItem[] | null {
  if (!input || typeof input !== 'object') return null;
  const record = input as Record<string, unknown>;
  if (!Array.isArray(record.todos)) return null;

  const items: ParsedTodoItem[] = [];
  for (const raw of record.todos) {
    if (!raw || typeof raw !== 'object') continue;
    const todo = raw as Record<string, unknown>;
    const activeForm = typeof todo.activeForm === 'string' ? todo.activeForm.trim() : '';
    const content = typeof todo.content === 'string' ? todo.content.trim() : '';
    const primaryText = activeForm || content || 'Untitled';
    const secondaryText = content && content !== primaryText ? content : null;
    items.push({ primaryText, secondaryText, status: normalizeTodoStatus(todo.status) });
  }
  return items.length > 0 ? items : null;
}

export function getTodoWriteSummary(items: ParsedTodoItem[]): string {
  const completed = items.filter((i) => i.status === 'completed').length;
  const inProgress = items.filter((i) => i.status === 'in_progress').length;
  const pending = items.length - completed - inProgress;
  return `${items.length} items · ${completed} done · ${inProgress} running · ${pending} pending`;
}

// ── Edit Diff（红绿对比，DiffView 组件消费） ─────────────────────────────────

export type DiffLineType = 'added' | 'removed' | 'context';

export interface DiffLine {
  type: DiffLineType;
  text: string;
  oldLineNo: number | null;
  newLineNo: number | null;
}

/**
 * 行级 LCS diff。小输入用 O(n*m) DP，大输入退回贪心匹配，避免内存爆炸。
 * 纯函数，DiffView 只做渲染。
 */
export function computeDiffLines(oldStr: string, newStr: string): DiffLine[] {
  const oldLines = oldStr.split('\n');
  const newLines = newStr.split('\n');
  if (oldLines.length * newLines.length > 500_000) return greedyDiff(oldLines, newLines);

  const m = oldLines.length;
  const n = newLines.length;
  const dp: number[][] = Array.from({ length: m + 1 }, () => new Array<number>(n + 1).fill(0));
  for (let i = 1; i <= m; i++) {
    for (let j = 1; j <= n; j++) {
      dp[i][j] = oldLines[i - 1] === newLines[j - 1]
        ? dp[i - 1][j - 1] + 1
        : Math.max(dp[i - 1][j], dp[i][j - 1]);
    }
  }

  const result: DiffLine[] = [];
  let i = m;
  let j = n;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && oldLines[i - 1] === newLines[j - 1]) {
      result.push({ type: 'context', text: oldLines[i - 1], oldLineNo: i, newLineNo: j });
      i--;
      j--;
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      result.push({ type: 'added', text: newLines[j - 1], oldLineNo: null, newLineNo: j });
      j--;
    } else {
      result.push({ type: 'removed', text: oldLines[i - 1], oldLineNo: i, newLineNo: null });
      i--;
    }
  }
  return result.reverse();
}

function greedyDiff(oldLines: string[], newLines: string[]): DiffLine[] {
  const result: DiffLine[] = [];
  let oi = 0;
  let ni = 0;
  while (oi < oldLines.length && ni < newLines.length) {
    if (oldLines[oi] === newLines[ni]) {
      result.push({ type: 'context', text: oldLines[oi], oldLineNo: oi + 1, newLineNo: ni + 1 });
      oi++;
      ni++;
    } else {
      let foundOld = -1;
      let foundNew = -1;
      const lookAhead = Math.min(10, Math.max(oldLines.length - oi, newLines.length - ni));
      for (let d = 1; d <= lookAhead; d++) {
        if (oi + d < oldLines.length && oldLines[oi + d] === newLines[ni]) { foundOld = d; break; }
        if (ni + d < newLines.length && oldLines[oi] === newLines[ni + d]) { foundNew = d; break; }
      }
      if (foundOld >= 0 && (foundNew < 0 || foundOld <= foundNew)) {
        for (let k = 0; k < foundOld; k++) {
          result.push({ type: 'removed', text: oldLines[oi + k], oldLineNo: oi + k + 1, newLineNo: null });
        }
        oi += foundOld;
      } else if (foundNew >= 0) {
        for (let k = 0; k < foundNew; k++) {
          result.push({ type: 'added', text: newLines[ni + k], oldLineNo: null, newLineNo: ni + k + 1 });
        }
        ni += foundNew;
      } else {
        result.push({ type: 'removed', text: oldLines[oi], oldLineNo: oi + 1, newLineNo: null });
        result.push({ type: 'added', text: newLines[ni], oldLineNo: null, newLineNo: ni + 1 });
        oi++;
        ni++;
      }
    }
  }
  while (oi < oldLines.length) {
    result.push({ type: 'removed', text: oldLines[oi], oldLineNo: oi + 1, newLineNo: null });
    oi++;
  }
  while (ni < newLines.length) {
    result.push({ type: 'added', text: newLines[ni], oldLineNo: null, newLineNo: ni + 1 });
    ni++;
  }
  return result;
}

export interface DiffData {
  filePath?: string;
  oldStr: string;
  newStr: string;
}

const EDIT_OLD_KEYS = ['old_str', 'old_string', 'old_text', 'oldStr', 'oldText', 'search'];
const EDIT_NEW_KEYS = ['new_str', 'new_string', 'new_text', 'newStr', 'newText', 'replace'];
const FILE_PATH_KEYS = ['file_path', 'path', 'filePath', 'target_file', 'targetFile'];

/** 从 edit 工具 input 提取可渲染的 diff；无 old/new 或不是 edit 返回 null。 */
export function extractDiffFromToolInput(
  toolName: string | undefined,
  toolInput: unknown,
): DiffData[] | null {
  if (!toolName || !toolInput || typeof toolInput !== 'object') return null;
  const normalized = normalizeToolName(toolName);
  if (normalized !== 'edit' && normalized !== 'editfile' && normalized !== 'multiedit') return null;

  const input = toolInput as Record<string, unknown>;
  const filePath = extractString(input, FILE_PATH_KEYS);

  const oldStr = extractString(input, EDIT_OLD_KEYS);
  const newStr = extractString(input, EDIT_NEW_KEYS);
  if (oldStr !== null && newStr !== null) {
    return [{ filePath: filePath ?? undefined, oldStr, newStr }];
  }

  const edits = input.edits ?? input.changes ?? input.operations;
  if (Array.isArray(edits)) {
    const diffs: DiffData[] = [];
    for (const edit of edits) {
      if (edit && typeof edit === 'object') {
        const rec = edit as Record<string, unknown>;
        const eOld = extractString(rec, EDIT_OLD_KEYS);
        const eNew = extractString(rec, EDIT_NEW_KEYS);
        if (eOld !== null && eNew !== null) {
          diffs.push({ filePath: filePath ?? undefined, oldStr: eOld, newStr: eNew });
        }
      }
    }
    return diffs.length > 0 ? diffs : null;
  }
  return null;
}

function extractString(obj: Record<string, unknown>, keys: string[]): string | null {
  for (const key of keys) {
    const value = obj[key];
    if (typeof value === 'string') return value;
  }
  return null;
}
