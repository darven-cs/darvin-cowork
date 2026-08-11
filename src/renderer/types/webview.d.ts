/** Electron <webview> 元素（Browser tab 网页载体）。 */
export interface WebviewElement extends HTMLElement {
  loadURL(url: string): Promise<void>;
  getURL(): string;
  getTitle(): string;
  goBack(): void;
  goForward(): void;
  reload(): void;
  stop(): void;
  canGoBack(): boolean;
  canGoForward(): boolean;
  setZoomFactor(factor: number): void;
  executeJavaScript<T = unknown>(code: string): Promise<T>;
  addEventListener(
    type: string,
    listener: (event: unknown) => void,
    options?: boolean | AddEventListenerOptions,
  ): void;
  removeEventListener(
    type: string,
    listener: (event: unknown) => void,
    options?: boolean | EventListenerOptions,
  ): void;
}
