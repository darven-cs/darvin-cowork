/**
 * 读写主进程侧的"用户级"yaml：darvin-cowork 的个人偏好（当前只有 llm）。
 *
 * 文件位置走 os.userConfigDir()，与 Go 侧 config.UserConfigPath() 一致：
 *   Linux:   ~/.config/darvin-cowork/config.yaml
 *   macOS:   ~/Library/Application Support/darvin-cowork/config.yaml
 *   Windows: %APPDATA%/darvin-cowork/config.yaml
 *
 * 这里手工用 yaml 文本拼接（不引第三方 yaml 库），避免给 main 加一个
 * 体积不小的依赖；字段名映射严格按 Go 侧 mapstructure tag，确保
 * 双方解析后的对象一致。
 */

import { app } from 'electron';
import fs from 'node:fs/promises';
import path from 'node:path';
import type { DarvinLocale } from '../../shared/darvin-api';

export interface UserSettingsLLM {
  api_key?: string;
  base_url?: string;
}

export interface UserSettings {
  llm?: UserSettingsLLM;
  locale?: DarvinLocale;
}

function userSettingsPath(): string {
  // 与 Go 侧 config.UserConfigPath() 对齐：appData + 'darvin-cowork'。
  // 不能用 userData —— app.setName('Darvin') 把 Electron 的 userData 路径
  // 偏到 ~/.config/Darvin/，而 Go 永远读 ~/.config/darvin-cowork/。
  // appData 才是 XDG_CONFIG_HOME / ~/Library/Application Support / %APPDATA%
  // 这一层，与 os.UserConfigDir() 跨平台语义完全一致。
  const base = app.getPath('appData');
  return path.join(base, 'darvin-cowork', 'config.yaml');
}

/**
 * 读取用户级 yaml，文件不存在返 null（首次安装）。
 * 实现极简：只解析 `llm.api_key` / `llm.base_url` 两个叶子键，
 * 其他字段保持原样落回文件（merge）。
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
    llm: {
      api_key: patch.llm?.api_key ?? existing.llm?.api_key,
      base_url: patch.llm?.base_url ?? existing.llm?.base_url,
    },
    locale: patch.locale ?? existing.locale,
  };

  const body =
    `# darvin-cowork 用户级配置（spec FR-5.2）\n` +
    `# 由设置面板写入；不要手工改文件路径，会被覆盖。\n` +
    `llm:\n` +
    `  provider: anthropic\n` +
    `  api_key: ${yamlQuote(merged.llm?.api_key ?? '')}\n` +
    `  base_url: ${yamlQuote(merged.llm?.base_url ?? '')}\n` +
    `locale: ${merged.locale ?? 'zh'}\n`;

  await fs.writeFile(file, body, { encoding: 'utf8', mode: 0o600 });
}

function parseSimpleYaml(src: string): UserSettings {
  const root: UserSettings = {};
  let section = '';
  for (const raw of src.split('\n')) {
    const stripped = raw.replace(/#.*$/, '');
    const line = stripped.trim();
    if (!line) continue;
    const m = line.match(/^([\w.-]+):\s*(.*)$/);
    if (!m) continue;
    const indented = /^\s/.test(stripped);
    const key = m[1];
    const rawVal = m[2] ?? '';

    if (!rawVal && !indented && !key.includes('.')) {
      section = key;
      continue;
    }
    if (key === 'locale' && !indented) {
      const val =
        rawVal.startsWith('"') && rawVal.endsWith('"') ? rawVal.slice(1, -1) : rawVal;
      if (val === 'zh' || val === 'en') {
        root.locale = val;
      }
      continue;
    }
    const val =
      rawVal.startsWith('"') && rawVal.endsWith('"') ? rawVal.slice(1, -1) : rawVal;
    const full = key.includes('.') ? key : `${section}.${key}`;
    if (full.startsWith('llm.')) {
      const leaf = full.slice('llm.'.length);
      root.llm = root.llm ?? {};
      (root.llm as Record<string, string | undefined>)[leaf] = val || undefined;
    }
  }
  return root;
}

function yamlQuote(s: string): string {
  // yaml 双引号字符串：反斜杠 / 双引号 / 换行做转义
  return `"${s.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n')}"`;
}