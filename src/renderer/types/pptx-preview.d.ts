/** pptx-preview 无内置类型声明，这里补齐最小面。该包导出命名 `init`。 */
declare module 'pptx-preview' {
  interface PptxPreviewOptions {
    width?: number;
    mode?: 'list' | 'single';
  }
  interface PptxPreviewer {
    slideCount?: number;
    preview(data: ArrayBuffer): Promise<void>;
    destroy(): void;
  }
  export function init(container: HTMLElement, options?: PptxPreviewOptions): PptxPreviewer;
}
