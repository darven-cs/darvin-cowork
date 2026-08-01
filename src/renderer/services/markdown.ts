/**
 * markdown 渲染服务：markdown-it 管线 + 代码块切分 + 大文档截断阈值。
 * 纯函数（可单测）；DOMPurify 净化放组件层，因为需要 DOM。
 */
// eslint-disable-next-line import/no-named-as-default -- markdown-it 是 export= 模块，默认导入即类本身
import MarkdownIt from 'markdown-it';
import type { Token } from 'markdown-it';
import taskLists from 'markdown-it-task-lists';
import mark from 'markdown-it-mark';
import katexPlugin from '@vscode/markdown-it-katex';

export const LARGE_MARKDOWN_THRESHOLD = 8 * 1024;
export const LARGE_MARKDOWN_HEAD_LENGTH = 4 * 1024;
export const LARGE_MARKDOWN_TAIL_LENGTH = 8 * 1024;

export type MarkdownSegment =
  | { type: 'html'; html: string }
  | { type: 'code'; lang: string; code: string };

function createMarkdownIt() {
  const md = new MarkdownIt({ html: false, linkify: true, breaks: true, typographer: false });
  md.use(taskLists);
  md.use(mark);
  md.use(katexPlugin);
  return md;
}

const sharedMd = createMarkdownIt();

/**
 * 把 markdown 切成「普通 HTML 段 + 代码块段」。fence 从 token 流里单独提出，
 * 交给 CodeBlock 组件用 Shiki 高亮，避免在 v-html 里做 DOM 手术。
 */
export function renderMarkdownSegments(content: string): MarkdownSegment[] {
  const tokens = sharedMd.parse(content, {});
  const segments: MarkdownSegment[] = [];
  let pending: Token[] = [];

  const flush = () => {
    if (pending.length === 0) return;
    const html = sharedMd.renderer.render(pending, sharedMd.options, {});
    pending = [];
    if (html) segments.push({ type: 'html', html });
  };

  for (const token of tokens) {
    if (token.type === 'fence') {
      flush();
      const lang = (token.info.trim().split(/\s+/)[0] || 'text').toLowerCase();
      segments.push({ type: 'code', lang, code: token.content });
    } else {
      pending.push(token);
    }
  }
  flush();
  return segments;
}

export function shouldUseLargeMarkdownPreview(content: string): boolean {
  return content.length > LARGE_MARKDOWN_THRESHOLD;
}

/** 头 4KB + 尾 8KB + `...` 占位；LobsterAI 同款策略。 */
export function getLargeMarkdownPreview(content: string): string {
  if (content.length <= LARGE_MARKDOWN_HEAD_LENGTH + LARGE_MARKDOWN_TAIL_LENGTH) return content;
  return [
    content.slice(0, LARGE_MARKDOWN_HEAD_LENGTH).trimEnd(),
    '',
    '...',
    '',
    content.slice(-LARGE_MARKDOWN_TAIL_LENGTH).trimStart(),
  ].join('\n');
}

export function formatContentSize(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  return `${Math.ceil(bytes / 1024)} KB`;
}
