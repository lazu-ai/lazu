// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('wallet route renders with mocked balance and usage data', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/wallet');
  await expect(page.locator('.wal-balance-summary')).toBeVisible();
  await app.expectNoUnexpectedErrors();
});

test('self-hosted wallet keeps the ledger when recharge is unavailable', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    status: { SidebarModulePreset: 'self-hosted' },
  });
  await expectRouteReady(page, '/console/wallet');
  await expect(page.locator('.wal-ledger-card')).toBeVisible();
  await expect(page.locator('.wal-topup-empty')).toBeVisible();
  await app.expectNoUnexpectedErrors();
});

test('top-up history labels the credited timestamp as effective time', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url) => {
      const { pathname } = new URL(url);
      if (
        pathname === '/api/user/topup' ||
        pathname === '/api/user/topup/self'
      ) {
        return {
          success: true,
          message: '',
          data: {
            items: [
              {
                amount: 10,
                complete_time: 1_787_000_000,
                money: 10,
                payment_method: 'stripe',
                status: 'success',
                trade_no: 'SMOKE-EFFECTIVE-TIME',
              },
            ],
            page: 1,
            page_size: 10,
            total: 1,
          },
        };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/wallet');

  await page.getByRole('button', { name: /History|历史记录/ }).click();
  await expect(
    page.getByRole('columnheader', { name: /Effective time|生效时间/ }),
  ).toBeVisible();
  await app.expectNoUnexpectedErrors();
});
