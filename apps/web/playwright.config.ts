import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from '@playwright/test';

const port = Number(process.env.PLAYWRIGHT_PORT || 4173);
const baseURL = process.env.PLAYWRIGHT_BASE_URL || `http://127.0.0.1:${port}`;

// 所有"本地产物"统一落到仓库根的 .local-results/。该目录已 gitignore，
// 避免 trace、HTML 报告、临时截图意外被提交。详见根目录 CONTRIBUTING.md。
// 注：apps/web 的 package.json 是 "type": "module"，__dirname 不可用，
// 必须从 import.meta.url 派生（否则 Playwright 加载 config 时会 ReferenceError）。
const HERE = dirname(fileURLToPath(import.meta.url));
const LOCAL_RESULTS_ROOT = resolve(HERE, '../../.local-results');

export default defineConfig({
  testDir: './tests/smoke',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,
  // 失败 trace / 截图的输出目录
  outputDir: resolve(LOCAL_RESULTS_ROOT, 'test-results'),
  reporter: process.env.CI
    ? 'line'
    : [
        ['list'],
        [
          'html',
          {
            outputFolder: resolve(LOCAL_RESULTS_ROOT, 'playwright-report'),
            open: 'never',
          },
        ],
      ],
  timeout: 30_000,
  expect: {
    timeout: 5_000,
  },
  use: {
    baseURL,
    trace: 'retain-on-failure',
  },
  webServer: {
    command: `bun run preview -- --host 127.0.0.1 --port ${port}`,
    url: baseURL,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
