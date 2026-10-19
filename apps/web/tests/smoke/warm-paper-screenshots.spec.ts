// @ts-nocheck
import { mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { test } from '@playwright/test';

// 输出到仓库根的 .local-results/screenshots/。整个 .local-results/ 已 gitignore。
// 默认 STAMP=latest（每次 run 自动覆盖，不留历史）。
// 需要基线对比时手动设 STAMP，例如：
//   STAMP=pre-vocs-migration bun run test:smoke
// 然后把 warm-paper-pre-vocs-migration/ 移到 snapshots/ 下手动归档：
//   mv .local-results/screenshots/warm-paper-pre-vocs-migration \
//      .local-results/screenshots/snapshots/pre-vocs-migration
const OUT_DIR = resolve(
  process.cwd(),
  '../../.local-results/screenshots/warm-paper-' +
    (process.env.STAMP || 'latest'),
);

const VIEWPORTS = {
  desktop: { width: 1440, height: 900 },
  mobile: { width: 390, height: 844 },
};

const PAGES = [
  { group: 'public', name: 'home', route: '/' },
  { group: 'public', name: 'pricing', route: '/pricing' },
  { group: 'public', name: 'models', route: '/models' },
  { group: 'auth', name: 'login', route: '/login' },
  { group: 'auth', name: 'register', route: '/register' },
  { group: 'auth', name: 'reset', route: '/reset' },
  { group: 'console', name: 'dashboard', route: '/console' },
  { group: 'console', name: 'token', route: '/console/token' },
  { group: 'console', name: 'log', route: '/console/usage?tab=logs' },
  { group: 'console', name: 'playground', route: '/console/playground' },
  { group: 'console', name: 'wallet', route: '/console/wallet' },
  { group: 'console', name: 'personal', route: '/console/personal' },
  { group: 'console', name: 'setting', route: '/console/setting' },
];

test.beforeAll(async () => {
  await mkdir(OUT_DIR, { recursive: true });
});

for (const [device, viewport] of Object.entries(VIEWPORTS)) {
  for (const page of PAGES) {
    test(`${device} ${page.group} ${page.name}`, async ({ browser }) => {
      const ctx = await browser.newContext({ viewport });
      const tab = await ctx.newPage();
      const fileName = `${device}-${page.group}-${page.name}.png`;
      const file = resolve(OUT_DIR, fileName);
      await mkdir(dirname(file), { recursive: true });
      try {
        await tab.goto(page.route, {
          waitUntil: 'networkidle',
          timeout: 12_000,
        });
      } catch (err) {
        // Only swallow networkidle timeouts — real navigation failures
        // (connection refused, DNS, cert errors) must still fail the test.
        if (err?.name !== 'TimeoutError') {
          throw err;
        }
      }
      await tab.waitForTimeout(600);
      await tab.screenshot({ path: file, fullPage: false });
      await ctx.close();
    });
  }
}
