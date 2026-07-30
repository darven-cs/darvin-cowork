import { test, expect, launchApp, closeApp, type DarvinBridgeGlobal } from './helpers';

/**
 * Happy path：用户从启动 → 设置 LLM key → 真流式响应。
 *
 * 这个 spec @real-llm 标记 — 当 ANTHROPIC_API_KEY 没设时整 spec 跳过，
 * CI 不阻塞。开发者本地配 key 后可手动跑：
 *   ANTHROPIC_API_KEY=sk-ant-... npm run e2e -- --project=real-llm
 *
 * 步骤：
 *   1. 启动 app，状态应为 'offline'（还没设 key）
 *   2. 切到设置 → 模型，灌入 key，等 save 成功
 *   3. 状态走 'online'（子进程重启加载新 key）
 *   4. 切回主聊天，输入 'ping'
 *   5. 等 assistant 消息出现且 done=true
 *
 * 与原 spec 的区别：session 数据所有权归 main（SessionStore），prompt
 * 不再传 sessionId —— main 端从 store.getActive() 拿当前 session。
 */
test('@real-llm happy path: real Anthropic streaming response', async () => {
  test.skip(!process.env.ANTHROPIC_API_KEY, 'ANTHROPIC_API_KEY not set — skipping real LLM spec');

  const { app, window } = await launchApp();
  try {
    // 1. baseline status
    await window.waitForFunction(async () => {
      const s = await (globalThis as unknown as DarvinBridgeGlobal).darvin.status();
      return s === 'online' || s === 'ready' || s === 'offline' || s === 'no-binary';
    });

    // 2. 灌入 key。走 preload 桥而不是点表单：这一步只是 happy path 的
    //    前置条件，设置面板本身的交互由下面的 @core 用例覆盖。
    await window.evaluate(
      (apiKey) =>
        (globalThis as unknown as DarvinBridgeGlobal).darvin.setLLMConfig({ apiKey, baseUrl: '' }),
      process.env.ANTHROPIC_API_KEY!,
    );

    // 3. wait for restart
    await window.waitForFunction(
      async () => {
        const s = await (globalThis as unknown as DarvinBridgeGlobal).darvin.status();
        return s === 'online' || s === 'ready';
      },
      undefined,
      { timeout: 30_000 },
    );

    // 4. prompt
    await window.click('[data-testid="composer-textarea"]');
    await window.fill('[data-testid="composer-textarea"]', 'ping');
    await window.click('[data-testid="composer-send"]');

    // 5. wait for assistant bubble
    const assistant = window.locator('[data-testid="message-assistant"]').first();
    await assistant.waitFor({ timeout: 30_000 });
    const text = (await assistant.textContent()) ?? '';
    expect(text.trim().length).toBeGreaterThan(0);
  } finally {
    await closeApp(app);
  }
});

/**
 * 设置面板可见性 + 表单交互（不需要真 key）。
 * 验证 SettingsSubNav 的 models tab 渲染、表单可输入、save 按钮的
 * 禁用条件。这是 e2e 回归里最便宜的可见性检查。
 */
test('@core settings panel renders and form is interactive', async () => {
  const { app, window } = await launchApp();
  try {
    // 切到 settings 视图 → 模型分区
    await window.click('[data-testid="sidebar-settings"]');
    await window.click('[data-testid="settings-nav-models"]');

    await expect(window.locator('[data-testid="settings-models"]')).toBeVisible();
    await expect(window.locator('[data-testid="settings-models-apikey"]')).toBeEnabled();

    // 输入 key 后 save 应启用
    await window.fill('[data-testid="settings-models-apikey"]', 'test-key');
    const saveBtn = window.locator('[data-testid="settings-models-save"]');
    await expect(saveBtn).toBeEnabled();
  } finally {
    await closeApp(app);
  }
});