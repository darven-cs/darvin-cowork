/**
 * Main 端 schedule 转发层 —— 纯 RPC 透传到 Go 侧 agent.schedule.*。
 *
 * schedule 表 / cron 引擎 / 触发链路全部在 darvin-agent gateway 内；
 * main 端不持有任何 schedule 状态。每个方法对应一个
 * `agent.schedule.<op>` JSON-RPC 调用。
 */

import type { AgentClient } from '../runtime/client';
import type {
  DarvinSchedule,
  DarvinScheduleInput,
  DarvinScheduleRun,
} from '../../shared/darvin-api';

export interface ScheduleProxy {
  list(req: { workspaceId: string }): Promise<{ schedules: DarvinSchedule[] }>;
  get(req: { workspaceId: string; scheduleId: string }): Promise<{ schedule: DarvinSchedule }>;
  create(req: { workspaceId: string; schedule: DarvinScheduleInput }): Promise<{ schedule: DarvinSchedule }>;
  update(req: {
    workspaceId: string;
    scheduleId: string;
    patch: Partial<DarvinScheduleInput>;
  }): Promise<{ schedule: DarvinSchedule }>;
  delete(req: { workspaceId: string; scheduleId: string }): Promise<{ deleted: boolean }>;
  toggle(req: { workspaceId: string; scheduleId: string; enabled: boolean }): Promise<{ schedule: DarvinSchedule }>;
  runNow(req: { workspaceId: string; scheduleId: string }): Promise<{ run: DarvinScheduleRun }>;
  abort(req: { workspaceId: string; scheduleId: string; runId: string }): Promise<{ aborted: boolean }>;
  listRuns(req: {
    workspaceId: string;
    scheduleId: string;
    limit?: number;
    offset?: number;
  }): Promise<{ runs: DarvinScheduleRun[] }>;
  listAllRuns(req: {
    workspaceId: string;
    limit?: number;
    offset?: number;
  }): Promise<{ runs: DarvinScheduleRun[] }>;
}

export function createScheduleProxy(client: AgentClient): ScheduleProxy {
  const send = (op: string) => <T>(payload: unknown): Promise<T> =>
    client.request<T>(`agent.schedule.${op}`, payload);

  return {
    list: send('list'),
    get: send('get'),
    create: send('create'),
    update: send('update'),
    delete: send('delete'),
    toggle: send('toggle'),
    runNow: send('run_now'),
    abort: send('abort'),
    listRuns: send('list_runs'),
    listAllRuns: send('list_all_runs'),
  };
}