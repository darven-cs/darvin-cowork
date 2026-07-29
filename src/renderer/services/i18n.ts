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
  'home.greeting.morning': '早上好',
  'home.greeting.noon': '中午好',
  'home.greeting.afternoon': '下午好',
  'home.greeting.evening': '晚上好',
  'home.subtitle': '今天有什么我可以帮你的？',
  'home.disclaimer': 'AI 生成内容仅供参考，请核实重要信息',
  'model.label': '当前模型',
  'home.prompt.placeholder': '给 Darvin 发送消息…',
  'quick.slide.title': '做 PPT',
  'quick.slide.desc': '一键生成演示文稿',
  'quick.data.title': '看数据',
  'quick.data.desc': '上传文件，智能分析',
  'quick.doc.title': '写文档',
  'quick.doc.desc': '起草 / 润色 / 翻译',
  'quick.web.title': '搜网页',
  'quick.web.desc': '实时联网检索',
  'plus.upload.title': '上传文件',
  'plus.upload.desc': '本地文件上传分析',
  'plus.goal.title': '目标设定',
  'plus.goal.desc': '把任务拆成可执行步骤',
  'plus.todo.title': '待办清单',
  'plus.todo.desc': '管理你的任务列表',
  'plus.settings.title': '偏好设置',
  'plus.settings.desc': '主题 / 快捷键 / 通知',
  'model.search.placeholder': '搜索模型…',
  'model.menu.title': '切换模型',
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
