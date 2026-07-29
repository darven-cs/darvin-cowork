/// <reference types="vite/client" />

import type { DarvinApi } from '../shared/darvin-api';

declare global {
  interface Window {
    readonly darvin: DarvinApi;
  }
}

export {};
