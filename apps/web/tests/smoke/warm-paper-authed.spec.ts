// @ts-nocheck
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { test } from '@playwright/test';

// Opt-in only. Default `bun run test:smoke` runs against `vite preview`
// with no auth backend; this suite does a real /api/user/login round
// trip, so leaving it un-gated turns CI red on every commit. Run with
// `RUN_AUTHED_SMOKE=1` (and a live backend on the configured base URL)
// when you actually want the authed screenshots.
test.skip(
  process.env.RUN_AUTHED_SMOKE !== '1',
  'authed smoke disabled: set RUN_AUTHED_SMOKE=1 with a live backend',
);

// 见 warm-paper-screenshots.spec.ts 头部关于 .local-results/ 的约定说明。
const OUT_DIR = resolve(
  process.cwd(),
  '../../.local-results/screenshots/warm-paper-authed-' +
    (process.env.STAMP || 'latest'),
);

// 不要把真实密码塞进默认值。
// 注意：test.skip 只让测试被标记为 skipped，**不阻止顶层语句执行**——
// 所以这里的凭据检查必须用 RUN_AUTHED_SMOKE === '1' 双重守卫，
// 否则默认 `bun run test:smoke`（不设环境变量）会被这条 throw 直接挂掉。
const USER = process.env.LOGIN_USER;
const PASS = process.env.LOGIN_PASS;
if (process.env.RUN_AUTHED_SMOKE === '1' && (!USER || !PASS)) {
  throw new Error(
    'warm-paper-authed.spec.ts: LOGIN_USER and LOGIN_PASS must be set when RUN_AUTHED_SMOKE=1',
  );
}

const VIEWPORTS = {
  desktop: { width: 1440, height: 900 },
  mobile: { width: 390, height: 844 },
};

const PAGES = [
  { name: 'dashboard', route: '/console' },
  { name: 'token', route: '/console/token' },
  { name: 'log', route: '/console/usage?tab=logs' },
  { name: 'wallet', route: '/console/wallet' },
  { name: 'personal', route: '/console/personal' },
  { name: 'setting', route: '/console/setting' },
  { name: 'channel', route: '/console/channel' },
  { name: 'models-admin', route: '/console/models' },
];

async function login(page) {
  await page.goto('/login', { waitUntil: 'networkidle' });
  // Username + password fields are first/second textbox
  const userInput = page
    .getByPlaceholder(/username|email|用户名|邮箱/i)
    .first();
  await userInput.fill(USER);
  const passInput = page.getByPlaceholder(/password|密码/i).first();
  await passInput.fill(PASS);
  // Wait briefly for status fetch so the TOS checkbox renders (when enabled)
  // before we attempt to interact / submit. Without this the checkbox can
  // appear AFTER the submit click and validation silently blocks the request.
  await page.waitForTimeout(900);
  const checkbox = page.getByRole('checkbox').first();
  if (await checkbox.isVisible().catch(() => false)) {
    await checkbox.click().catch(() => {});
  } else {
    const accept = page.getByText(/agree|同意/i).first();
    if (await accept.isVisible().catch(() => false)) {
      await accept.click().catch(() => {});
    }
  }
  // Click Continue / 继续 / 登录 — covers en + zh translations.
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

for (const [device, viewport] of Object.entries(VIEWPORTS)) {
  for (const p of PAGES) {
    test(`${device} console ${p.name}`, async ({ browser }) => {
      const ctx = await browser.newContext({ viewport });
      const page = await ctx.newPage();
      try {
        await login(page);
      } catch (e) {
        await page.screenshot({
          path: resolve(OUT_DIR, `${device}-LOGIN-FAIL-${p.name}.png`),
        });
        await ctx.close();
        throw e;
      }
      try {
        await page.goto(p.route, {
          waitUntil: 'networkidle',
          timeout: 15_000,
        });
      } catch (err) {
        // Swallow only networkidle timeouts (some console pages keep
        // long-poll connections open). Real navigation failures
        // (connection refused, DNS, cert errors, route not found) must
        // still fail the test — otherwise we silently screenshot the
        // previous page and ship a green CI on a broken route.
        if (err?.name !== 'TimeoutError') {
          throw err;
        }
      }
      await page.waitForTimeout(800);
      if (process.env.PROBE_BUTTONS === '1') {
        const buttons = await page
          .locator('header.console-page-topbar button')
          .allInnerTexts();
        // eslint-disable-next-line no-console
        console.log(
          `PROBE [${device}/${p.name}] TOPBAR:`,
          JSON.stringify(buttons),
        );
        const sidebarItems = await page
          .locator('.sidebar-v2__item-label')
          .allInnerTexts();
        // eslint-disable-next-line no-console
        console.log(
          `PROBE [${device}/${p.name}] SIDEBAR:`,
          JSON.stringify(sidebarItems),
        );
      }
      await page.screenshot({
        path: resolve(OUT_DIR, `${device}-console-${p.name}.png`),
        fullPage: false,
      });
      await ctx.close();
    });
  }
}
