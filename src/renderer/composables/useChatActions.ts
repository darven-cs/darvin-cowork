/**
 * Chat 发送动作：append user message → call api → start assistant message
 *
 * HomeView（首屏输入跳转）和 ChatView（对话内发送）共用；
 * busy 标志由调用方 ref 传入，避免 composable 内部隐藏状态。
 */

import { useMessages } from './useMessages';
import { useSession } from './useSession';

export function useChatActions() {
  const messages = useMessages();
  const session = useSession();

  async function send(content: string, busyRef: { value: boolean }): Promise<void> {
    if (!content.trim()) return;
    busyRef.value = true;
    const sessId = session.currentSessionId.value;
    messages.appendUserMessage(sessId, content);
    try {
      const r = await window.darvin.prompt({ content, sessionId: sessId });
      messages.startAssistantMessage(r.sessionId, r.messageId);
    } catch (err) {
      const mid = `m-err-${Date.now().toString(36)}`;
      messages.startAssistantMessage(sessId, mid);
      messages.appendEvent({ type: 'error', messageId: mid, message: (err as Error).message });
    } finally {
      busyRef.value = false;
    }
  }

  return { send };
}