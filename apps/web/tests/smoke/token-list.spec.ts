// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('token management route renders with mocked token data', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/token');
  // Each quota cell reads used on the left and the budget on the right.
  const finite = page.locator('.tok-quota:not(.is-unlimited)').first();
  await expect(finite.locator('.tok-quota__used')).toHaveText('$0');
  await expect(finite.locator('.tok-quota__total')).toHaveText('$0.5');
  const unlimited = page.locator('.tok-quota.is-unlimited').first();
  await expect(unlimited.locator('.tok-quota__used')).toHaveText('$2.5');
  await expect(unlimited.locator('.tok-quota__total')).toHaveText('不限');
  await app.expectNoUnexpectedErrors();
});

test('token editor remains dismissible while token details load', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: async (url) => {
      if (new URL(url).pathname === '/api/token/1') {
        await new Promise((resolve) => setTimeout(resolve, 1_000));
        return {
          success: true,
          message: '',
          data: {
            id: 1,
            name: 'Smoke token',
            key: 'sk-smoke',
            status: 1,
          },
        };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/token');

  await page
    .getByRole('row', { name: /Smoke token/ })
    .first()
    .getByRole('button', { name: '编辑', exact: true })
    .click();
  const modal = page.locator('.lazu-modal-content');
  await expect(modal).toBeVisible();
  await expect(modal.locator('.lazu-modal-close')).toBeEnabled();
  await page.keyboard.press('Escape');
  await expect(modal).toHaveCount(0);
  await app.expectNoUnexpectedErrors();
});

test('token table migrates saved columns and keeps fields in mobile cards', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  await page.addInitScript(() => {
    localStorage.setItem(
      'page-console-token-columns',
      JSON.stringify({ key: true }),
    );
  });
  await expectRouteReady(page, '/console/token');
  await expect(page.getByRole('columnheader', { name: '密钥' })).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await expect(page.locator('.lazu-card-list__item').first()).toContainText(
    '密钥',
  );
  await app.expectNoUnexpectedErrors();
});

test('column visibility stays hidden while the table uses mobile cards', async ({
  page,
}) => {
  await page.setViewportSize({ width: 700, height: 844 });
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/token');

  await expect(page.getByRole('button', { name: /列|Columns/ })).toBeHidden();
  await app.expectNoUnexpectedErrors();
});

test('selecting the filtered page never includes hidden API keys', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url) => {
      const requestUrl = new URL(url);
      if (['/api/token', '/api/token/'].includes(requestUrl.pathname)) {
        const status = requestUrl.searchParams.get('status');
        const enabled = {
          id: 1,
          name: 'Enabled token',
          key: 'sk-enabled',
          status: 1,
          unlimited_quota: true,
          used_quota: 0,
          remain_quota: 0,
          created_time: 1735689600,
        };
        const disabled = {
          id: 2,
          name: 'Disabled token from another unfiltered page',
          key: 'sk-disabled',
          status: 2,
          unlimited_quota: true,
          used_quota: 0,
          remain_quota: 0,
          created_time: 1735689600,
        };
        // The overview walks every Key in pages of 100; it sees both, which
        // is what gives the status tabs their counts.
        if (requestUrl.searchParams.get('size') === '100') {
          return {
            success: true,
            message: '',
            data: {
              items: [enabled, disabled],
              page: 1,
              page_size: 100,
              total: 2,
            },
          };
        }
        return {
          success: true,
          message: '',
          data: {
            items:
              status === '2'
                ? [disabled]
                : status === '1'
                  ? [enabled]
                  : [enabled],
            page: 1,
            page_size: 10,
            total: status ? 1 : 2,
          },
        };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/token');

  await page.getByRole('radio', { name: /^已禁用/ }).click();
  await expect(
    page.getByRole('row', { name: /Disabled token from another/ }),
  ).toBeVisible();
  await expect(page.getByRole('row', { name: /Enabled token/ })).toHaveCount(0);

  await page.getByRole('checkbox', { name: /选择当前页/ }).click();
  await expect(page.locator('.tok-selection__count')).toHaveText('已选 1 把');

  await page.getByRole('button', { name: '取消选择', exact: true }).click();
  await page.getByRole('radio', { name: /^已启用/ }).click();
  await expect(page.locator('.tok-selection__count')).toHaveCount(0);
  await app.expectNoUnexpectedErrors();
});
