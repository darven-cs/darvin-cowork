/**
 * Shiki 语法高亮：shiki/core 按需加载 + JS regex engine（无 WASM，Electron 离线可用）。
 * 模块级单例，懒初始化一次，之后 codeToHtml 同步调用。
 */
import { createHighlighterCore } from 'shiki/core';
import type { HighlighterCore } from 'shiki/core';
import { createJavaScriptRegexEngine } from 'shiki/engine/javascript';
import type { LanguageRegistration } from '@shikijs/types';

import typescript from '@shikijs/langs/typescript';
import javascript from '@shikijs/langs/javascript';
import jsx from '@shikijs/langs/jsx';
import tsx from '@shikijs/langs/tsx';
import python from '@shikijs/langs/python';
import bash from '@shikijs/langs/bash';
import shell from '@shikijs/langs/shell';
import yaml from '@shikijs/langs/yaml';
import json from '@shikijs/langs/json';
import go from '@shikijs/langs/go';
import rust from '@shikijs/langs/rust';
import vue from '@shikijs/langs/vue';
import css from '@shikijs/langs/css';
import scss from '@shikijs/langs/scss';
import html from '@shikijs/langs/html';
import xml from '@shikijs/langs/xml';
import sql from '@shikijs/langs/sql';
import markdown from '@shikijs/langs/markdown';
import diff from '@shikijs/langs/diff';
import githubLight from '@shikijs/themes/github-light';
import githubDark from '@shikijs/themes/github-dark';

const LANGS: LanguageRegistration[] = [
  typescript, javascript, jsx, tsx, python, bash, shell, yaml, json, go,
  rust, vue, css, scss, html, xml, sql, markdown, diff,
].flat();

const THEMES = [githubLight, githubDark];

export type ShikiHighlighter = HighlighterCore;

const LANG_ALIASES: Record<string, string> = {
  ts: 'typescript',
  tsx: 'tsx',
  js: 'javascript',
  jsx: 'jsx',
  py: 'python',
  sh: 'shell',
  bash: 'shell',
  zsh: 'shell',
  yml: 'yaml',
  md: 'markdown',
};

const LOADED_LANG_NAMES = new Set<string>(
  LANGS.flatMap((l) => [l.name, ...(l.aliases ?? [])]),
);

let highlighterPromise: Promise<ShikiHighlighter> | null = null;

export function getHighlighter(): Promise<ShikiHighlighter> {
  if (!highlighterPromise) {
    highlighterPromise = createHighlighterCore({
      themes: THEMES,
      langs: LANGS,
      engine: createJavaScriptRegexEngine(),
    });
  }
  return highlighterPromise;
}

/** 返回可高亮的 shiki 语言名；未知 / 纯文本返回 null（走纯文本兜底）。 */
export function resolveLang(lang: string): string | null {
  const key = lang.trim().toLowerCase();
  if (!key || key === 'text' || key === 'plain' || key === 'plaintext') return null;
  const resolved = LANG_ALIASES[key] ?? key;
  return LOADED_LANG_NAMES.has(resolved) ? resolved : null;
}
