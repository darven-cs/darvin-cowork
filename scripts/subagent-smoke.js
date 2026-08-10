/**
 * Subagent RPC headless smoke: spawn darvin-agent, connect via WS,
 * assert the 4 agent.subagent.* methods are registered and degrade
 * gracefully on empty / unknown inputs (no LLM key required).
 */

'use strict';

const { spawn } = require('child_process');
const path = require('path');
const os = require('os');
const fs = require('fs');

const REPO = path.resolve(__dirname, '..');
const BIN = path.join(REPO, 'bin', `darvin-agent-${os.platform()}-${os.arch()}`);

function waitForPort(child, timeoutMs) {
  return new Promise((resolve, reject) => {
    let out = '';
    const timer = setTimeout(() => reject(new Error(`port line timeout; saw:\n${out}`)), timeoutMs);
    const onData = (chunk) => {
      out += chunk.toString();
      const m = out.match(/<port>(\d+)<\/port>/);
      if (m) {
        clearTimeout(timer);
        resolve(Number(m[1]));
      }
    };
    child.stdout.on('data', onData);
    child.stderr.on('data', (c) => { out += c.toString(); });
  });
}

function rpc(ws, method, params = {}) {
  return new Promise((resolve) => {
    const id = String(Math.random());
    const onMsg = (ev) => {
      const msg = JSON.parse(ev.data);
      if (String(msg.id) === id) {
        ws.removeEventListener('message', onMsg);
        resolve(msg);
      }
    };
    ws.addEventListener('message', onMsg);
    ws.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }));
  });
}

async function main() {
  const child = spawn(BIN, [], {
    env: { ...process.env, DARVIN_CONFIG: path.join(REPO, 'src/darvin-agent/config.yaml') },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let port;
  try {
    port = await waitForPort(child, 15000);
  } catch (e) {
    child.kill('SIGKILL');
    throw e;
  }
  console.log(`[smoke] port ${port}`);

  const ws = new WebSocket(`ws://localhost:${port}/ws`);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve);
    ws.addEventListener('error', () => reject(new Error('ws connect failed')));
  });

  const checks = [
    ['agent.subagent.list', { sessionId: 'smoke-none' }, (m) => {
      if (m.error) throw new Error(`list error: ${JSON.stringify(m.error)}`);
      if (!Array.isArray(m.result?.subagents)) throw new Error('list result.subagents not array');
      return 'ok';
    }],
    ['agent.subagent.get_messages', { runId: 'smoke-none/sub/x' }, (m) => {
      if (m.error) throw new Error(`get_messages error: ${JSON.stringify(m.error)}`);
      if (!Array.isArray(m.result?.messages)) throw new Error('get_messages result.messages not array');
      return 'ok';
    }],
    ['agent.subagent.abort', { runId: 'smoke-none/sub/x' }, (m) => {
      if (m.error) throw new Error(`abort error: ${JSON.stringify(m.error)}`);
      if (m.result?.ok !== true) throw new Error('abort result.ok not true');
      return 'ok';
    }],
    ['agent.subagent.read_result', { runId: 'smoke-none/sub/x', offset_bytes: 0, limit_bytes: 1024 }, (m) => {
      // Unknown run must route through the handler (not method-not-found);
      // a "run not found" RPC error is the correct degradation.
      if (m.error && m.error.code !== -32603) throw new Error(`read_result unexpected error: ${JSON.stringify(m.error)}`);
      if (!m.error && typeof m.result?.text !== 'string') throw new Error('read_result result.text not string');
      return 'ok';
    }],
    ['agent.tools.list', {}, (m) => {
      if (m.error) throw new Error(`tools.list error: ${JSON.stringify(m.error)}`);
      const names = (m.result?.tools ?? []).map((t) => t.name);
      for (const want of ['delegate_subagent', 'list_subagents', 'abort_subagent', 'parallel_subagents', 'read_subagent_result']) {
        if (!names.includes(want)) throw new Error(`tools.list missing ${want}`);
      }
      return 'ok';
    }],
  ];

  let pass = 0;
  for (const [method, params, assert] of checks) {
    const res = await rpc(ws, method, params);
    assert(res);
    console.log(`[smoke] ${method} -> ok`);
    pass++;
  }

  ws.close();
  child.kill('SIGTERM');
  console.log(`[smoke] ${pass}/${checks.length} subagent RPC checks passed`);
}

main().catch((e) => {
  console.error('[smoke] FAIL:', e.message);
  process.exit(1);
});
