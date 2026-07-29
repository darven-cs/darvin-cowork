/**
 * 与 darvin-agent 子进程的 IPC 客户端。
 *
 * 待实现：
 * - 协议：JSON-RPC / protobuf / 自定义（选其一）
 * - 连接生命周期：connect / reconnect / disconnect
 * - request / send / on 三类方法
 * - 把方法桥接到 preload 的 `window.darvin`
 */

export interface AgentClient {
  connect(): Promise<void>;
  disconnect(): Promise<void>;
  // TODO: request<T>(method, params): Promise<T>
  // TODO: send<T>(event, payload): void
  // TODO: on<T>(event, handler): Unsubscribe
}

export function createAgentClient(/* bin: string */): AgentClient {
  throw new Error('createAgentClient 未实现');
}
