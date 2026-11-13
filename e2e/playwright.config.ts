import { defineConfig } from '@playwright/test';
import { sandbox } from './lib/sandbox.js';

// Every spec runs against this checkout's e2e sandbox (task e2e:serve), read
// from .data/e2e/env.json. Specs share one panel, so they run one at a time.
const env = sandbox();

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 5 * 60_000,
  expect: { timeout: 15_000 },
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: env.panelUrl,
    // Traefik's default certificate is self-signed inside the sandbox.
    ignoreHTTPSErrors: true,
    actionTimeout: 20_000,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
});
