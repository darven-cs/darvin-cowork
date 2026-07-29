/**
 * i18n：单 key 字典，UI 主体中文、dev 字符串可保留英文。
 */

type Lang = 'zh' | 'en';

const dict: Record<string, string> = {
  'app.title': 'Darvin Cowork',
  'app.new_chat': '新建会话',
  'app.theme.toggle': '切换主题',
  'app.settings': '设置',
  'app.runtime.ready': 'Runtime: ready',
  'app.runtime.offline': 'Runtime: offline',
  'app.runtime.no_binary': 'Runtime: no binary',
  'sidebar.sessions': '会话',
  'sidebar.brand': 'LobsterAI',
  'sidebar.nav.new_task': '新建任务',
  'sidebar.nav.search': '搜索任务',
  'sidebar.nav.scheduled': '定时任务',
  'sidebar.nav.suite': '专家套件',
  'sidebar.nav.skill': '技能',
  'sidebar.nav.mcp': 'MCP',
  'sidebar.my_agent_label': '我的 Agent',
  'sidebar.agent.main.name': '主 Agent',
  'sidebar.agent.main.sub': '全场景办公助手',
  'sidebar.recent_label': '近期任务',
  'sidebar.login': '登录',
  'sidebar.settings': '设置',
  'sidebar.placeholder.warn': '此功能尚未实现',
  'chat.empty': 'Send a message to start the conversation.',
  'chat.empty.hint': '试试输入 "ping"',
  'chat.empty.title': 'Begin here',
  'chat.placeholder': 'Send a message...',
  'chat.placeholder.busy': 'Agent is busy...',
  'chat.send': '发送',
  'chat.menu.toggle_sidebar': '折叠侧栏',
  'chat.menu.toggle_sidepanel': '折叠工具面板',
  'sidepanel.tabs.tools': 'Tools',
  'sidepanel.tabs.thinking': 'Thinking',
  'sidepanel.tabs.artifact': 'Artifact',
  'sidepanel.empty.tools': 'No tool calls yet. Tool traces will appear here when the agent runs.',
  'sidepanel.empty.thinking': 'No thinking trace yet.',
  'sidepanel.empty.artifact': 'No artifacts yet.',
  'model.sonnet': 'claude-sonnet-4-5',
  'model.opus': 'claude-opus-4-5',
  'model.gpt4o': 'gpt-4o',
};

let currentLang: Lang = 'zh';

export function setLang(lang: Lang): void {
  currentLang = lang;
}

export function t(key: string): string {
  return dict[key] ?? key;
}

export function getLang(): Lang {
  return currentLang;
}
