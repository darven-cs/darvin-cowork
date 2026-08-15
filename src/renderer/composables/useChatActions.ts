/**
 * Chat 发送动作：append user message → call api → start assistant message
 *
 * HomeView（首屏输入跳转）和 ChatView（对话内发送）共用；
 * busy 标志由调用方 ref 传入，避免 composable 内部隐藏状态。
 *
 * session 数据所有权归 main：调用 `window.darvin.prompt` 不传 sessionId，
 * main 从 SessionStore 拿到当前 active session。
 */

import { useMessages } from './useMessages';
import { useSession } from './useSession';
import { useImportedFiles } from './useImportedFiles';
import { useModel } from './useModel';
import { parseSlashCommand, translateSkillError } from './slash';
import { t } from '../services/i18n';
import { showToast } from '../services/toast';
import type { DarvinAttachmentRef, DarvinImageRef } from '../../shared/darvin-api';

/**
 * 把附件路径以 `文件:` / `图片:` 行嵌入内容（LobsterAI 式 finalPrompt）：
 * 展示即发送、随 content 持久化，刷新后仍能在聊天里「引用」文件。
 */
function buildAttachmentContent(
  content: string,
  files: DarvinAttachmentRef[],
  images: DarvinImageRef[],
): string {
  const lines = [
    ...files.map((f) => `${t('attachment.fileLabel')}: ${f.path}`),
    ...images.map((i) => `${t('attachment.imageLabel')}: ${i.path}`),
  ];
  if (lines.length === 0) return content;
  return `${content}\n\n${lines.join('\n')}`;
}

export function useChatActions() {
  const messages = useMessages();
  const session = useSession();
  const imported = useImportedFiles();
  const model = useModel();

  async function send(content: string, busyRef: { value: boolean }): Promise<void> {
    if (!content.trim()) return;
    // `/` 前缀路由。`//` 转义：去掉首字符 `/` 走普通 prompt（不触发 skill）。
    const escaped = content.startsWith('//');
    const routeContent = escaped ? content.slice(1) : content;
    const slash = escaped ? null : parseSlashCommand(content);

    // compose 态（点过「新建任务」）或没有 active session：先建会话再发。
    // 用首条消息当标题，避免出现一堆「新建会话」空壳。
    let sessId = session.activeSessionId.value;
    if (session.draftMode.value || sessId === null) {
      const created = await session.createSession(routeContent.trim().slice(0, 30));
      sessId = created.id;
      session.draftMode.value = false;
    }
    busyRef.value = true;
    const { files, images } = imported.splitForSend();
    // 气泡展示原始输入（含 `//` 转义，regenerate 时才能区分）；Go 端拿 routeContent。
    const displayContent = buildAttachmentContent(content, files, images);
    const finalContent = buildAttachmentContent(routeContent, files, images);
    messages.appendUserMessage(sessId, displayContent, undefined, undefined, files, images);

    if (slash) {
      try {
        const r = await window.darvin.invokeSkill({
          sessionId: sessId,
          skillId: slash.skillId,
          args: slash.args,
          content: finalContent,
        });
        if (files.length > 0 || images.length > 0) imported.clear();
        messages.startAssistantMessage(r.sessionId, r.messageId);
      } catch (err) {
        // 校验失败（skill 不存在 / 禁用 / 不可手动触发）→ toast，不画错误气泡。
        showToast(translateSkillError(err as Error, slash.skillId), 'error');
      } finally {
        busyRef.value = false;
      }
      return;
    }

    try {
      // 仅当当前模型仍在可选列表里才发按次覆盖；否则用 session 默认，
      // 避免把过期/未配置 provider 的模型 id 发到错误 wire（如 gpt-4o → anthropic 404）。
      const overrideProvider = model.providerOf(model.currentModel.value);
      const r = await window.darvin.prompt({
        content: finalContent,
        attachments: files.map((f) => f.path),
        images,
        provider: overrideProvider || undefined,
        model: overrideProvider ? model.currentModel.value : undefined,
      });
      // 附件是路径引用（无复制），发送即消费：清空暂存，不删用户原文件。
      if (files.length > 0 || images.length > 0) imported.clear();
      messages.startAssistantMessage(r.sessionId, r.messageId);
    } catch (err) {
      const mid = `m-err-${Date.now().toString(36)}`;
      messages.startAssistantMessage(sessId, mid);
      messages.appendEvent({ type: 'error', messageId: mid, message: (err as Error).message });
    } finally {
      busyRef.value = false;
    }
  }

  async function copy(text: string): Promise<boolean> {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Electron file:// 下 clipboard API 可能不可用，退回 execCommand
      try {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
        return true;
      } catch {
        return false;
      }
    }
  }

  /** 在 turn 内重新生成：找到含 messageId 的 turn 的 user 消息，重发 prompt。 */
  async function regenerate(messageId: string): Promise<void> {
    const target = findTurnUserMessage(messageId);
    if (!target) return;
    // 重新生成 `/skill args` 的 turn 时再次触发 skill；`//` 转义保留原样走普通 prompt。
    const escaped = target.content.startsWith('//');
    const routeContent = escaped ? target.content.slice(1) : target.content;
    const slash = escaped ? null : parseSlashCommand(target.content);
    if (slash) {
      try {
        const r = await window.darvin.invokeSkill({
          sessionId: target.sessionId,
          skillId: slash.skillId,
          args: slash.args,
          content: routeContent,
        });
        messages.startAssistantMessage(target.sessionId, r.messageId);
      } catch (err) {
        showToast(translateSkillError(err as Error, slash.skillId), 'error');
      }
      return;
    }
    try {
      const r = await window.darvin.prompt({ content: routeContent });
      messages.startAssistantMessage(target.sessionId, r.messageId);
    } catch (err) {
      const mid = `m-err-${Date.now().toString(36)}`;
      messages.startAssistantMessage(target.sessionId, mid);
      messages.appendEvent({ type: 'error', messageId: mid, message: (err as Error).message });
    }
  }

  function findTurnUserMessage(messageId: string): { content: string; sessionId: string } | null {
    const buckets = messages.messagesBySessionId.value;
    for (const [sid, list] of Object.entries(buckets)) {
      let lastUser: { content: string } | null = null;
      for (const m of list) {
        if (m.role === 'user') lastUser = m;
        if (m.id === messageId) {
          return lastUser ? { content: lastUser.content, sessionId: sid } : null;
        }
      }
    }
    return null;
  }

  return { send, copy, regenerate };
}
