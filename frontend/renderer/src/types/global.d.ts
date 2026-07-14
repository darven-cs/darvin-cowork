import type { DarvinApi } from '../../../preload';

declare global {
  interface Window {
    api: DarvinApi;
  }
}
