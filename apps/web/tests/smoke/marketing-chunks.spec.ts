// @ts-nocheck
/* Marketing pages are chunks of their own. A prerendered page links its
 * chunk's stylesheets and preloads its scripts from the HTML, hydrates
 * without React errors, and the console never downloads them. */
import { readFileSync } from 'node:fs';
import { basename, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { setupMockedApp } from './helpers/mock-app';

const HERE = dirname(fileURLToPath(import.meta.url));
const manifest = JSON.parse(
  readFileSync(resolve(HERE, '../../dist/.vite/manifest.json'), 'utf8'),
);
const PAGES = {
  '/': 'src/pages/Home/index.tsx',
  '/faq': 'src/pages/FAQ/index.tsx',
  '/about': 'src/pages/About/index.tsx',
  '/privacy': 'src/pages/PrivacyPolicy/index.tsx',
  '/terms': 'src/pages/UserAgreement/index.tsx',
};
const marketingChunks = new Set(
  Object.values(PAGES).map((key) => basename(manifest[key].file)),
);
const reactErrors = (page) => {
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => {
    if (
      message.type() === 'error' &&
      /hydrat|Minified React error|did not match/i.test(message.text())
    )
      errors.push(message.text());
  });
  return errors;
};

for (const [path, key] of [
  ['/', PAGES['/']],
  ['/zh/faq/', PAGES['/faq']],
  ['/ja/terms/', PAGES['/terms']],
]) {
  test(`${path} preloads its page chunk and hydrates cleanly`, async ({
    page,
  }) => {
    await setupMockedApp(page, { language: 'en' });
    const errors = reactErrors(page);
    const response = await page.goto(path);
    const html = await response.text();
    const file = manifest[key].file;
    expect(html).toContain(
      `<link rel="modulepreload" crossorigin href="/${file}"`,
    );
    for (const css of manifest[key].css || []) {
      expect(html).toContain(`/${css}`);
    }
    await expect(page.locator('#root')).not.toBeEmpty();
    await page.waitForLoadState('networkidle');
    expect(errors).toEqual([]);
  });
}

test('the console does not download marketing pages', async ({ page }) => {
  await setupMockedApp(page, { language: 'zh' });
  // nginx answers app-owned paths with app.html; vite preview would fall
  // back to index.html, the prerendered home page.
  const appShell = readFileSync(resolve(HERE, '../../dist/app.html'), 'utf8');
  await page.route('**/console/token', (route) =>
    route.fulfill({ status: 200, contentType: 'text/html', body: appShell }),
  );
  const fetched = [];
  page.on('request', (request) => {
    const file = basename(new URL(request.url()).pathname);
    if (marketingChunks.has(file)) fetched.push(file);
  });
  await page.goto('/console/token');
  await page.waitForLoadState('networkidle');
  expect(fetched).toEqual([]);
});
