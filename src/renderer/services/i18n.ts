/**
 * i18n：平铺字符串字典，UI 默认 zh；新增 key 必须同时登记 dictZh / dictEn。
 */

import { ref } from 'vue';

type Lang = 'zh' | 'en';

const dictZh: Record<string, string> = {
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
  'expert.search.placeholder': '搜索专家 Agent…',
  'expert.use': '使用',
  'expert.details': '详情',
  'settings.title': '设置',
  'settings.nav.aria_label': '设置子导航',
  'settings.account.title': '账户',
  'settings.account.desc': '管理你的登录身份与基础信息（当前为本地演示数据）',
  'settings.account.username': '用户名',
  'settings.account.email': '邮箱',
  'settings.account.logout': '退出登录',
  'settings.account.logout_warn': '退出登录尚未接入，等待账号体系落地',
  'settings.appearance.title': '外观',
  'settings.appearance.desc': '选择界面主题，选择结果会保存在本地',
  'settings.appearance.language': '语言',
  'settings.appearance.language_desc': '切换界面显示语言，立即生效',
  'settings.shortcuts.title': '快捷键',
  'settings.shortcuts.desc': '当前为规划中的快捷键映射，尚未全部生效',
  'settings.about.title': '关于',
  'settings.about.desc': 'Darvin Cowork 的版本与运行时信息',
  'settings.about.version': '版本',
  'settings.about.runtime': '运行时',
  'settings.about.electron': 'Electron',
  'settings.about.arch_title': '架构',
  'settings.about.licenses_title': '开源许可',
  'settings.models.title': '模型',
  'settings.models.desc': '配置 LLM 提供方；保存后会自动重启 Go 子进程以应用新 Key。',
  'settings.models.provider': '提供方',
  'settings.models.api_key': 'API Key',
  'settings.models.api_key_placeholder': 'sk-ant-...',
  'settings.models.base_url': 'Base URL（可选）',
  'settings.models.base_url_placeholder': 'https://api.anthropic.com',
  'settings.models.save': '保存',
  'settings.models.saving': '保存中…',
  'settings.models.reset': '重置',
  'settings.models.saved_restarted': '已保存并重启 Go 子进程，新 Key 已生效',
  'settings.models.saved_no_restart': '已保存，但 Go 子进程重启失败，请检查二进制路径',
  'settings.models.save_failed': '保存失败',
  'settings.models.load_failed': '加载失败',
  'settings.models.path_hint': '配置文件路径：',
};

const dictEn: Record<string, string> = {
  'app.title': 'Darvin Cowork',
  'app.new_chat': 'New chat',
  'app.theme.toggle': 'Toggle theme',
  'app.settings': 'Settings',
  'app.runtime.ready': 'Runtime: ready',
  'app.runtime.offline': 'Runtime: offline',
  'app.runtime.no_binary': 'Runtime: no binary',
  'sidebar.sessions': 'Chats',
  'sidebar.brand': 'LobsterAI',
  'sidebar.nav.new_task': 'New task',
  'sidebar.nav.search': 'Search tasks',
  'sidebar.nav.scheduled': 'Scheduled tasks',
  'sidebar.nav.suite': 'Expert suite',
  'sidebar.nav.skill': 'Skills',
  'sidebar.nav.mcp': 'MCP',
  'sidebar.my_agent_label': 'My Agent',
  'sidebar.agent.main.name': 'Main Agent',
  'sidebar.agent.main.sub': 'All-scenario office assistant',
  'sidebar.recent_label': 'Recent tasks',
  'sidebar.login': 'Sign in',
  'sidebar.settings': 'Settings',
  'sidebar.placeholder.warn': "This feature isn't implemented yet",
  'chat.empty': 'Send a message to start the conversation.',
  'chat.empty.hint': 'Try typing "ping"',
  'chat.empty.title': 'Begin here',
  'chat.placeholder': 'Send a message...',
  'chat.placeholder.busy': 'Agent is busy...',
  'chat.send': 'Send',
  'chat.menu.toggle_sidebar': 'Collapse sidebar',
  'chat.menu.toggle_sidepanel': 'Collapse tool panel',
  'sidepanel.tabs.tools': 'Tools',
  'sidepanel.tabs.thinking': 'Thinking',
  'sidepanel.tabs.artifact': 'Artifact',
  'sidepanel.empty.tools': 'No tool calls yet. Tool traces will appear here when the agent runs.',
  'sidepanel.empty.thinking': 'No thinking trace yet.',
  'sidepanel.empty.artifact': 'No artifacts yet.',
  'model.sonnet': 'claude-sonnet-4-5',
  'model.opus': 'claude-opus-4-5',
  'model.gpt4o': 'gpt-4o',
  'home.greeting.morning': 'Good morning',
  'home.greeting.noon': 'Good afternoon',
  'home.greeting.afternoon': 'Good afternoon',
  'home.greeting.evening': 'Good evening',
  'home.subtitle': 'What can I help you with today?',
  'home.disclaimer': 'AI-generated content is for reference only. Please verify important information.',
  'model.label': 'Current model',
  'home.prompt.placeholder': 'Send a message to Darvin…',
  'quick.slide.title': 'Create slides',
  'quick.slide.desc': 'Generate presentations in one click',
  'quick.data.title': 'Analyze data',
  'quick.data.desc': 'Upload files for smart analysis',
  'quick.doc.title': 'Write documents',
  'quick.doc.desc': 'Draft / polish / translate',
  'quick.web.title': 'Search the web',
  'quick.web.desc': 'Real-time web search',
  'plus.upload.title': 'Upload files',
  'plus.upload.desc': 'Upload and analyze local files',
  'plus.goal.title': 'Set goals',
  'plus.goal.desc': 'Break tasks into actionable steps',
  'plus.todo.title': 'To-do list',
  'plus.todo.desc': 'Manage your task list',
  'plus.settings.title': 'Preferences',
  'plus.settings.desc': 'Theme / shortcuts / notifications',
  'model.search.placeholder': 'Search models…',
  'model.menu.title': 'Switch model',
  'expert.search.placeholder': 'Search expert agents…',
  'expert.use': 'Use',
  'expert.details': 'Details',
  'settings.title': 'Settings',
  'settings.nav.aria_label': 'Settings sub-navigation',
  'settings.account.title': 'Account',
  'settings.account.desc': 'Manage your sign-in identity and basic info (currently using local demo data)',
  'settings.account.username': 'Username',
  'settings.account.email': 'Email',
  'settings.account.logout': 'Sign out',
  'settings.account.logout_warn': 'Sign-out is not wired up yet; awaiting the account system',
  'settings.appearance.title': 'Appearance',
  'settings.appearance.desc': 'Choose an interface theme; your choice is saved locally',
  'settings.appearance.language': 'Language',
  'settings.appearance.language_desc': 'Switch the interface language; takes effect immediately',
  'settings.shortcuts.title': 'Shortcuts',
  'settings.shortcuts.desc': 'Planned keyboard shortcut mappings; not all are active yet',
  'settings.about.title': 'About',
  'settings.about.desc': 'Darvin Cowork version and runtime info',
  'settings.about.version': 'Version',
  'settings.about.runtime': 'Runtime',
  'settings.about.electron': 'Electron',
  'settings.about.arch_title': 'Architecture',
  'settings.about.licenses_title': 'Open-source licenses',
  'settings.models.title': 'Models',
  'settings.models.desc': 'Configure LLM providers; the Go subprocess restarts automatically after saving to apply the new key.',
  'settings.models.provider': 'Provider',
  'settings.models.api_key': 'API Key',
  'settings.models.api_key_placeholder': 'sk-ant-...',
  'settings.models.base_url': 'Base URL (optional)',
  'settings.models.base_url_placeholder': 'https://api.anthropic.com',
  'settings.models.save': 'Save',
  'settings.models.saving': 'Saving…',
  'settings.models.reset': 'Reset',
  'settings.models.saved_restarted': 'Saved and the Go subprocess restarted; the new key is now active',
  'settings.models.saved_no_restart': 'Saved, but the Go subprocess failed to restart; please check the binary path',
  'settings.models.save_failed': 'Save failed',
  'settings.models.load_failed': 'Load failed',
  'settings.models.path_hint': 'Config file path:',
};

// dev-only: zh/en 字典 key 集合必须严格一致
function assertSameKeys(zh: Record<string, string>, en: Record<string, string>): void {
  const zhKeys = Object.keys(zh).sort();
  const enKeys = Object.keys(en).sort();
  if (zhKeys.length !== enKeys.length || zhKeys.some((k, i) => k !== enKeys[i])) {
    const missingInEn = zhKeys.filter((k) => !(k in en));
    const extraInEn = enKeys.filter((k) => !(k in zh));
    throw new Error(
      `[i18n] key drift between zh and en: missing-in-en=${JSON.stringify(missingInEn)} extra-in-en=${JSON.stringify(extraInEn)}`,
    );
  }
}

if (process.env.NODE_ENV !== 'production') {
  assertSameKeys(dictZh, dictEn);
}

// currentLang 是 Vue ref：组件里 `{{ t('foo') }}` 在 render 期间读到
// ref.value，Vue 自动建立依赖，setLang 后整棵树 re-render，不需要 reload。
const currentLang = ref<Lang>('zh');

/**
 * 切换当前语言；调用后所有 `{{ t('xxx') }}` 自动 re-render。
 */
export function setLang(lang: Lang): void {
  currentLang.value = lang;
}

/**
 * 读取当前语言。
 */
export function getLang(): Lang {
  return currentLang.value;
}

/**
 * 按当前语言查 key；未命中 dev 期 warn + 返回 key，prod 期静默返回 key。
 * 在 render 上下文中调用时，自动追踪 currentLang 变化。
 */
export function t(key: string): string {
  const table = currentLang.value === 'en' ? dictEn : dictZh;
  const value = table[key];
  if (value === undefined) {
    if (process.env.NODE_ENV !== 'production') {
      console.warn(`[i18n] missing ${currentLang.value} translation for key: ${key}`);
    }
    return key;
  }
  return value;
}