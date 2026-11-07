// The web UI end to end: Chromium against the real binary (e2e/global-setup.ts builds it and runs
// `serve`). `make e2e-ui` runs it; the HTML report in e2e-report/ is what an owner opens.
//
//   E2E_VIDEO=all   keep every test's video (by default a video is kept only for a failed test)
//   E2E_HEADED=1    run the browser headed
//
// Specs are `*.e2e.ts`, not `*.spec.ts`: vitest's default include would otherwise collect them into
// `npm test` (web-check).
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/*.e2e.ts',
  outputDir: './e2e-results/output',
  globalSetup: './e2e/global-setup.ts',
  // One server holds the run's state and the specs seed their own projects on it; one worker keeps the
  // order of a run, and so its screenshots and trace, the same each time.
  workers: 1,
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 15_000 },
  reporter: [['list'], ['html', { outputFolder: './e2e-report', open: 'never' }]],
  use: {
    headless: process.env.E2E_HEADED !== '1',
    viewport: { width: 1440, height: 900 },
    trace: 'on',
    screenshot: 'on',
    video: process.env.E2E_VIDEO === 'all' ? 'on' : 'retain-on-failure',
    actionTimeout: 15_000,
  },
  projects: [
    { name: 'ui', testIgnore: '**/route-guard.e2e.ts' },
    // After every UI spec: each route in src/App.tsx was reached by one of them.
    { name: 'route-guard', testMatch: '**/route-guard.e2e.ts', dependencies: ['ui'] },
  ],
});
