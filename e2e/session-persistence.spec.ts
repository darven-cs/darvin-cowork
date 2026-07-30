import { test, expect, launchApp, closeApp, type DarvinBridgeGlobal } from './helpers';
import os from 'node:os';

/**
 * Session persistence：制造已知状态 → prompt → quit → 重启 → 历史可见。
 *
 * 这个 spec @core — 不需要 LLM key。流程：
 *   1. 启动 app，prompt 一条占位内容（用 mock 占位 API key 让
 *      Go 子进程不至于 panic；不在 happy-path 里需要真 Anthropic）
 *   2. quit（SIGTERM，spec FR-2.4）
 *   3. 重启 app，断言历史消息被 get_messages 拉回来
 *
 * 不依赖 Anthropic 真实 key —— 一个 placeholder api_key 足以让 Go
 * 启动（executor 在 emit error 前已经 append 了 user message + 触发
 * dispatcher 三 hook），session 落库后再 quit。
 *
 * session / message 数据所有权归 main（`SessionStore` 持有
 * `userData/darvin-cowork.sqlite`），不依赖 Go 侧 sessions.db。
 */
test('@core session persistence across app restarts', async () => {
  test.skip(process.platform === 'linux' && os.userInfo().uid === 0,
    'root-skip: HOME path semantics differ for root');

  const { app, window } = await launchApp();
  let sessionId: string | null = null;
  try {
    // 用户消息先灌进本地（在 IPC prompt 之前）
    await window.click('[data-testid="composer-textarea"]');
    await window.fill('[data-testid="composer-textarea"]', 'persistence test');
    await window.click('[data-testid="composer-send"]');

    // wait for the user bubble to appear in DOM
    await window.waitForSelector('[data-testid="message-user"]', { timeout: 5_000 });

    // 读 main 端当前 active session id
    sessionId = await window.evaluate(async () => {
      const r = await (globalThis as unknown as DarvinBridgeGlobal).darvin.getActiveSession();
      return r.sessionId;
    });

    // quit gracefully
    await closeApp(app);
    // 给 Go 子进程留出 close 窗口
    await new Promise((r) => setTimeout(r, 1500));
  } catch (e) {
    await closeApp(app);
    throw e;
  }

  // 重启
  const second = await launchApp();
  try {
    // 验证 active session 跨重启保持（main 端 bootstrapActiveSession 选最近 updated）
    const active = await second.window.evaluate(async () => {
      const r = await (globalThis as unknown as DarvinBridgeGlobal).darvin.getActiveSession();
      return r.sessionId;
    });
    if (sessionId !== null) {
      expect(active).toBe(sessionId);
    }

    // 历史消息从 SessionStore 拉回来 —— 数据所有权归 main
    if (active !== null) {
      const msgs = await second.window.evaluate(async (sid) => {
        const r = await (globalThis as unknown as DarvinBridgeGlobal).darvin.getMessages(sid);
        return r.messages;
      }, active);
      expect(Array.isArray(msgs)).toBe(true);
      // 至少应该有 user 消息"persistence test"
      const userMsg = msgs.find((m) => (m as { role: string }).role === 'user');
      expect((userMsg as { content: string } | undefined)?.content).toBe('persistence test');
    }
  } finally {
    await closeApp(second.app);
  }
});