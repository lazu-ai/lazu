// @ts-nocheck
/**
 * Calibrated UI audit — desktop + true-mobile screenshots and layout metrics
 * (horizontal overflow, sub-44px tap targets, tiny text) for the public site
 * and the console.
 *
 * Run:
 *   RUN_UI_AUDIT=1 LOGIN_USER=root LOGIN_PASS='...' \
 *   PLAYWRIGHT_BASE_URL=http://127.0.0.1:5173 \
 *   bun playwright test --config=playwright.config.ts \
 *     tests/smoke/ui-audit.spec.ts --workers=1
 *
 * Output: ../../.local-results/screenshots/ui-audit-<STAMP|recheck>/
 *
 * Unlike a naive screenshot run this records the rendered viewport width so
 * "looks cut off" can be trusted (calibration), uses a real mobile context
 * (isMobile), logs in once, and throttles itself to avoid 429 contamination.
 * Not part of `bun run test:smoke` — gated behind RUN_UI_AUDIT=1.
 */
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { test } from '@playwright/test';

test.skip(
  process.env.RUN_UI_AUDIT !== '1',
  'set RUN_UI_AUDIT=1 to run the calibrated UI audit',
);

const STAMP = process.env.STAMP || 'recheck';
const OUT_DIR = resolve(
  process.cwd(),
  `../../.local-results/screenshots/ui-audit-${STAMP}`,
);

const USER = process.env.LOGIN_USER || 'root';
const PASS = process.env.LOGIN_PASS || '';

const VIEWPORTS = {
  desktop: { width: 1280, height: 900, isMobile: false, dsf: 1 },
  mobile: { width: 390, height: 844, isMobile: true, dsf: 2 },
};

const PAGES = [
  { group: 'public', name: 'home', route: '/' },
  { group: 'public', name: 'faq', route: '/faq' },
  { group: 'public', name: 'models', route: '/models' },
  { group: 'auth', name: 'login', route: '/login' },
  { group: 'auth', name: 'register', route: '/register' },
  { group: 'console', name: 'console', route: '/console' },
  { group: 'console', name: 'console-models', route: '/console/models' },
  { group: 'console', name: 'console-channel', route: '/console/channel' },
  { group: 'console', name: 'console-token', route: '/console/token' },
  { group: 'console', name: 'console-usage', route: '/console/usage' },
  { group: 'console', name: 'console-setting', route: '/console/setting' },
  { group: 'console', name: 'console-wallet', route: '/console/wallet' },
];

// Runs inside the page. Collects layout-health metrics for the current route.
const MEASURE = () => {
  const de = document.documentElement;
  const vw = de.clientWidth;
  const clamp = (s, n) =>
    (s || '').toString().trim().replace(/\s+/g, ' ').slice(0, n);
  const visible = (r) => r.width > 0 && r.height > 0;

  const overflowEls = [];
  for (const el of document.body.querySelectorAll('*')) {
    const r = el.getBoundingClientRect();
    if (r.right > vw + 1 && visible(r) && r.width <= 4000) {
      overflowEls.push({
        tag: el.tagName.toLowerCase(),
        cls: clamp(el.className, 70),
        left: Math.round(r.left),
        right: Math.round(r.right),
        width: Math.round(r.width),
        text: clamp(el.textContent, 70),
      });
    }
  }

  const smallTargets = [];
  const interactive = document.querySelectorAll(
    'a, button, input, select, [role="button"], [role="tab"], [role="menuitem"], [role="switch"]',
  );
  for (const el of interactive) {
    const r = el.getBoundingClientRect();
    if (!visible(r)) continue;
    if (r.width < 44 || r.height < 44) {
      smallTargets.push({
        tag: el.tagName.toLowerCase(),
        text: clamp(el.textContent || el.getAttribute('aria-label'), 36),
        w: Math.round(r.width),
        h: Math.round(r.height),
        x: Math.round(r.left),
        y: Math.round(r.top),
      });
    }
  }

  const tinyText = [];
  for (const el of document.body.querySelectorAll('*')) {
    if (el.children.length) continue; // leaf nodes only
    const txt = (el.textContent || '').trim();
    if (!txt) continue;
    const r = el.getBoundingClientRect();
    if (!visible(r)) continue;
    const fs = parseFloat(getComputedStyle(el).fontSize);
    if (fs && fs < 12) {
      tinyText.push({
        tag: el.tagName.toLowerCase(),
        fs,
        text: clamp(txt, 36),
      });
    }
  }

  return {
    width: window.innerWidth,
    height: window.innerHeight,
    clientWidth: vw,
    clientHeight: de.clientHeight,
    scrollWidth: de.scrollWidth,
    scrollHeight: de.scrollHeight,
    overflowX: Math.max(0, de.scrollWidth - vw),
    overflowElCount: overflowEls.length,
    overflowEls: overflowEls.slice(0, 14),
    smallTargetCount: smallTargets.length,
    smallTargets: smallTargets.slice(0, 18),
    tinyTextCount: tinyText.length,
    tinyText: tinyText.slice(0, 24),
    title: document.title,
    url: location.pathname,
  };
};

let storageStatePath = '';
const results = [];

async function gotoSettled(page, route) {
  try {
    await page.goto(route, { waitUntil: 'domcontentloaded', timeout: 25_000 });
  } catch (err) {
    if (err?.name !== 'TimeoutError') throw err;
  }
  // Vite dev server keeps an HMR websocket open, so `networkidle` never
  // settles — wait a fixed beat for React mount + first data paint instead.
  await page.waitForLoadState('load').catch(() => {});
  await page.waitForTimeout(1800);
}

test.beforeAll(async ({ browser }) => {
  await mkdir(OUT_DIR, { recursive: true });
  if (!PASS) {
    console.warn(
      '[ui-audit] LOGIN_PASS not set — console pages will be logged out',
    );
    return;
  }
  const ctx = await browser.newContext({
    viewport: { width: 1280, height: 900 },
  });
  const page = await ctx.newPage();
  try {
    await gotoSettled(page, '/login');
    await page
      .getByPlaceholder(/username|email|用户名|邮箱/i)
      .first()
      .fill(USER);
    await page
      .getByPlaceholder(/password|密码/i)
      .first()
      .fill(PASS);
    const checkbox = page.getByRole('checkbox').first();
    if (await checkbox.isVisible().catch(() => false)) {
      await checkbox.click().catch(() => {});
    }
    await page
      .getByRole('button', { name: /continue|sign in|继续|登录|登入/i })
      .first()
      .click();
    await page.waitForURL((u) => !u.pathname.startsWith('/login'), {
      timeout: 15_000,
    });
    storageStatePath = resolve(OUT_DIR, 'storage-state.json');
    await ctx.storageState({ path: storageStatePath });
  } catch (err) {
    console.warn(
      '[ui-audit] login failed — console pages will be logged out:',
      err?.message,
    );
    storageStatePath = '';
  } finally {
    await ctx.close();
  }
});

for (const [device, vp] of Object.entries(VIEWPORTS)) {
  test(`ui-audit ${device}`, async ({ browser }) => {
    test.setTimeout(600_000);
    const ctx = await browser.newContext({
      viewport: { width: vp.width, height: vp.height },
      isMobile: vp.isMobile,
      hasTouch: vp.isMobile,
      deviceScaleFactor: vp.dsf,
      storageState: storageStatePath || undefined,
    });
    const page = await ctx.newPage();

    for (const p of PAGES) {
      const base = { device, group: p.group, name: p.name, route: p.route };
      try {
        await gotoSettled(page, p.route);
        await page.screenshot({
          path: resolve(OUT_DIR, `${device}-${p.name}.png`),
          fullPage: false,
        });
        await page.screenshot({
          path: resolve(OUT_DIR, `${device}-${p.name}-full.png`),
          fullPage: true,
        });
        const m = await page.evaluate(MEASURE);
        if (p === PAGES[0]) {
          console.log(
            `[calibration] ${device}: configured=${vp.width} rendered clientWidth=${m.clientWidth}` +
              (Math.abs(m.clientWidth - vp.width) > 20
                ? '  <-- MISCALIBRATED'
                : '  OK'),
          );
        }
        results.push({ ...base, ...m });
      } catch (err) {
        results.push({ ...base, error: String(err?.message || err) });
      }
      await page.waitForTimeout(600); // throttle
    }
    await ctx.close();
  });
}

test.afterAll(async () => {
  results.sort((a, b) => (a.device + a.name).localeCompare(b.device + b.name));
  await writeFile(
    resolve(OUT_DIR, 'audit-results.json'),
    JSON.stringify(results, null, 2),
  );
  console.log(`[ui-audit] wrote ${results.length} page results to ${OUT_DIR}`);
});
