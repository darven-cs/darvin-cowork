/**
 * artifact 文档读取工具：内联 content 优先，缺则经 IPC 读本地文件。
 * office 二进制产物（docx/pdf/xlsx/pptx）的 content 为空，走 filePath 读取。
 */

/** 读 artifact 对应文件为 data URL（相对路径按 workspace 根解析，main 端处理）。 */
export async function readArtifactFile(filePath: string): Promise<string> {
  const r = await window.darvin.readFileAsDataUrl(filePath);
  if (!r.success || !r.dataUrl) throw new Error(r.error ?? 'read failed');
  return r.dataUrl;
}

/** 把 data URL 转成 ArrayBuffer（office 库的输入格式）。 */
export function dataUrlToArrayBuffer(dataUrl: string): ArrayBuffer {
  const base64 = dataUrl.slice(dataUrl.indexOf(',') + 1);
  const bin = atob(base64);
  const buf = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) {
    buf[i] = bin.charCodeAt(i);
  }
  return buf.buffer;
}

/** 取 artifact 的 ArrayBuffer：content 若是 data URL 直接用，否则读文件。 */
export async function artifactToBuffer(filePath?: string, content?: string): Promise<ArrayBuffer> {
  if (content && content.startsWith('data:')) return dataUrlToArrayBuffer(content);
  if (filePath) return dataUrlToArrayBuffer(await readArtifactFile(filePath));
  throw new Error('no file source');
}
