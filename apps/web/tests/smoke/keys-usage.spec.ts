// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('the retired key inspector route lands on API Keys', async ({ page }) => {
  await setupMockedApp(page);
  await page.goto('/console/key');
  await page.waitForURL('**/console/token');
  await expect(page.getByRole('heading', { name: 'API Keys' })).toBeVisible();
});

test('a Key Pass keeps its inspector instead of a forbidden page', async ({
  page,
}) => {
  // /console/token admits workspace managers and members only, so sending a
  // Key Pass there would replace its inspector with the forbidden page. Its
  // bookmark goes to the public inspector the retired route used to wrap.
  await setupMockedApp(page, {
    consoleScope: 'key:ci-key',
    user: { key_name: 'ci-key' },
  });
  await page.goto('/console/key');
  await page.waitForURL((url) => url.pathname === '/key');
});

test('a key row expands into its own usage instead of opening the editor', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/token');

  await expect(page.locator('.tok-usage')).toHaveCount(0);
  // Click the name cell: the row ignores clicks that land on a control.
  await page.locator('tbody tr').first().getByText('Smoke token').click();

  // The row's read-only detail opens in place (yunmu-console.md rule 7): the
  // quota it is bound by, when it was last used, and a way into its requests.
  const panel = page.locator('.tok-usage');
  await expect(panel).toBeVisible();
  await expect(panel).toContainText('额度');
  await expect(panel).toContainText('最近使用');
  await expect(panel.getByRole('link', { name: /请求日志/ })).toHaveAttribute(
    'href',
    // The usage page reads the Key from `key`; `token_name` is not parsed
    // there, so linking with it would open an unfiltered log.
    /\/console\/usage\?key=/,
  );
  // Clicking the row must not have opened the edit form.
  await expect(page.locator('.tokv2-drawer')).toHaveCount(0);

  await page.locator('tbody tr').first().getByText('Smoke token').click();
  await expect(page.locator('.tok-usage')).toHaveCount(0);

  await app.expectNoUnexpectedErrors();
});

test('Keys with the same name keep their spend isolated by ID', async ({
  page,
}) => {
  // Spend uses the immutable ID, even when two Keys share a display name.
  const shared = (id: number) => ({
    created_time: 1735689600,
    id,
    key: `sk-shared-${id}`,
    name: 'prod',
    remain_quota: 500000,
    status: 1,
    unlimited_quota: false,
    used_quota: 0,
  });
  const app = await setupMockedApp(page, {
    payloadFor: (raw: string) => {
      const url = new URL(raw);
      if (url.pathname === '/api/log/self/stat') {
        expect(url.searchParams.has('token_name')).toBe(false);
        return {
          success: true,
          data: {
            amount_microusd:
              url.searchParams.get('token_id') === '1' ? 1_000_000 : 7_000_000,
          },
        };
      }
      if (
        url.pathname.startsWith('/api/token') &&
        !url.pathname.includes('stat')
      )
        return {
          success: true,
          message: '',
          data: {
            items: [shared(1), shared(2)],
            page: 1,
            page_size: 10,
            total: 2,
          },
        };
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/token');

  await page.locator('tbody tr').first().getByText('prod').click();
  const panel = page.locator('.tok-usage');
  await expect(panel).toBeVisible();
  await expect(panel.locator('dd').first()).toHaveText('$1');
  await page
    .locator('tbody tr')
    .filter({ hasText: 'prod' })
    .last()
    .getByText('prod', { exact: true })
    .click();
  await expect(
    page.locator('.tok-usage').last().locator('dd').first(),
  ).toHaveText('$7');

  await app.expectNoUnexpectedErrors();
});
