/**
 * Main 端 IM 通道转发层 —— 纯 RPC 透传到 Go 侧 agent.im.*。
 *
 * IM 通道实例（凭据 / 访问策略 / 状态）全部归 darvin-agent gateway；
 * main 端不持有任何 IM 状态。每个方法对应一个 `agent.im.<op>`
 * JSON-RPC 调用。
 */

import type { AgentClient } from '../runtime/client';
import type {
  DarvinIMInstance,
  DarvinIMLoginResult,
} from '../../shared/darvin-api';

export interface IMProxy {
  list(req: { workspaceId?: string }): Promise<{ instances: DarvinIMInstance[] }>;
  get(req: { instanceId: string }): Promise<{ instance: DarvinIMInstance }>;
  create(req: {
    workspaceId?: string;
    channel: string;
    name: string;
    enabled?: boolean;
    config: Record<string, unknown>;
    accessMode?: string;
    allowFrom?: string[];
  }): Promise<{ instance: DarvinIMInstance }>;
  update(req: { instanceId: string; patch: Record<string, unknown> }): Promise<{ instance: DarvinIMInstance }>;
  delete(req: { instanceId: string }): Promise<{ deleted: boolean }>;
  setEnabled(req: { instanceId: string; enabled: boolean }): Promise<{ instance: DarvinIMInstance }>;
  test(req: { channel: string; config: Record<string, unknown> }): Promise<{ ok: boolean; error?: string }>;
  loginStart(req: { workspaceId?: string; channel: string; instanceId: string }): Promise<DarvinIMLoginResult>;
  loginPoll(req: { workspaceId?: string; sessionId: string }): Promise<DarvinIMLoginResult>;
}

export function createIMProxy(client: AgentClient): IMProxy {
  return {
    list: (req) => client.request('agent.im.list', req),
    get: (req) => client.request('agent.im.get', req),
    create: (req) => client.request('agent.im.create', req),
    update: (req) => client.request('agent.im.update', req),
    delete: (req) => client.request('agent.im.delete', req),
    setEnabled: (req) => client.request('agent.im.set_enabled', req),
    test: (req) => client.request('agent.im.test', req),
    loginStart: (req) => client.request('agent.im.login_start', req),
    loginPoll: (req) => client.request('agent.im.login_poll', req),
  };
}
