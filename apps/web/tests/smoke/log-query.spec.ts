// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('usage log route renders with mocked query results', async ({ page }) => {
  const app = await setupMockedApp(page);
  await page.addInitScript(() => {
    localStorage.setItem(
      'page-console-log-columns-user-v6',
      JSON.stringify({ ip: false }),
    );
  });
  await expectRouteReady(page, '/console/usage?tab=logs');
  await expect(page.getByRole('columnheader', { name: '输入' })).toBeVisible();
  await app.expectNoUnexpectedErrors();
});

test('platform-scope admin queries the platform-wide log endpoints', async ({
  page,
}) => {
  const requestedLogPaths: string[] = [];
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url) => {
      const { pathname } = new URL(url);
      if (pathname.startsWith('/api/log')) requestedLogPaths.push(pathname);
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/usage?tab=logs');

  // The admin routes are the genuinely global ones; /api/log/self/* is
  // scoped to the caller and must not be what root sees.
  await expect.poll(() => requestedLogPaths).toContain('/api/log/');
  expect(
    requestedLogPaths.some((path) => path.startsWith('/api/log/self')),
  ).toBe(false);

  await expect(page.getByRole('columnheader', { name: '用户' })).toBeVisible();
  const userCell = page.locator('.logv2-desktop-master .logv2-who').first();
  await expect(userCell).toContainText('admin');
  await expect(userCell).toContainText('#1');
  await app.expectNoUnexpectedErrors();
});

test('workspace-scope user still queries the self log endpoints', async ({
  page,
}) => {
  const requestedLogPaths: string[] = [];
  const app = await setupMockedApp(page, {
    user: { id: 7, username: 'member', display_name: 'Member', role: 1 },
    payloadFor: (url) => {
      const { pathname } = new URL(url);
      if (pathname.startsWith('/api/log')) requestedLogPaths.push(pathname);
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/usage?tab=logs');

  await expect.poll(() => requestedLogPaths).toContain('/api/log/self/');
  expect(requestedLogPaths).not.toContain('/api/log/');
  await expect(page.getByRole('columnheader', { name: '用户' })).toHaveCount(0);
  await app.expectNoUnexpectedErrors();
});

test('usage user view ranks accounts and drills back into the detail view', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url) => {
      const { pathname, searchParams } = new URL(url);
      if (pathname !== '/api/data/dashboard') return undefined;
      // The aggregate must not downgrade itself to the caller's own rows.
      expect(searchParams.get('scope')).toBe('platform');
      return {
        success: true,
        message: '',
        data: {
          scope: 'global',
          totals: { amount_microusd: 300000, request_count: 30 },
          by_user: [
            {
              id: 7,
              name: 'grace',
              request_count: 20,
              amount_microusd: 200000,
              prompt_tokens: 1200,
              completion_tokens: 400,
              error_count: 1,
            },
            {
              id: 1,
              name: 'admin',
              request_count: 10,
              amount_microusd: 100000,
              prompt_tokens: 600,
              completion_tokens: 200,
            },
          ],
          time_series: [],
        },
      };
    },
  });
  await expectRouteReady(page, '/console/usage?tab=logs');

  await page.getByRole('button', { name: '用户消费', exact: true }).click();
  await expect(page).toHaveURL(/view=users/);
  const ranking = page.locator('.logv2-card table').first();
  await expect(ranking.getByText('grace')).toBeVisible();
  await expect(ranking.getByText('5.0%')).toBeVisible();
  await expect(page.getByText(/2 个活跃用户/)).toBeVisible();

  await page
    .getByRole('button', { name: /查看明细/ })
    .first()
    .click();
  await expect(page).not.toHaveURL(/view=users/);
  await expect(page.getByText('用户：grace')).toBeVisible();
  await app.expectNoUnexpectedErrors();
});

test('usage user ranking carries active group and channel filters', async ({
  page,
}) => {
  const aggregateQueries: URLSearchParams[] = [];
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url) => {
      const parsed = new URL(url);
      if (parsed.pathname !== '/api/data/dashboard') return undefined;
      aggregateQueries.push(parsed.searchParams);
      return {
        success: true,
        message: '',
        data: { by_user: [], time_series: [], totals: {} },
      };
    },
  });
  await expectRouteReady(page, '/console/usage?tab=logs');

  await page.getByRole('button', { name: /^筛选/ }).click();
  await page.locator('#log-channel').fill('1');
  await page.getByRole('button', { name: '完成', exact: true }).click();
  // The log's group is the lane; the 线路 switch sets it and searches.
  await page
    .getByRole('group', { name: '线路' })
    .getByRole('button', { name: '稳定', exact: true })
    .click();
  await page.getByRole('button', { name: '用户消费', exact: true }).click();

  await expect
    .poll(() =>
      aggregateQueries.some(
        (query) =>
          query.get('group') === 'direct' && query.get('channel') === '1',
      ),
    )
    .toBe(true);
  await app.expectNoUnexpectedErrors();
});

test('usage log deep link fetches a receipt outside the current page', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  let requestedReceipt = false;
  await page.route('**/api/log/self/search?**', async (route) => {
    const url = new URL(route.request().url());
    requestedReceipt =
      url.searchParams.get('keyword') === 'req-health-deep-link';
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: [
          {
            id: 999,
            request_id: 'req-health-deep-link',
            model_name: 'deep-link-model',
            type: 2,
            created_at: 1735689500,
          },
        ],
      }),
    });
  });

  await expectRouteReady(
    page,
    '/console/usage?tab=logs&request=req-health-deep-link',
  );
  await expect(
    page.getByLabel('Selected request receipt').getByText('deep-link-model'),
  ).toBeVisible();
  expect(requestedReceipt).toBe(true);
  await app.expectNoUnexpectedErrors();
});

test('usage receipt inspector is keyboard accessible and revealed on mobile', async ({
  page,
}) => {
  await page.setViewportSize({ width: 800, height: 900 });
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/usage?tab=logs');

  // The row opens by pointer; its time cell is the keyboard handle and says
  // whether the detail is open.
  const inspectButton = page.locator('tr.logv2-row .logv2-time').first();
  await expect(inspectButton).toHaveAttribute('aria-expanded', 'false');
  await inspectButton.focus();
  await inspectButton.press('Enter');

  const detail = page.getByLabel('Selected request receipt');
  await expect(detail).toBeFocused();
  // The row above already names the model; the detail carries the request
  // id a customer quotes to support.
  await expect(detail).toContainText('req-smoke-usage');
  await expect(page).toHaveURL(/request=req-smoke-usage/);
  await expect
    .poll(async () =>
      detail.evaluate((element) => {
        const rect = element.getBoundingClientRect();
        return rect.top >= 0 && rect.top < window.innerHeight;
      }),
    )
    .toBe(true);

  // The same control closes it again.
  await expect(inspectButton).toHaveAttribute('aria-expanded', 'true');
  await inspectButton.click();
  await expect(detail).toHaveCount(0);
  await app.expectNoUnexpectedErrors();
});

test('opening a request detail does not move the reader', async ({ page }) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/usage?tab=logs');

  // The panel used to render after the table and its pagination and then call
  // scrollIntoView, which threw the reader to the bottom of the page and cost
  // them their position in the list. It now expands under its own row.
  await page.evaluate(() => {
    window.__lazuScrolled = false;
    window.addEventListener('scroll', () => {
      window.__lazuScrolled = true;
    });
  });

  const trigger = page.locator('tr.logv2-row .logv2-time').first();
  const rowBoxBefore = await trigger.boundingBox();
  await trigger.click();

  const detail = page.getByLabel('Selected request receipt');
  await expect(detail).toBeVisible();
  await expect(detail).toBeFocused();

  // The trigger has not moved on screen, and nothing scrolled.
  const rowBoxAfter = await trigger.boundingBox();
  expect(Math.abs((rowBoxAfter?.y ?? 0) - (rowBoxBefore?.y ?? 0))).toBeLessThan(
    2,
  );
  expect(await page.evaluate(() => window.__lazuScrolled)).toBe(false);

  // The detail sits immediately beneath the row it belongs to.
  const adjacent = await detail.evaluate((el) => {
    const detailRow = el.closest('tr');
    const previous = detailRow?.previousElementSibling;
    return previous?.classList.contains('logv2-row') ?? false;
  });
  expect(adjacent).toBe(true);

  await app.expectNoUnexpectedErrors();
});

test('usage log keeps default-hidden fields in mobile cards', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/usage?tab=logs');
  await expect(
    page.locator('.lazu-card-list__item').first().getByText('输入 → 输出', {
      exact: true,
    }),
  ).toBeVisible();
  await app.expectNoUnexpectedErrors();
});

test('mobile request log keeps pagination available', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const requestedPages: number[] = [];
  const app = await setupMockedApp(page, {
    payloadFor: (url) => {
      const requestUrl = new URL(url);
      if (requestUrl.pathname !== '/api/log/self/') return undefined;
      const requestedPage = Number(requestUrl.searchParams.get('p') || 1);
      requestedPages.push(requestedPage);
      return {
        success: true,
        message: '',
        data: {
          items: [
            {
              id: requestedPage,
              request_id: `req-mobile-page-${requestedPage}`,
              username: 'admin',
              token_name: 'Smoke token',
              model_name: `mobile-page-${requestedPage}`,
              prompt_tokens: 12,
              completion_tokens: 24,
              amount_microusd: 36,
              created_at: 1735689600 + requestedPage,
            },
          ],
          page: requestedPage,
          page_size: 10,
          total: 21,
        },
      };
    },
  });
  await expectRouteReady(page, '/console/usage?tab=logs');

  const mobileMaster = page.locator('.logv2-mobile-master');
  await expect(mobileMaster.getByText('mobile-page-1')).toBeVisible();
  await mobileMaster
    .locator('.lazu-pagination-button')
    .filter({ hasText: '2' })
    .click();
  await expect(mobileMaster.getByText('mobile-page-2')).toBeVisible();
  expect(requestedPages).toContain(2);
  await app.expectNoUnexpectedErrors();
});

test('deep links carry their scope into the request the log page sends', async ({
  page,
}) => {
  // The overview's failure notice links with ?type=5 and a model card with
  // ?model=…; both are promises about what the destination shows, so the
  // query the page issues has to carry them.
  const queries: string[] = [];
  const app = await setupMockedApp(page, {
    payloadFor: (url) => {
      const parsed = new URL(url);
      if (parsed.pathname.startsWith('/api/log/self/'))
        queries.push(parsed.search);
      return undefined;
    },
  });
  await expectRouteReady(
    page,
    '/console/usage?tab=logs&type=5&model=gpt-4o-mini',
  );

  await expect
    .poll(() => queries.some((query) => query.includes('type=5')))
    .toBe(true);
  await expect
    .poll(() =>
      queries.some((query) => query.includes('model_name=gpt-4o-mini')),
    )
    .toBe(true);

  await app.expectNoUnexpectedErrors();
});
