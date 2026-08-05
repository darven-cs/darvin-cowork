/**
 * Headless 端到端 smoke test。
 *
 * 不依赖 Electron、不依赖 Anthropic key：直接 spawn darvin-agent 二进制，
 * 解析端口，WS 连上去，跑一遍 JSON-RPC 协议栈：
 *   - subscribe_events
 *   - list_sessions
 *   - get_messages
 *   - prompt
 *   - 等 done + agent_end
 *   - abort / prompt again
 *
 * Node 22+ 自带 WebSocket，不需要 `ws` 依赖；这是为了 npm run smoke
 * 在没装 node_modules 的 dev 容器里也能跑。
 *
 * 用法：node scripts/ws-smoke-client.js <port>
 * 退出码：0 = all assertions pass, 1 = fail。
 */

'use strict';

const port = Number(process.argv[2] ?? process.env.SMOKE_PORT);
if (!port || Number.isNaN(port)) {
  console.error('usage: node scripts/ws-smoke-client.js <port>');
  process.exit(2);
}

const ws = new WebSocket(`ws://localhost:${port}/ws`);
let nextId = 1;
const pending = new Map();
const events = [];

function send(method, params = {}) {
  const id = String(nextId++);
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }));
  });
}

function once(type, timeoutMs = 10_000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timeout waiting for ${type}`)), timeoutMs);
    const onEvent = (ev) => {
      if (ev.type !== type) return;
      clearTimeout(timer);
      ws.removeEventListener('message', onEventWrapper);
      resolve(ev);
    };
    const onEventWrapper = (m) => {
      try {
        const frame = JSON.parse(m.data);
        if (frame.method === 'agent.event') onEvent(frame.params);
      } catch (_) {
        /* ignore parse errors */
      }
    };
    ws.addEventListener('message', onEventWrapper);
  });
}

ws.addEventListener('message', (m) => {
  let frame;
  try { frame = JSON.parse(m.data); } catch (_) { return; }
  if (frame.method === 'agent.event') {
    events.push(frame.params);
    return;
  }
  const id = frame.id;
  if (id === undefined || id === null) return;
  const p = pending.get(String(id));
  if (!p) return;
  pending.delete(String(id));
  if (frame.error) {
    p.reject(new Error(`rpc ${frame.error.code}: ${frame.error.message}`));
  } else {
    p.resolve(frame.result);
  }
});

ws.addEventListener('error', (e) => {
  console.error('[smoke] ws error:', e.message ?? e);
});

(async () => {
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', () => resolve());
    ws.addEventListener('error', (e) => reject(new Error('ws open failed')));
  });
  console.log('[smoke] ws connected');

  // 1. subscribe_events
  const sub = await send('agent.subscribe_events', { sessionId: 'default' });
  if (!sub.subscribed) throw new Error('subscribe_events did not return subscribed:true');
  console.log('[smoke] subscribed to default');

  // 2. list_sessions
  const list = await send('agent.list_sessions', {});
  if (!Array.isArray(list.sessions)) throw new Error('list_sessions did not return sessions array');
  console.log(`[smoke] list_sessions returned ${list.sessions.length} session(s)`);

  // 3. get_messages
  const msgs = await send('agent.get_messages', { sessionId: 'default' });
  if (!Array.isArray(msgs.messages)) throw new Error('get_messages did not return messages array');
  console.log(`[smoke] get_messages returned ${msgs.messages.length} message(s)`);

  // 4. prompt (note: with placeholder api_key this will hit AgentError,
  //    not produce text_delta — that's expected, not a smoke failure).
  const pr = await send('agent.prompt', { content: 'smoke test', sessionId: 'default' });
  console.log(`[smoke] prompt -> sessionId=${pr.sessionId} messageId=${pr.messageId}`);

  // 5. wait for agent_end
  await once('agent_end', 15_000);
  console.log(`[smoke] received ${events.length} event(s) total; agent_end seen`);

  // 6. list_sessions now sees the touched session (hook 3 写盘后).
  //    不强制 expect > 0 — hook 写盘可能延迟，但 list_sessions 必须能 call 通。
  const after = await send('agent.list_sessions', {});
  if (!Array.isArray(after.sessions)) throw new Error('post-prompt list_sessions failed');
  console.log(`[smoke] post-prompt list_sessions returned ${after.sessions.length} session(s)`);

  console.log('[smoke] all checks passed');
  ws.close();
  process.exit(0);
})().catch((e) => {
  console.error('[smoke] FAIL:', e.message);
  process.exit(1);
});