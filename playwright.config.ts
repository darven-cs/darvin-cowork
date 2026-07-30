import { defineConfig } from '@playwright/test';

/**
 * Playwright config for the Electron app (spec FR-6.1).
 *
 * Two projects:
 *   - core: runs in every CI pass (no LLM required).
 *   - real-llm: gated on ANTHROPIC_API_KEY so dev machines without a
 *     key don't hit Anthropic on every run.
 *
 * The Electron binary path is read from PLAYWRIGHT_ELECTRON_BIN (set
 * by npm run e2e → scripts/e2e-prepare.sh, or by CI). When unset the
 * config falls back to the local forge build target.
 */
export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  fullyParallel: false,
  retries: 0,
  reporter: process.env.CI ? 'list' : 'list',
  use: {
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'core',
      testMatch: /.*\.spec\.ts$/,
      grep: /@core/,
    },
    {
      name: 'real-llm',
      testMatch: /.*\.spec\.ts$/,
      grep: /@real-llm/,
    },
  ],
});