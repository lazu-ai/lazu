// @ts-nocheck
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test } from '@playwright/test';

const output = resolve(
  process.cwd(),
  '../../.local-results/screenshots/brand-matrix-latest',
);
const cases = [
  {
    name: 'desktop-light',
    theme: 'light',
    viewport: { width: 1440, height: 900 },
  },
  {
    name: 'desktop-dark',
    theme: 'dark',
    viewport: { width: 1440, height: 900 },
  },
  {
    name: 'mobile-light',
    theme: 'light',
    viewport: { width: 390, height: 844 },
  },
  {
    name: 'mobile-narrow-light',
    theme: 'light',
    viewport: { width: 360, height: 800 },
    language: 'zh',
  },
  {
    name: 'mobile-compact-ja-light',
    theme: 'light',
    viewport: { width: 320, height: 720 },
    language: 'ja',
  },
  { name: 'mobile-dark', theme: 'dark', viewport: { width: 390, height: 844 } },
];

test.beforeAll(async () => mkdir(output, { recursive: true }));

async function mockPublicApis(page) {
  await page.route('**/api/status', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          setup: true,
          system_name: 'Lazu',
          server_address: 'http://127.0.0.1:4173',
        },
      }),
    }),
  );
  await page.route('**/api/home_page_content', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '{"success":true,"data":""}',
    }),
  );
  await page.route('**/api/auth/oidc/status', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '{"success":true,"data":{"enabled":false,"email_code_auth":true,"password_login":true,"providers":[]}}',
    }),
  );
}

for (const entry of cases) {
  test(`Brand matrix ${entry.name}`, async ({ browser }) => {
    const context = await browser.newContext({ viewport: entry.viewport });
    const page = await context.newPage();
    await mockPublicApis(page);
    await page.addInitScript(
      ({ language, theme }) => {
        localStorage.setItem('lazu.theme', theme);
        localStorage.setItem('theme-mode', theme);
        localStorage.setItem('i18nextLng', language);
      },
      { language: entry.language ?? 'en', theme: entry.theme },
    );
    await page.goto('/', { waitUntil: 'networkidle' });
    await expect(
      page.locator('.lazu-landing:not(.lazu-public-header)'),
    ).toBeVisible();
    const publicLogo = page.locator('.lazu-landing__brand .brand-logo');
    const publicLogoBox = await publicLogo.boundingBox();
    // logo-system.md § 5: the Primary lockup is never shown under 28px tall,
    // which keeps the serif wordmark's cap height above its 17px floor.
    expect(publicLogoBox?.height).toBeGreaterThanOrEqual(28);
    // The header carries the Primary lockup from the generator: the seal's
    // circular container on the 80-unit canvas, never a page-local rebuild.
    const publicLogoSource = decodeURIComponent(
      (await publicLogo.getAttribute('src')) ?? '',
    );
    expect(publicLogoSource).toMatch(/viewBox="-15 0 \d+ 80"/);
    expect(publicLogoSource).toContain('<circle cx="512" cy="512" r="512"');
    await expect(page.locator('.lazu-landing__brand > span')).toHaveCount(0);
    const navControlsFit = await page
      .locator('.lazu-landing__nav')
      .evaluate((nav) => {
        const viewportWidth = document.documentElement.clientWidth;
        return [...nav.querySelectorAll('a, button')].every((element) => {
          const box = element.getBoundingClientRect();
          return box.left >= 0 && box.right <= viewportWidth;
        });
      });
    expect(navControlsFit).toBe(true);
    if (entry.viewport.width <= 420) {
      const brandBox = await page.locator('.lazu-landing__brand').boundingBox();
      const signInBox = await page
        .locator('.lazu-landing__signin')
        .boundingBox();
      expect(signInBox?.y).toBeGreaterThan(
        (brandBox?.y ?? 0) + (brandBox?.height ?? 0),
      );
    }
    const contract = await page.evaluate(() => {
      const root = getComputedStyle(document.documentElement);
      return {
        action: root.getPropertyValue('--lazu-action').trim(),
        font: getComputedStyle(document.body).fontFamily,
        page: root.getPropertyValue('--lazu-page').trim(),
        overflow:
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
        theme: document.body.getAttribute('theme-mode'),
      };
    });
    expect(contract.theme).toBe(entry.theme);
    // G1 (BRAND.md, locked 2026-10-06): flat neutral ground and an ink
    // primary action. The retired Kumo warm orange must not return.
    expect(contract.page).toBe(entry.theme === 'dark' ? '#0f0f11' : '#f5f5f6');
    expect(contract.action).toBe(
      entry.theme === 'dark' ? '#ececee' : '#202329',
    );
    expect(contract.font).toContain('Geist');
    expect(contract.overflow).toBeLessThanOrEqual(1);
    await page.screenshot({
      path: resolve(output, `${entry.name}-home.png`),
      fullPage: true,
    });
    await context.close();
  });
}

test('Brand keyboard focus and reduced motion contracts', async ({
  browser,
}) => {
  const context = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    reducedMotion: 'reduce',
  });
  const page = await context.newPage();
  await mockPublicApis(page);
  await page.goto('/login');
  const email = page.getByRole('textbox', { name: 'Email' });
  await email.focus();
  await page.keyboard.press('Shift+Tab');
  const focused = page.locator(':focus-visible');
  await expect(focused).toBeVisible();
  const feedback = await focused.evaluate((element) => {
    const style = getComputedStyle(element);
    return { boxShadow: style.boxShadow, outline: style.outlineStyle };
  });
  expect(feedback.boxShadow !== 'none' || feedback.outline !== 'none').toBe(
    true,
  );
  const transition = await page
    .locator('.lazu-button')
    .first()
    .evaluate((element) => {
      const style = getComputedStyle(element);
      return {
        duration: Number.parseFloat(style.transitionDuration.split(',')[0]),
        property: style.transitionProperty,
      };
    });
  expect(transition.property).not.toContain('transform');
  expect(transition.duration).toBeGreaterThan(0);
  await context.close();
});

test('marketing theme changes update the default favicon', async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('lazu.theme', 'dark');
    localStorage.setItem('i18nextLng', 'en');
  });

  await page.goto('/');
  const favicon = page.locator('#app-favicon');
  const themeToggle = page.getByRole('button', { name: 'Toggle theme' });
  await expect(favicon).toHaveAttribute('href', /\/favicon-paper\.svg$/);

  await themeToggle.click();
  await expect(page.locator('body')).toHaveAttribute('theme-mode', 'light');
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('lazu.theme')))
    .toBe('light');
  await expect(favicon).toHaveAttribute('href', /\/favicon-graphite\.svg$/);
});
