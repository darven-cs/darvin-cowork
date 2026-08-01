/**
 * 读写主进程侧的"用户级"yaml：darvin-cowork 的个人偏好（llm / app / memory / locale）。
 *
 * 文件位置走 os.userConfigDir()，与 Go 侧 config.UserConfigPath() 一致：
 *   Linux:   ~/.config/darvin-cowork/config.yaml
 *   macOS:   ~/Library/Application Support/darvin-cowork/config.yaml
 *   Windows: %APPDATA%/darvin-cowork/config.yaml
 *
 * 这里手工用 yaml 文本拼接（不引第三方 yaml 库），避免给 main 加一个
 * 体积不小的依赖；字段名映射严格按 Go 侧 mapstructure tag，确保
 * 双方解析后的对象一致。多 provider 的 `providers` 块是 main/renderer 侧
 * 预留的凭据存储（Go 暂未消费），active provider 始终写 `llm.provider`。
 */

import fs from 'node:fs/promises';
import path from 'node:path';
import type { DarvinLocale } from '../../shared/darvin-api';
import { userSettingsPath } from './user-paths';

export interface UserSettingsLLM {
  provider?: string;
  api_key?: string;
  base_url?: string;
  default_model?: string;
}

export interface UserSettingsProviderEntry {
  api_key?: string;
  base_url?: string;
  default_model?: string;
}

export interface UserSettingsApp {
  auto_launch?: boolean;
  notifications?: boolean;
  proxy?: string;
}

export interface UserSettingsMemory {
  enabled?: boolean;
  embedding_provider?: string;
  api_key?: string;
}

export interface UserSettings {
  llm?: UserSettingsLLM;
  providers?: Record<string, UserSettingsProviderEntry>;
  app?: UserSettingsApp;
  memory?: UserSettingsMemory;
  locale?: DarvinLocale;
}

/**
 * 读取用户级 yaml，文件不存在返 null（首次安装）。
 */
export async function readUserSettingsYAML(): Promise<UserSettings | null> {
  try {
    const raw = await fs.readFile(userSettingsPath(), 'utf8');
    return parseSimpleYaml(raw);
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code === 'ENOENT') return null;
    throw e;
  }
}

/**
 * 把 patch 合并到现有用户级 yaml 后写回。读 + 改 + 写：
 *   - 字段不在 patch 里则保留原值
 *   - patch 里显式置空字符串则写入空串
 *
 * 失败抛错给 caller；main.ts 的 IPC handler 决定是否表面化到 UI。
 */
export async function writeUserSettingsYAML(patch: UserSettings): Promise<void> {
  const file = userSettingsPath();
  await fs.mkdir(path.dirname(file), { recursive: true });

  const existing = (await readUserSettingsYAML()) ?? {};

  const merged: UserSettings = {
    llm: mergeLLM(existing.llm, patch.llm),
    providers: mergeProviders(existing.providers, patch.providers),
    app: mergeApp(existing.app, patch.app),
    memory: mergeMemory(existing.memory, patch.memory),
    locale: patch.locale ?? existing.locale,
  };

  const body =
    `# darvin-cowork 用户级配置\n` +
    `# 由设置面板写入；不要手工改文件路径，会被覆盖。\n` +
    `llm:\n` +
    `  provider: ${yamlQuote(merged.llm?.provider ?? 'anthropic')}\n` +
    `  api_key: ${yamlQuote(merged.llm?.api_key ?? '')}\n` +
    `  base_url: ${yamlQuote(merged.llm?.base_url ?? '')}\n` +
    `  default_model: ${yamlQuote(merged.llm?.default_model ?? '')}\n` +
    `app:\n` +
    `  auto_launch: ${yamlBool(merged.app?.auto_launch ?? false)}\n` +
    `  notifications: ${yamlBool(merged.app?.notifications ?? true)}\n` +
    `  proxy: ${yamlQuote(merged.app?.proxy ?? '')}\n` +
    `memory:\n` +
    `  enabled: ${yamlBool(merged.memory?.enabled ?? false)}\n` +
    `  embedding_provider: ${yamlQuote(merged.memory?.embedding_provider ?? 'openai')}\n` +
    `  api_key: ${yamlQuote(merged.memory?.api_key ?? '')}\n` +
    (Object.keys(merged.providers ?? {}).length > 0
      ? `providers:\n` +
        Object.entries(merged.providers ?? {})
          .map(
            ([name, entry]) =>
              `  ${name}:\n` +
              `    api_key: ${yamlQuote(entry?.api_key ?? '')}\n` +
              `    base_url: ${yamlQuote(entry?.base_url ?? '')}\n` +
              `    default_model: ${yamlQuote(entry?.default_model ?? '')}`,
          )
          .join('\n') +
        '\n'
      : '') +
    `locale: ${merged.locale ?? 'zh'}\n`;

  await fs.writeFile(file, body, { encoding: 'utf8', mode: 0o600 });
}

function mergeLLM(
  existing: UserSettingsLLM | undefined,
  patch: UserSettingsLLM | undefined,
): UserSettingsLLM {
  if (!patch) return existing ?? {};
  return {
    provider: patch.provider ?? existing?.provider ?? 'anthropic',
    api_key: patch.api_key !== undefined ? patch.api_key : existing?.api_key,
    base_url: patch.base_url !== undefined ? patch.base_url : existing?.base_url,
    default_model: patch.default_model !== undefined ? patch.default_model : existing?.default_model,
  };
}

function mergeProviders(
  existing: Record<string, UserSettingsProviderEntry> | undefined,
  patch: Record<string, UserSettingsProviderEntry> | undefined,
): Record<string, UserSettingsProviderEntry> {
  if (!patch) return existing ?? {};
  const out = { ...(existing ?? {}) };
  for (const [name, entry] of Object.entries(patch)) {
    const prev = out[name] ?? {};
    out[name] = {
      api_key: entry.api_key !== undefined ? entry.api_key : prev.api_key,
      base_url: entry.base_url !== undefined ? entry.base_url : prev.base_url,
      default_model: entry.default_model !== undefined ? entry.default_model : prev.default_model,
    };
  }
  return out;
}

function mergeApp(existing: UserSettingsApp | undefined, patch: UserSettingsApp | undefined): UserSettingsApp {
  if (!patch) return existing ?? {};
  return {
    auto_launch: patch.auto_launch ?? existing?.auto_launch,
    notifications: patch.notifications ?? existing?.notifications,
    proxy: patch.proxy !== undefined ? patch.proxy : existing?.proxy,
  };
}

function mergeMemory(
  existing: UserSettingsMemory | undefined,
  patch: UserSettingsMemory | undefined,
): UserSettingsMemory {
  if (!patch) return existing ?? {};
  return {
    enabled: patch.enabled ?? existing?.enabled,
    embedding_provider: patch.embedding_provider ?? existing?.embedding_provider,
    api_key: patch.api_key !== undefined ? patch.api_key : existing?.api_key,
  };
}

function parseSimpleYaml(src: string): UserSettings {
  const root: UserSettings = {};
  let section = '';
  let subsection = '';
  for (const raw of src.split('\n')) {
    const stripped = raw.replace(/#.*$/, '');
    if (!stripped.trim()) continue;
    const indent = /^\s*/.exec(stripped)?.[0].length ?? 0;
    const line = stripped.trim();
    const m = line.match(/^([\w.-]+):\s*(.*)$/);
    if (!m) continue;
    const key = m[1];
    const rawVal = m[2] ?? '';
    const val = unquote(rawVal);

    if (indent === 0 && key === 'locale' && (val === 'zh' || val === 'en')) {
      root.locale = val;
      continue;
    }
    if (indent === 0 && !rawVal) {
      section = key;
      subsection = '';
      continue;
    }
    if (indent === 2 && !rawVal && section === 'providers') {
      subsection = key;
      continue;
    }

    if (section === 'llm' || section === 'app' || section === 'memory') {
      const target = section === 'llm'
        ? (root.llm = root.llm ?? {})
        : section === 'app'
          ? (root.app = root.app ?? {})
          : (root.memory = root.memory ?? {});
      if (indent === 2) {
        (target as Record<string, string | boolean | undefined>)[key] = coerce(val);
      }
      continue;
    }

    if (section === 'providers' && subsection) {
      root.providers = root.providers ?? {};
      const entry = (root.providers[subsection] = root.providers[subsection] ?? {});
      if (indent >= 4) {
        (entry as Record<string, string | undefined>)[key] = val;
      }
      continue;
    }
  }
  return root;
}

function unquote(rawVal: string): string {
  const v = rawVal.trim();
  if (v.startsWith('"') && v.endsWith('"')) return v.slice(1, -1);
  return v;
}

function coerce(v: string): string | boolean {
  if (v === 'true') return true;
  if (v === 'false') return false;
  return v;
}

function yamlQuote(s: string): string {
  // yaml 双引号字符串：反斜杠 / 双引号 / 换行做转义
  return `"${s.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n')}"`;
}

function yamlBool(b: boolean): string {
  return b ? 'true' : 'false';
}
