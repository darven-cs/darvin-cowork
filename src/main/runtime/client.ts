/**
 * 与 darvin-agent 子进程的 IPC 客户端（占位）。
 *
 * 落地后需实现：
 * - JSON-RPC / protobuf / 自定义协议（选其一）
 * - 连接生命周期（connect / reconnect / disconnect）
 * - preload 暴露给 renderer 的 API
 */

export interface AgentClient {
  connect(): Promise<void>;
  disconnect(): Promise<void>;
  // TODO: request<T>(method, params): Promise<T>
  // TODO: send<T>(event, payload): void
  // TODO: on<T>(event, handler): Unsubscribe
}

export function createAgentClient(/* bin: string */): AgentClient {
  throw new Error('createAgentClient 未实现，待 darvin-agent 协议确定后补全');
}
