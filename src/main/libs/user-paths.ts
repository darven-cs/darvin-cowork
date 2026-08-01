/**
 * 用户级数据目录。
 *
 * 布局：
 *   <userDataDir>/                        ← app.getPath('userData')，
 *   │                                       由 app.setName('darvin-cowork')
 *   │                                       决定，三平台下分别是：
 *   │                                       Linux:   ~/.config/darvin-cowork/
 *   │                                       macOS:   ~/Library/Application Support/darvin-cowork/
 *   │                                       Windows: %APPDATA%\darvin-cowork\
 *   ├── Chromium cache...                 ← Electron 自身的运行时缓存
 *   │   (Cache/ Cookies/ GPUCache/ ...)
 *   └── darvin-agent/                     ← agent 拥有的业务数据
 *       ├── config.yaml                   ← Electron 写入、Go 读合并
 *       └── sessions.db                   ← Go agent sessions.db（统一数据源）
 *
 * 与 Go 侧 config.UserDataDir() 落在同一绝对路径下；config.UserConfigPath()
 * 与 agentSessionsDsnPath() 也对齐。旧的 darvin-cowork.sqlite（Electron
 * SessionStore）已随 merge-databases refactor 删除，dev 阶段由开发者人工
 * rm 清掉（spec §4.7）。
 */
import { app } from 'electron';
import path from 'node:path';

export function userDataDir(): string {
  return app.getPath('userData');
}

export function agentDataDir(): string {
  return path.join(userDataDir(), 'darvin-agent');
}

export function userSettingsPath(): string {
  return path.join(agentDataDir(), 'config.yaml');
}

export function agentSessionsDsnPath(): string {
  return path.join(agentDataDir(), 'sessions.db');
}
