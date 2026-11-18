// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp, fail } from './helpers/mock-app';

test('wallet exposes separate top-up and billing views with reconciled statement', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  const usageRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === '/api/data/dashboard/self';
  });
  await page.goto('/console/wallet', { waitUntil: 'domcontentloaded' });
  await expect(page).toHaveURL(/\/console\/wallet$/);
  await usageRequest;

  await expect(page.locator('.wal-balance-value')).toHaveText('$5.00');
  await expect(page.locator('.wal-topup-stage')).toBeVisible();

  await page.getByRole('tab', { name: '账单' }).click();
  await expect(page).toHaveURL(/\/console\/wallet\?tab=billing/);
  await expect(page.locator('.wal-statement-strip')).toBeVisible();
  await expect(page.locator('.wal-billing-card')).toBeVisible();

  await app.expectNoUnexpectedErrors();
});

test('wallet reflows on mobile without horizontal page overflow', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/wallet');

  await expect(page.locator('.wal-topup-stage')).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(1);

  await page.getByRole('tab', { name: '账单' }).click();
  await expect(page.locator('.wal-billing-stage')).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(1);

  await app.expectNoUnexpectedErrors();
});

test('wallet billing does not present zero statement figures when ledger fails', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url) => {
      if (new URL(url).pathname === '/api/user/wallet/ledger') {
        return fail(500, 'internal_error');
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/wallet?tab=billing');

  await expect(page.locator('.wal-billing-card')).toContainText('重试');
  await expect(page.locator('.wal-statement-cell__value').first()).toHaveText(
    '—',
  );
  await app.expectNoUnexpectedErrors();
});

test('wallet separates paid and credited amounts and links a ledger day to logs', async ({
  page,
}) => {
  const now = Math.floor(Date.now() / 1000);
  const completedAt = Math.floor(
    new Date('2026-08-02T12:00:00Z').getTime() / 1000,
  );
  const app = await setupMockedApp(page, {
    payloadFor: (url) => {
      const requestUrl = new URL(url);
      if (requestUrl.pathname === '/api/user/wallet/ledger') {
        return {
          success: true,
          message: '',
          data: {
            opening_balance_microusd: 0,
            items: [
              {
                closing_balance_microusd: 109_750_000,
                credit_microusd: 110_000_000,
                date: new Date(now * 1000).toISOString().slice(0, 10),
                spend_microusd: 250_000,
              },
            ],
          },
        };
      }
      if (
        requestUrl.pathname === '/api/user/topup/self' ||
        requestUrl.pathname === '/api/user/topup'
      ) {
        return {
          success: true,
          message: '',
          data: {
            items: [
              {
                amount: 100,
                complete_time: completedAt,
                credited_microusd: 110_000_000,
                create_time: Math.floor(
                  new Date('2026-07-01T12:00:00Z').getTime() / 1000,
                ),
                money: 110,
                paid_amount: 100,
                payment_method: 'stripe',
                status: 'success',
                trade_no: 'wallet-ledger-smoke',
              },
            ],
            page: 1,
            page_size: 100,
            total: 1,
          },
        };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/wallet?tab=billing');

  const ledgerRow = page.locator('.wal-ledger tbody tr').first();
  await expect(ledgerRow).toContainText('+$110.00');

  await page.getByRole('tab', { name: /充值订单/ }).click();
  const billingCard = page.locator('.wal-billing-card');
  await expect(billingCard).toContainText('$110.00');
  await expect(billingCard).toContainText('$100.00');
  await expect(billingCard).toContainText('2026-08-02');

  await page.getByRole('tab', { name: '每日汇总' }).click();
  const logRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return (
      url.pathname === '/api/log/self/' &&
      Number(url.searchParams.get('start_timestamp')) > 0 &&
      Number(url.searchParams.get('end_timestamp')) >
        Number(url.searchParams.get('start_timestamp'))
    );
  });
  await ledgerRow.getByRole('link').click();
  await expect(page).toHaveURL(/\/console\/usage\?tab=logs&start_timestamp=/);
  await logRequest;
  await app.expectNoUnexpectedErrors();
});

test('manual top-up completion uses a cancelable confirmation', async ({
  page,
}) => {
  let completions = 0;
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url, request) => {
      const path = new URL(url).pathname;
      if (path === '/api/user/topup/complete') {
        completions++;
        expect(request.postDataJSON()).toEqual({
          trade_no: 'review-pending-topup',
        });
        return { success: true };
      }
      if (path === '/api/user/topup')
        return {
          success: true,
          data: {
            items: [
              {
                id: 1,
                trade_no: 'review-pending-topup',
                status: 'pending',
                amount: 10,
                create_time: 1788624000,
              },
            ],
            total: 1,
          },
        };
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/wallet?tab=billing&view=orders');
  const billingCard = page.locator('.wal-billing-card');
  const complete = billingCard.getByRole('button', {
    name: '确认到账',
    exact: true,
  });
  const confirm = page.getByRole('dialog', { name: '确认到账', exact: true });
  await complete.click();
  await expect(confirm).toBeVisible();
  await expect
    .poll(() =>
      confirm.evaluate((dialog) => dialog.contains(document.activeElement)),
    )
    .toBe(true);
  await page.keyboard.press('Escape');
  await expect(confirm).toBeHidden();
  await expect(billingCard).toBeVisible();
  expect(completions).toBe(0);
  await complete.click();
  await confirm.getByRole('button', { name: '确认到账', exact: true }).click();
  await expect.poll(() => completions).toBe(1);
  await app.expectNoUnexpectedErrors();
});
