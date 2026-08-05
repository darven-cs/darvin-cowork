/**
 * usePermissions — Go 权限审批请求的队列 + 响应。
 *
 * 监听 `permission_request` 事件入队；一次只展示一个（PermissionModal
 * 消费 current）。用户 allow / deny 后调 window.darvin.respondPermission
 * 回传 Go（agent.permission_response），Go 侧解除对应工具调用的阻塞。
 */
import { ref } from 'vue';
import type { DarvinPermissionBehavior } from '../../shared/darvin-api';

interface PendingPermission {
  sessionId: string;
  requestId: string;
  toolName: string;
  toolInput: unknown;
  dangerLevel: 'safe' | 'caution' | 'destructive';
  reason: string;
}

const queue = ref<PendingPermission[]>([]);
const current = ref<PendingPermission | null>(null);
let initialized = false;

function popNext(): void {
  if (current.value) return;
  const next = queue.value[0];
  if (next) {
    queue.value = queue.value.slice(1);
    current.value = next;
  }
}

export function usePermissions() {
  if (!initialized) {
    initialized = true;
    window.darvin.onEvent((e) => {
      if (e.type === 'permission_request') {
        queue.value = [...queue.value, e];
        if (!current.value) popNext();
      }
    });
  }

  async function respond(
    behavior: DarvinPermissionBehavior,
    opts?: { updatedInput?: unknown; message?: string; interrupt?: boolean; remember?: boolean },
  ): Promise<void> {
    const req = current.value;
    if (!req) return;
    await window.darvin.respondPermission({
      sessionId: req.sessionId,
      requestId: req.requestId,
      behavior,
      ...opts,
    });
    current.value = null;
    popNext();
  }

  return { queue, current, respond };
}
