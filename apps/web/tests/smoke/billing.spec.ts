// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp, fail } from './helpers/mock-app';

test('legacy billing route redirects to the wallet with real usage data', async ({
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
  // The spend trend is on the page, not behind a disclosure: it explains the
  // balance above it and the ledger below it.
  const figures = page.locator('.wal-chart-card__figures');
  await expect(figures).toContainText('$0.25');
  await expect(figures).toContainText('请求 5');

  await expect(page.locator('.wal-chart-card__canvas')).toBeVisible();

  await app.expectNoUnexpectedErrors();
});

test('wallet reflows on mobile without horizontal page overflow', async ({
  page,
}) => {
  await page.setViewportSize({ width: 800, height: 900 });
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/wallet');

  await expect(page.locator('.wal-layout')).toBeVisible();
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

test('wallet does not present zero usage when the usage request fails', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url) => {
      if (new URL(url).pathname === '/api/data/dashboard/self') {
        return fail(500, 'internal_error');
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/wallet');

  await expect(page.getByText('用量数据加载失败').first()).toBeVisible();
  const figures = page.locator('.wal-chart-card__figures');
  await expect(figures).toContainText('消耗 —');
  await expect(figures).toContainText('请求 —');
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
  await expectRouteReady(page, '/console/wallet');

  const ledgerRow = page.locator('.wal-ledger tbody tr').first();
  await expect(ledgerRow).toContainText('+110.00');

  await page.getByRole('button', { name: '历史记录' }).click();
  const historyDialog = page.getByRole('dialog', { name: '充值记录' });
  await expect(historyDialog).toContainText('$110.00');
  await expect(historyDialog).toContainText('$100.00');
  await expect(historyDialog).toContainText('2026-08-02');
  await page.keyboard.press('Escape');

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
  await expectRouteReady(page, '/console/wallet');
  await page.getByRole('button', { name: '历史记录' }).click();
  const history = page.getByRole('dialog', { name: '充值记录' });
  const complete = history.getByRole('button', {
    name: '确认到账',
    exact: true,
  });
  const confirm = page.getByRole('dialog', { name: '确认到账', exact: true });
  await complete.click();
  // Escape before the confirmation opens (and takes focus) would reach the
  // history dialog instead.
  await expect(confirm).toBeVisible();
  await expect
    .poll(() =>
      confirm.evaluate((dialog) => dialog.contains(document.activeElement)),
    )
    .toBe(true);
  await page.keyboard.press('Escape');
  await expect(confirm).toBeHidden();
  await expect(history).toBeVisible();
  expect(completions).toBe(0);
  await complete.click();
  await confirm.getByRole('button', { name: '确认到账', exact: true }).click();
  await expect.poll(() => completions).toBe(1);
  await app.expectNoUnexpectedErrors();
});
