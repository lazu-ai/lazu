// @ts-nocheck
/**
 * One-off screenshot capture for UI redesign review.
 * Run:  RUN_UI_REVIEW=1 LOGIN_USER=root LOGIN_PASS='...' \
 *       PLAYWRIGHT_BASE_URL=http://localhost:5173 \
 *       bun playwright test --config=playwright.config.ts \
 *         tests/smoke/ui-review-screenshots.spec.ts
 *
 * Output: ../../.local-results/ui-review/screenshots/<page>.png
 *
 * Delete this file after the review is done. It is intentionally NOT
 * part of `bun run test:smoke`.
 */
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { test } from '@playwright/test';

test.skip(
  process.env.RUN_UI_REVIEW !== '1',
  'set RUN_UI_REVIEW=1 to run UI screenshot capture',
);

const OUT_DIR = resolve(
  process.cwd(),
  '../../.local-results/ui-review/screenshots',
);

const USER = process.env.LOGIN_USER;
const PASS = process.env.LOGIN_PASS;
if (process.env.RUN_UI_REVIEW === '1' && (!USER || !PASS)) {
  throw new Error('LOGIN_USER and LOGIN_PASS must be set');
}

const PAGES = [
  { name: '01-dashboard', route: '/console' },
  { name: '02-token-api-keys', route: '/console/token' },
  { name: '03-usage', route: '/console/usage' },
  { name: '04-wallet', route: '/console/wallet' },
  { name: '06-personal', route: '/console/personal' },
  { name: '09-channel', route: '/console/channel' },
  { name: '10-models', route: '/console/models' },
  { name: '13-setting', route: '/console/setting' },
];

async function login(page) {
  await page.goto('/login', { waitUntil: 'networkidle' });
  const userInput = page
    .getByPlaceholder(/username|email|用户名|邮箱/i)
    .first();
  await userInput.fill(USER);
  const passInput = page.getByPlaceholder(/password|密码/i).first();
  await passInput.fill(PASS);
  await page.waitForTimeout(800);
  const checkbox = page.getByRole('checkbox').first();
  if (await checkbox.isVisible().catch(() => false)) {
    await checkbox.click().catch(() => {});
  }
  const submit = page
    .getByRole('button', { name: /continue|sign in|继续|登录|登入/i })
    .first();
  await submit.click();
  await page.waitForURL((u) => !u.pathname.startsWith('/login'), {
    timeout: 10_000,
  });
}

test.beforeAll(async () => {
  await mkdir(OUT_DIR, { recursive: true });
});

for (const p of PAGES) {
  test(`ui-review ${p.name}`, async ({ browser }) => {
    const ctx = await browser.newContext({
      viewport: { width: 1440, height: 900 },
    });
    const page = await ctx.newPage();
    try {
      await login(page);
    } catch (e) {
      await page.screenshot({
        path: resolve(OUT_DIR, `LOGIN-FAIL-${p.name}.png`),
      });
      await ctx.close();
      throw e;
    }
    try {
      await page.goto(p.route, { waitUntil: 'networkidle', timeout: 15_000 });
    } catch (err) {
      if (err?.name !== 'TimeoutError') throw err;
    }
    await page.waitForTimeout(1200);
    await page.screenshot({
      path: resolve(OUT_DIR, `${p.name}.png`),
      fullPage: true,
    });
    await ctx.close();
  });
}

// Setting page has 5 sub-tabs that are buttons (no independent URL), need to
// click + wait + screenshot each.
const SETTING_SUBTABS = [
  { name: '13a-setting-access', label: /^(Access|访问)$/ },
  { name: '13b-setting-billing', label: /^(Billing|充值)$/ },
  { name: '13c-setting-ops', label: /^(Ops|运营)$/ },
  { name: '13d-setting-branding', label: /^(Branding|站点)$/ },
  { name: '13e-setting-upstream', label: /^(Upstream|上游)$/ },
];

for (const t of SETTING_SUBTABS) {
  test(`ui-review ${t.name}`, async ({ browser }) => {
    const ctx = await browser.newContext({
      viewport: { width: 1440, height: 900 },
    });
    const page = await ctx.newPage();
    try {
      await login(page);
    } catch (e) {
      await ctx.close();
      throw e;
    }
    await page.goto('/console/setting', {
      waitUntil: 'networkidle',
      timeout: 15_000,
    });
    await page.waitForTimeout(800);
    // Click the sub-tab in setting's left rail
    const tab = page.getByRole('tab', { name: t.label }).first();
    await tab.click();
    await page.waitForTimeout(1500); // wait for tab content to mount
    await page.screenshot({
      path: resolve(OUT_DIR, `${t.name}.png`),
      fullPage: true,
    });
    await ctx.close();
  });
}
