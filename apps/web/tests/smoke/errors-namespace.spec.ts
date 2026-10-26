// @ts-nocheck
/* The failure copy (`errors` namespace) is not part of the first screen: it
 * is fetched in the background once the main translations are in. A failure
 * that arrives before that still reads in the visitor's language, because
 * the API error path and showError() wait for the namespace. */
import { readFileSync } from 'node:fs';
import { basename, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { setupMockedApp } from './helpers/mock-app';

const HERE = dirname(fileURLToPath(import.meta.url));
const manifest = JSON.parse(
  readFileSync(resolve(HERE, '../../dist/.vite/manifest.json'), 'utf8'),
);
const errorChunks = new Set(
  Object.entries(manifest)
    .filter(([key]) => key.startsWith('virtual:lazu-locale/errors/'))
    .map(([, entry]) => basename(entry.file)),
);
const isErrorChunk = (url: string) =>
  errorChunks.has(basename(new URL(url).pathname));
const isTokenList = (url: URL) => url.pathname.startsWith('/api/token');

const delayStatus = async (page, ms: number) => {
  await page.route('**/api/status', async (route) => {
    const response = await route.fetch().catch(() => null);
    await new Promise((done) => setTimeout(done, ms));
    if (response) await route.fulfill({ response });
    else await route.fallback();
  });
};

const translationChunk = (language: string) =>
  basename(manifest[`virtual:lazu-locale/translation/${language}`].file);

test('fetches the errors namespace only after the main translations', async ({
  page,
}) => {
  expect(errorChunks.size).toBe(4);
  await setupMockedApp(page, { language: 'zh' });
  let order = 0;
  let translationLoaded = 0;
  let errorsRequested = 0;
  page.on('requestfinished', (request) => {
    if (basename(new URL(request.url()).pathname) === translationChunk('zh')) {
      translationLoaded ||= ++order;
    }
  });
  page.on('request', (request) => {
    if (isErrorChunk(request.url())) errorsRequested ||= ++order;
  });
  const errorsChunk = page.waitForRequest((request) =>
    isErrorChunk(request.url()),
  );
  await page.goto('/console/token');
  await errorsChunk;
  expect(translationLoaded).toBeGreaterThan(0);
  expect(errorsRequested).toBeGreaterThan(translationLoaded);
});

const fail401 = (code: string) => (route) =>
  route.fulfill({
    status: 401,
    contentType: 'application/json',
    body: JSON.stringify({
      success: false,
      error: { code, type: 'authentication_error', request_id: 'req_mock' },
    }),
  });

test('a 401 before the namespace loads still reads in Chinese', async ({
  page,
}) => {
  await setupMockedApp(page, { language: 'zh' });
  await delayStatus(page, 2000);
  // A rejected API key is a 401 that keeps the console session: its copy
  // comes from the errors namespace.
  await page.route(isTokenList, fail401('invalid_api_key'));
  await page.goto('/console/token');
  await expect(
    page.getByText('API Key 无效。请检查 Key 是否复制完整').first(),
  ).toBeVisible();
});

test('an ended session before the namespace loads still reads in Chinese', async ({
  page,
}) => {
  await setupMockedApp(page, { language: 'zh' });
  await delayStatus(page, 2000);
  await page.route(isTokenList, fail401('session_expired'));
  await page.goto('/console/token');
  await expect(
    page.getByText('未登录或登录已过期，请重新登录').first(),
  ).toBeVisible();
});

test('a dropped connection still reads in Chinese', async ({ page }) => {
  await setupMockedApp(page, { language: 'zh' });
  await delayStatus(page, 2000);
  await page.route(isTokenList, (route) => route.abort('internetdisconnected'));
  await page.goto('/console/token');
  await expect(
    page.getByText('没有收到服务器响应，操作结果尚未确认。').first(),
  ).toBeVisible();
});
