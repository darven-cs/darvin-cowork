/* eslint-disable import/no-named-as-default -- export= 模块的默认导入误报警告 */
declare module 'markdown-it-task-lists' {
  import type MarkdownIt from 'markdown-it';
  const plugin: (md: MarkdownIt, options?: { enabled?: boolean; label?: boolean; labelAfter?: boolean }) => void;
  export default plugin;
}

declare module 'markdown-it-mark' {
  import type MarkdownIt from 'markdown-it';
  const plugin: (md: MarkdownIt) => void;
  export default plugin;
}
