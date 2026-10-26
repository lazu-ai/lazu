// @ts-nocheck
/* The opening page's chunk is requested at startup, alongside the
 * translations, instead of after them. Warming a console page's data at
 * startup must not add prompts or redirects when nobody is signed in or the
 * session has ended. */
import { readFileSync } from 'node:fs';
import { basename, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { setupMockedApp } from './helpers/mock-app';

const HERE = dirname(fileURLToPath(import.meta.url));
const manifest = JSON.parse(
  readFileSync(resolve(HERE, '../../dist/.vite/manifest.json'), 'utf8'),
);
const chunk = (key: string) => basename(manifest[key].file);
const fileOf = (url: string) => basename(new URL(url).pathname);
const toasts = (page) =>
  page.getByRole('region', { name: /Notifications/ }).getByRole('listitem');

for (const [path, entry] of [
  ['/console', 'src/pages/Dashboard/index.tsx'],
  ['/console/token', 'src/pages/Token/index.tsx'],
]) {
  test(`requests the ${path} page chunk before the translations finish`, async ({
    page,
  }) => {
    await setupMockedApp(page, { language: 'zh' });
    let order = 0;
    let pageRequested = 0;
    let translationsLoaded = 0;
    page.on('request', (request) => {
      if (fileOf(request.url()) === chunk(entry)) pageRequested ||= ++order;
    });
    page.on('requestfinished', (request) => {
      if (
        fileOf(request.url()) === chunk('virtual:lazu-locale/translation/zh')
      ) {
        translationsLoaded ||= ++order;
      }
    });
    await page.goto(path);
    await expect(page.locator('main').first()).toBeVisible();
    await expect.poll(() => pageRequested).toBeGreaterThan(0);
    expect(translationsLoaded).toBeGreaterThan(0);
    expect(pageRequested).toBeLessThan(translationsLoaded);
  });
}

const sessionExpired = (route) =>
  route.fulfill({
    status: 401,
    contentType: 'application/json',
    body: JSON.stringify({
      success: false,
      error: {
        code: 'session_expired',
        type: 'authentication_error',
        request_id: 'req_mock',
      },
    }),
  });

for (const path of ['/console', '/console/models', '/console/token']) {
  test(`a signed-out visit to ${path} goes to sign-in without a prompt`, async ({
    page,
  }) => {
    await setupMockedApp(page, { language: 'zh' });
    await page.addInitScript(() => localStorage.removeItem('user'));
    const consoleData = [];
    page.on('request', (request) => {
      const { pathname } = new URL(request.url());
      if (
        pathname.startsWith('/api/') &&
        !['/api/status', '/api/user/self', '/api/auth/oidc/status'].includes(
          pathname,
        )
      )
        consoleData.push(pathname);
    });
    await page.goto(path);
    await expect(page).toHaveURL(/\/login$/);
    await page.waitForTimeout(1000);
    await expect(toasts(page)).toHaveCount(0);
    // Nobody is signed in, so no console data is asked for.
    expect(consoleData).toEqual([]);
  });
}

test('an ended session signs out once, with one prompt', async ({ page }) => {
  await setupMockedApp(page, { language: 'zh' });
  await page.route(
    (url) =>
      url.pathname.startsWith('/api/') &&
      url.pathname !== '/api/status' &&
      !url.pathname.startsWith('/api/auth/'),
    sessionExpired,
  );
  await page.goto('/console/token');
  await expect(page).toHaveURL(/\/login\?expired=true$/);
  await page.waitForTimeout(1000);
  await expect(toasts(page)).toHaveCount(1);
  await expect(page.getByText('未登录或登录已过期，请重新登录')).toHaveCount(1);
});
